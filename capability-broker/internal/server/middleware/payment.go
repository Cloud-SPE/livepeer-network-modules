package middleware

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/receipts"
	paymentsv1 "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// CapabilitySpec is what the payment middleware needs to pass to the
// daemon's OpenSession: the work-unit identifier and the wei price per
// work unit. Both come from the broker's host-config capabilities
// list.
type CapabilitySpec struct {
	WorkUnit            string
	PricePerWorkUnitWei *big.Int
	// PerUnits is the denominator the price is quoted over
	// (offering-axes.md §6). 0 means 1.
	PerUnits uint64
}

// CapabilityLookup resolves a (capability_id, offering_id) pair to its
// pricing metadata. The broker wires `s.lookup` into this; the
// middleware uses it without depending on internal/config.
type CapabilityLookup func(capability, offering string) (CapabilitySpec, bool)

// InterimDebitConfig governs the long-running session ticker per plan
// 0015 §7. Zero values are safe defaults: an empty config is equivalent
// to interim-debit disabled (v0.2 single-debit fall-through).
type InterimDebitConfig struct {
	// Interval is the tick cadence. 0 disables the ticker entirely.
	// Default 30s; recommended floor in production is 1s (the broker
	// logs WARN below 1s outside of test environments).
	Interval time.Duration
	// MinRunwayUnits is the minimum required runway passed to
	// SufficientBalance per tick. 60 is the recommended default for
	// `seconds-elapsed` workloads at the 30s tick cadence.
	MinRunwayUnits uint64
	// GraceOnInsufficient is the duration the middleware waits between
	// observing `sufficient=false` and terminating the handler. Zero
	// (the default) means hard-terminate immediately. Reserved for the
	// future mid-session ticket top-up plan; v0.1 ships with the flag
	// wired and the default zero (per plan 0015 §11 decision 3).
	GraceOnInsufficient time.Duration
}

// SessionState is the per-request handle the dispatch layer uses to
// publish a LiveCounter back to the payment middleware. The middleware
// creates one before invoking next.ServeHTTP and stuffs it into the
// request context; the dispatch layer reads it before invoking the
// driver and calls SetLiveCounter once the driver's Params are ready.
//
// SessionState is intentionally goroutine-safe: the ticker reads
// LiveCounter() concurrently with dispatch's SetLiveCounter call.
type SessionState struct {
	live       atomic.Pointer[liveCounterHolder]
	meta       atomic.Pointer[receiptMetaHolder]
	settlement atomic.Pointer[SettlementInputs]
}

type liveCounterHolder struct {
	lc extractors.LiveCounter
}

type receiptMetaHolder struct {
	meta ReceiptMeta
}

type ReceiptMeta struct {
	WorkID           string
	RoundID          string
	RequestID        string
	CapabilityID     string
	OfferingID       string
	MemberEthAddress string
	BackendID        string
	HostEnrollmentID string
	HardwareUnitID   string
	GPUUUID          string
	TemplateID       string
	ExpectedMaxUnits uint64
}

// SetLiveCounter publishes a LiveCounter for the in-flight session.
// Called by the dispatch layer once the driver's Params are constructed.
// nil is a no-op (mode driver does not support interim debit).
func (s *SessionState) SetLiveCounter(lc extractors.LiveCounter) {
	if s == nil || lc == nil {
		return
	}
	s.live.Store(&liveCounterHolder{lc: lc})
}

// LiveCounter returns the current LiveCounter or nil. Safe for
// concurrent use.
func (s *SessionState) LiveCounter() extractors.LiveCounter {
	if s == nil {
		return nil
	}
	if h := s.live.Load(); h != nil {
		return h.lc
	}
	return nil
}

func (s *SessionState) SetReceiptMeta(meta ReceiptMeta) {
	if s == nil {
		return
	}
	s.meta.Store(&receiptMetaHolder{meta: meta})
}

func (s *SessionState) ReceiptMeta() (ReceiptMeta, bool) {
	if s == nil {
		return ReceiptMeta{}, false
	}
	if h := s.meta.Load(); h != nil {
		return h.meta, true
	}
	return ReceiptMeta{}, false
}

// SetSettlementInputs publishes the inputs needed to build a
// SettlementRecord later. Long-lived session drivers (RTMP,
// session-control) snapshot these inputs onto their per-session
// records during Serve so they can emit settlement at session-close
// time, after the per-request payment middleware has long since
// returned.
func (s *SessionState) SetSettlementInputs(in SettlementInputs) {
	if s == nil {
		return
	}
	s.settlement.Store(&in)
}

// SettlementInputs returns the inputs published by the payment
// middleware, or (zero, false) if absent.
func (s *SessionState) SettlementInputs() (SettlementInputs, bool) {
	if s == nil {
		return SettlementInputs{}, false
	}
	if p := s.settlement.Load(); p != nil {
		return *p, true
	}
	return SettlementInputs{}, false
}

type sessionStateKey struct{}

// SessionStateFromContext returns the SessionState attached by the
// payment middleware, or nil if absent (e.g. unpaid routes).
func SessionStateFromContext(ctx context.Context) *SessionState {
	if v := ctx.Value(sessionStateKey{}); v != nil {
		if s, ok := v.(*SessionState); ok {
			return s
		}
	}
	return nil
}

// Payment is the payment-lifecycle middleware:
//
//	OpenSession(work_id, capability, offering, price, work_unit)
//	  → ProcessPayment(payment_bytes, work_id)
//	  → handler.Serve  (parallel: interim DebitBalance + SufficientBalance ticker)
//	  → final DebitBalance(sender, work_id, work_units, debit_seq=N+1)
//	  → CloseSession(sender, work_id)
//
// The payee-side work_id is derived from the payment's
// ticket_params.recipient_rand_hash when available. That aligns the
// broker's session lifecycle with the receiver-issued TicketParams used
// on the sender side. Legacy mock/stub payments fall back to the
// inbound request id so existing unit tests and smoke paths keep
// working.
//
// Decode/cross-check failures map to:
//   - missing/malformed Livepeer-Payment header → 401 + payment_invalid
//   - capability not found in host-config       → 404 + capability_not_served
//   - offering not found under capability       → 404 + offering_not_served
//   - daemon rejects (mismatch / bad sender)    → 401 + payment_invalid
//
// SettlementEncoder renders a settlement record for the wire. Injected
// so the middleware does not reach for the server's signing key, and so
// a test can assert on the record without a key at all.
type SettlementEncoder func(*paymentsv1.SettlementRecord) (string, error)

// DebitSeqAllocator hands out the next debit sequence for a work_id.
//
// It is injected rather than derived from request state because the seq
// space belongs to the work_id: a gateway reuses one ticket session
// across many exchanges, so anything counted per-request repeats and the
// payee — correctly deduplicating on (sender, work_id, debit_seq) —
// drops every debit after the first.
type DebitSeqAllocator func(workID string) (uint64, error)

func Payment(client payment.Client, lookup CapabilityLookup, idc InterimDebitConfig, receiptSink receipts.Client, encode SettlementEncoder, allocSeq DebitSeqAllocator) Middleware {
	if encode == nil {
		encode = encodeSettlementRecord
	}
	if allocSeq == nil {
		// In-process fallback for tests and for a broker with no state
		// store. Correct within a process, lost on restart — the same
		// caveat the in-process job idempotency carries, and logged the
		// same way at startup.
		var mu sync.Mutex
		counters := map[string]uint64{}
		allocSeq = func(workID string) (uint64, error) {
			mu.Lock()
			defer mu.Unlock()
			counters[workID]++
			return counters[workID], nil
		}
	}
	// Production-safety warning per plan 0015 §9.1: tick intervals
	// below 1s are intended for conformance fixtures only.
	if idc.Interval > 0 && idc.Interval < time.Second {
		log.Printf("warning: --interim-debit-interval=%s is below the 1s production floor; "+
			"this is intended for conformance / test environments only", idc.Interval)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paymentHeader := r.Header.Get(livepeerheader.Payment)
			if paymentHeader == "" {
				livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid,
					"missing Livepeer-Payment header")
				return
			}
			paymentBytes, err := base64.StdEncoding.DecodeString(paymentHeader)
			if err != nil {
				livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid,
					"Livepeer-Payment is not valid base64: "+err.Error())
				return
			}

			capability := r.Header.Get(livepeerheader.Capability)
			offering := r.Header.Get(livepeerheader.Offering)
			spec, ok := lookup(capability, offering)
			if !ok {
				livepeerheader.WriteError(w, http.StatusNotFound, livepeerheader.ErrCapabilityNotServed,
					"capability "+capability+"/"+offering+" is not served by this broker")
				return
			}
			if err := validateExpectedPriceForRequest(paymentBytes, capability, offering, spec); err != nil {
				livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch,
					"expected price mismatch: "+err.Error())
				return
			}

			workID, payeeOwned := payment.DerivePayeeWorkID(paymentBytes)
			if !payeeOwned {
				workID = RequestIDFromContext(r.Context())
				if workID == "" {
					// RequestID middleware always sets one; defense in depth.
					livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
						"missing request id (RequestID middleware not wired)")
					return
				}
			}

			ctx := r.Context()

			// 1. OpenSession. Idempotent: a retry with the same work_id
			//    returns OUTCOME_ALREADY_OPEN.
			if _, err := client.OpenSession(ctx, payment.OpenSessionRequest{
				WorkID:              workID,
				Capability:          capability,
				Offering:            offering,
				PricePerWorkUnitWei: spec.PricePerWorkUnitWei,
				PerUnits:            spec.PerUnits,
				WorkUnit:            spec.WorkUnit,
			}); err != nil {
				livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
					"open session: "+err.Error())
				return
			}

			// 2. ProcessPayment. Daemon decodes wire bytes, seals
			//    sender, credits EV.
			result, err := client.ProcessPayment(ctx, payment.ProcessPaymentRequest{
				WorkID:       workID,
				PaymentBytes: paymentBytes,
			})
			if err != nil {
				code, errCode := mapClientErr(err)
				livepeerheader.WriteError(w, code, errCode, "process payment: "+err.Error())
				return
			}

			// Close the payee session only if this exchange EXCLUSIVELY
			// owns it.
			//
			// A payment-derived work_id is the payee's ticket-session
			// rand hash, which a gateway reuses across many exchanges.
			// Closing it after one job destroyed the shared session:
			// every later job on it still CREDITED (the payee guards
			// credit on nothing) but could not DEBIT, since a closed
			// session refuses debits — so work was served free from the
			// second job onward. Closing also forfeits residual credit,
			// which is not this exchange's to forfeit.
			//
			// The old behaviour was correct when work_id WAS the request
			// id: that session belonged to one exchange. It stopped
			// being correct when the id became payment-derived, and
			// nothing noticed because the error was discarded.
			//
			// A shared session is closed by the payee's own lifecycle —
			// rand rotation and retention — which is where that decision
			// belongs.
			// Set when the final debit fails and is handed off for
			// durable retry. Declared here because the close below is
			// deferred before the debit runs.
			var debitOutstanding bool
			if !payeeOwned {
				defer func() {
					// A closed session refuses debits, so closing while
					// a retry is outstanding guarantees the retry can
					// never land — the work would stay unbilled no
					// matter how many attempts the budget allows. The
					// payee's own retention reclaims it instead.
					if debitOutstanding {
						return
					}
					_ = client.CloseSession(ctx, result.Sender, workID)
				}()
			}

			if result.TicketsRejected > 0 && result.DominantRejection == payment.PaymentRejectionReasonInvalidRecipientRand {
				// The payee rotated its recipient rand. For a job the
				// remedy is the payer's existing evict-and-retry loop;
				// the code is what tells the gateway to run it.
				livepeerheader.WriteError(w, http.StatusConflict, livepeerheader.ErrRecipientRotated,
					"payee rejected every ticket: its recipient rand rotated; re-fetch ticket params and retry")
				return
			}

			// Any OTHER batch rejected in full also fails closed.
			//
			// This used to special-case the rotated rand and let every
			// other full rejection through, so a payment whose tickets
			// were all refused — a replayed nonce stream, a bad
			// signature, an exhausted nonce space — bought work anyway.
			// It credited nothing; the exchange was funded out of
			// balance credited earlier, and the caller saw 200 the whole
			// time. Found on the pilot stack, where a payer restart
			// replayed its nonces and three exchanges in a row were
			// served free.
			//
			// A partially-rejected batch is left alone deliberately: it
			// credited something, that balance is the honest one, and
			// the pre-flight runway check decides whether it buys
			// anything.
			if result.TicketsRejected > 0 && len(result.TicketStatus) > 0 &&
				int(result.TicketsRejected) >= len(result.TicketStatus) {
				livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid,
					"payee rejected every ticket in this payment; it credited nothing "+
						"(rejection reason "+strconv.Itoa(int(result.DominantRejection))+")")
				return
			}

			// 3. Set up interim-debit ticker. The dispatch layer publishes
			//    the LiveCounter via the SessionState we attach to the
			//    request context; the ticker polls it on each tick.
			//
			//    When idc.Interval == 0 (locked decision #6) or the
			//    driver does not support interim debit (LiveCounter never
			//    set), the ticker is a no-op and the post-handler path
			//    falls through to the v0.2 single-debit flow.
			state := &SessionState{}
			ctx = context.WithValue(ctx, sessionStateKey{}, state)
			state.SetReceiptMeta(ReceiptMeta{
				WorkID:       workID,
				RoundID:      DerivePaymentRoundID(paymentBytes),
				RequestID:    RequestIDFromContext(ctx),
				CapabilityID: capability,
				OfferingID:   offering,
			})
			state.SetSettlementInputs(SettlementInputs{
				PaymentBytes:   paymentBytes,
				FundedValueWei: result.CreditedEV,
				WorkUnit:       spec.WorkUnit,
			})
			handlerCtx, cancelHandler := context.WithCancel(ctx)
			defer cancelHandler()

			// Pre-flight: do not run a backend for a session that
			// cannot pay for a single unit of work.
			//
			// The interim ticker guards LONG-running work, but it is a
			// no-op for a unary job, so nothing checked before the
			// backend ran. A mainnet probe served real work against a
			// zero balance and reported success at every layer. The
			// ledger permits overdraft by design — that is the right
			// primitive — but the broker is the component that decides
			// whether to deliver, and it should decline what it has not
			// been paid for.
			//
			// One unit is deliberately the floor rather than the job's
			// estimate: payment credit is probabilistic, so a session
			// funded exactly to its estimate would flap. "Can it afford
			// anything at all" is the question with a stable answer.
			if suff, serr := client.SufficientBalance(ctx, payment.SufficientBalanceRequest{
				Sender:       result.Sender,
				WorkID:       workID,
				MinWorkUnits: 1,
			}); serr != nil {
				log.Printf("warning: pre-flight balance check failed work_id=%s: %v", workID, serr)
			} else if suff != nil && !suff.Sufficient {
				// Drain the request body before refusing.
				//
				// The outer idempotency layer records a digest of the
				// body as it was consumed, and compares it on replay. A
				// refusal that returns without reading recorded the
				// digest of an EMPTY read, so every replay of a refused
				// request mismatched and came back 400
				// request_id_reuse — a gateway retrying after a lost
				// response got a confusing reuse error instead of the
				// recorded outcome it is entitled to.
				_, _ = io.Copy(io.Discard, r.Body)
				// The payment was ADMITTED — credited to the ledger —
				// and then the request refused. Value moved and no work
				// was done, so this needs to be a statement, not just an
				// error code.
				//
				// Zero units, said out loud. WriteError sets only the
				// error and the status, so the response carried no units
				// claim at all and a gateway had to infer "nothing was
				// billed" from the absence of a header. A reader that
				// has to infer an amount is a reader that will
				// eventually infer the wrong one.
				w.Header().Set(livepeerheader.WorkUnits, "0")
				w.Header().Set(livepeerheader.WorkUnitName, spec.WorkUnit)
				// And signed evidence of the refusal. Without it the
				// exchange lookup answered ADMITTED_OUTCOME_UNKNOWN —
				// "this broker admitted the exchange and holds no signed
				// settlement for it" — which is the correct thing to say
				// about a record that has none, and a useless thing for
				// a gateway trying to reconcile an admitted envelope.
				// Nothing was billed, and that is a knowable, terminal
				// fact this broker can attest to now.
				refusal := SettlementIdentity{
					JobID:        w.Header().Get(livepeerheader.JobID),
					WorkID:       workID,
					IssuedAt:     time.Now().UTC().Format(time.RFC3339Nano),
					RequestID:    RequestIDFromContext(r.Context()),
					ChargedWei:   big.NewInt(0),
					DebitedUnits: 0,
				}
				if settlement := buildSettlementRecord(paymentBytes, result.CreditedEV, 0,
					spec.WorkUnit, livepeerheader.ErrInsufficientBalance, refusal); settlement != nil {
					if encoded, err := encode(settlement); err == nil {
						w.Header().Set(livepeerheader.Settlement, encoded)
					} else {
						log.Printf("warning: refusal settlement encode failed work_id=%s: %v", workID, err)
					}
				}
				livepeerheader.WriteError(w, http.StatusPaymentRequired,
					livepeerheader.ErrInsufficientBalance,
					"payment credited no runway for this offering; fund the session before requesting work")
				return
			}

			rec := &responseRecorder{ResponseWriter: w}
			// Hold the response until the final debit has resolved.
			// On a unary exchange the handler commits headers before
			// this middleware debits, so Livepeer-Work-Units named a
			// measurement the ledger had not accepted yet — and the
			// gateway team reasonably reads that header as "units you
			// were charged for". Deferring lets the header state what
			// the ledger actually took.
			//
			// This releases itself the moment the handler proves it is
			// streaming, so the streamed path is untouched: it corrects
			// its own claim in the trailer it declares.
			rec.deferResponse()
			defer rec.commit()
			// The Livepeer-Settlement trailer is declared by the STREAMED
			// job path, not here.
			//
			// Trailers ride only on a chunked response. This middleware
			// runs before the handler has decided its transport, and a
			// unary job copies the backend's Content-Length — so
			// declaring it here advertised a trailer on unary responses
			// that net/http then silently dropped, leaving a client that
			// waits for it waiting forever. The streamed path deletes
			// Content-Length before committing headers, so the
			// declaration is honest exactly there.
			//
			// The value is still set below on both transports: harmless
			// when undeclared, and it is what the durable record and the
			// GET /v1/settlement/{id} surface are built from.

			tickerDone := make(chan struct{})
			tickerStop := make(chan struct{})
			var (
				tickerSeq           atomic.Uint64 // last successfully-sent debit_seq; final flush uses N+1
				tickerLastTickTotal atomic.Uint64 // cumulative units already debited
				terminationReason   atomic.Pointer[string]
			)

			if idc.Interval > 0 {
				go runInterimDebitTicker(handlerCtx, tickerStop, tickerDone, interimDebitArgs{
					client:         client,
					sender:         result.Sender,
					workID:         workID,
					interval:       idc.Interval,
					allocSeq:       allocSeq,
					minRunwayUnits: idc.MinRunwayUnits,
					graceOnInsuff:  idc.GraceOnInsufficient,
					state:          state,
					seq:            &tickerSeq,
					lastTickTotal:  &tickerLastTickTotal,
					cancelHandler:  cancelHandler,
					terminated:     &terminationReason,
				})
			} else {
				// Interim debit disabled — close immediately so the
				// final-flush path doesn't wait on a no-op channel.
				close(tickerDone)
				close(tickerStop)
			}

			next.ServeHTTP(rec, r.WithContext(handlerCtx))

			// 4. Stop the ticker (idempotent if already disabled), wait
			//    for it to drain, then perform the final flush. Plan
			//    0015 §3.3: the final DebitBalance is issued by the main
			//    middleware path, not the ticker, so there is no race
			//    between a late tick and the close.
			if idc.Interval > 0 {
				select {
				case <-tickerStop:
					// already closed (e.g. by the ticker on insufficient_balance)
				default:
					close(tickerStop)
				}
				<-tickerDone
			}

			// Read actual work units. Same trailer-fallback logic as v0.1:
			// for http-stream@v0 the value lands in resp.Header() AFTER
			// the body, so we re-read post-handler.
			actual := rec.workUnits
			if actual == 0 {
				if h := rec.Header().Get(livepeerheader.WorkUnits); h != "" {
					if n, err := strconv.ParseUint(h, 10, 64); err == nil {
						actual = n
					}
				}
			}

			// For long-running sessions the LiveCounter is the canonical
			// running view; prefer its end-of-session reading over the
			// (often absent) Livepeer-Work-Units header.
			if lc := state.LiveCounter(); lc != nil {
				if liveTotal := lc.CurrentUnits(); liveTotal > actual {
					actual = liveTotal
				}
			}

			// 5. Final DebitBalance.
			//    - Single-debit path (idc.Interval == 0 or no interim
			//      ticks fired): debit_seq=1, work_units=actual.
			//    - Interim-debit path: debit_seq=N+1,
			//      work_units = actual - lastTickTotal.
			lastTotal := tickerLastTickTotal.Load()
			lastSeq := tickerSeq.Load()
			var finalSeq uint64 = 1
			var finalUnits uint64 = actual
			if lastSeq > 0 {
				finalSeq = lastSeq + 1
				if actual > lastTotal {
					finalUnits = actual - lastTotal
				} else {
					finalUnits = 0
				}
			}
			var chargedWei *big.Int
			var cumulativeUnits uint64
			// Units the LEDGER accepted, as opposed to units measured.
			// They differ exactly when a debit fails, which is the case
			// the settlement has to be able to say out loud.
			//
			// Starts at the full measurement: with no final debit to
			// make (finalUnits == 0) the interim ticks already covered
			// the exchange. A failed final debit subtracts only the part
			// that failed — interim ticks that DID succeed took real
			// value and the record must not disown them.
			debitedUnits := actual
			var debitFailed bool
			if finalUnits > 0 {
				// finalSeq counted from per-request state, which
				// repeats across exchanges on one work_id. Allocate
				// from the work_id's own space instead.
				if seq, serr := allocSeq(workID); serr == nil {
					finalSeq = seq
				} else {
					log.Printf("warning: debit seq allocation failed work_id=%s: %v", workID, serr)
				}
				// NEVER discard this error. It is the call that moves
				// money, and swallowing it is what let a closed shared
				// session serve work for free through two sessions of
				// debugging while every log line read "success".
				debitRes, derr := client.DebitBalance(ctx, payment.DebitBalanceRequest{
					Sender:    result.Sender,
					WorkID:    workID,
					WorkUnits: int64(finalUnits),
					DebitSeq:  finalSeq,
				})
				if debitRes != nil {
					chargedWei = debitRes.DebitedWei
					cumulativeUnits = debitRes.CumulativeUnits
				}
				if derr != nil {
					log.Printf("ERROR: final debit FAILED work_id=%s seq=%d units=%d: %v "+
						"— work was delivered and NOT billed", workID, finalSeq, finalUnits, derr)
					observability.RecordDebitFailure(capability, offering)
					// Fail the ACCOUNTING closed even though the work
					// has already gone out. The settlement used to
					// attest the measured units here, which made a
					// broker whose ledger call failed indistinguishable
					// from one that was paid — the failure invisible
					// exactly when it matters. The record now says
					// DEBIT_FAILED and attests what the ledger took.
					debitFailed = true
					debitedUnits = actual - finalUnits
					// Hand it up for durable retry rather than closing
					// the books here. A debit is idempotent by
					// (sender, work_id, debit_seq), so retrying the same
					// seq cannot double-charge — including the case that
					// motivates retry most, an attempt that landed and
					// lost its response.
					//
					// The settlement is deliberately NOT built now. A
					// record can only state a charge once the charge is
					// known, and that is exactly what is still
					// outstanding; emitting DEBIT_FAILED on the first
					// failed attempt would report a recoverable timeout
					// as a terminal loss.
					debitOutstanding = true
					if slot := PendingDebitSlotFrom(r.Context()); slot != nil {
						slot.Set(&PendingDebit{
							Sender:            result.Sender,
							WorkID:            workID,
							DebitSeq:          finalSeq,
							Units:             finalUnits,
							DebitedUnits:      debitedUnits,
							PaymentBytes:      paymentBytes,
							FundedValueWei:    result.CreditedEV,
							ActualUnits:       actual,
							WorkUnitName:      spec.WorkUnit,
							TerminationReason: loadTerminationReason(&terminationReason),
							JobID:             rec.Header().Get(livepeerheader.JobID),
							RequestID:         RequestIDFromContext(r.Context()),
							IssuedAt:          time.Now().UTC().Format(time.RFC3339Nano),
						})
					}
				}
			}

			// 6. If the ticker terminated the session for insufficient
			//    balance, record the cause for any post-mortem
			//    observability. Trailer-style Livepeer-Error emission is
			//    a future plan-0015 follow-up; the v0.1 cut logs at WARN.
			var terminationReasonValue string
			if reason := terminationReason.Load(); reason != nil {
				terminationReasonValue = *reason
				log.Printf("warning: interim-debit terminated work_id=%s reason=%s", workID, terminationReasonValue)
			}

			if receiptSink != nil && actual > 0 {
				if meta, ok := state.ReceiptMeta(); ok {
					revenue := metaGatewayRevenue(meta, spec, actual)
					if err := receiptSink.UpsertWorkReceipt(ctx, receipts.WorkReceipt{
						ID:                   meta.WorkID,
						RoundID:              meta.RoundID,
						RequestID:            meta.RequestID,
						CapabilityID:         meta.CapabilityID,
						OfferingID:           meta.OfferingID,
						MemberEthAddress:     meta.MemberEthAddress,
						BackendID:            meta.BackendID,
						HostEnrollmentID:     meta.HostEnrollmentID,
						HardwareUnitID:       meta.HardwareUnitID,
						GPUUUID:              meta.GPUUUID,
						TemplateID:           meta.TemplateID,
						ExpectedMaxUnits:     meta.ExpectedMaxUnits,
						ActualUnits:          actual,
						AcceptedWorkUnits:    actual,
						GatewayRevenueWei:    revenue,
						AttributedRevenueWei: revenue,
						Status:               "final",
					}); err != nil {
						observability.RecordWorkReceiptEmit("final", "error")
						log.Printf("warning: work receipt final emit failed work_id=%s: %v", meta.WorkID, err)
					} else {
						observability.RecordWorkReceiptEmit("final", "success")
					}
				}
			}

			// jobIdempotency wraps this middleware and sets the job id
			// header before calling through, so it is available here —
			// and it must be, because a settlement that cannot say which
			// exchange it describes can be replayed against another.
			ident := SettlementIdentity{
				JobID:    rec.Header().Get(livepeerheader.JobID),
				WorkID:   workID,
				IssuedAt: time.Now().UTC().Format(time.RFC3339Nano),
				// What the LEDGER charged, not what this process would
				// recompute. Billing is cumulative, so a second exchange
				// on a shared payment session costs the difference of
				// two ceilings — and a record that recomputed an
				// independent ceiling attested a number the ledger never
				// charged.
				ChargedWei:      chargedWei,
				CumulativeUnits: cumulativeUnits,
				RequestID:       RequestIDFromContext(r.Context()),
				DebitFailed:     debitFailed,
				DebitedUnits:    debitedUnits,
			}
			// No settlement while a debit is still outstanding.
			//
			// The comment at the failed-debit branch above says the
			// record is deliberately NOT built yet, and it is right: a
			// record can only state a charge once the charge is known,
			// and that is precisely what is unresolved. But this encoder
			// ran unconditionally, so the response carried a SIGNED
			// terminal DEBIT_FAILED settlement alongside the
			// accounting_pending header — two contradictory answers to
			// "what did this exchange cost", one of them signed. A
			// consumer that trusted the signature booked a terminal loss
			// for a debit that was about to succeed on retry, and would
			// then have received a second, disagreeing settlement.
			//
			// The retrier owns the terminal record for this exchange and
			// emits exactly one, DEBIT_FAILED only once retries are
			// actually exhausted (see debitretry.go settlePending).
			//
			// This became visible rather than merely wrong when the
			// unary path started holding its response: before that, a
			// settlement set after WriteHeader was dropped by net/http,
			// so the contradiction never reached a client on unary.
			if !debitOutstanding {
				if settlement := buildSettlementRecord(paymentBytes, result.CreditedEV, actual, spec.WorkUnit, terminationReasonValue, ident); settlement != nil {
					if encoded, err := encode(settlement); err == nil {
						rec.Header().Set(livepeerheader.Settlement, encoded)
					} else {
						log.Printf("warning: settlement encode failed work_id=%s: %v", workID, err)
					}
				}
			}

			// The response is still held only on the unary path. Correct
			// the claim to what the ledger took before letting it go.
			//
			// Both branches matter. On success debitedUnits == actual
			// and this changes nothing, which is the point: the header
			// means the same thing on every exchange. On failure it is
			// the difference between telling a caller it was charged for
			// work it was not charged for and telling it the truth.
			if rec.deferred() {
				rec.Header().Set(livepeerheader.WorkUnits, strconv.FormatUint(debitedUnits, 10))
				if debitOutstanding {
					// Not a terminal failure — the debit is queued for
					// retry — so this says "the number may still move"
					// rather than reporting a recoverable timeout as a
					// loss. It matches what GET /v1/settlement/{id}
					// answers for the same exchange.
					rec.Header().Set(livepeerheader.Error, livepeerheader.ErrAccountingPending)
				}
				rec.commit()
			} else if debitedUnits != actual {
				// Streaming corrects itself in its trailer; anything
				// else got too large to hold. Say so rather than let the
				// claim stand unexplained.
				log.Printf("warning: work-units header states measured %d, ledger took %d work_id=%s "+
					"(response could not be deferred)", actual, debitedUnits, workID)
			}
		})
	}
}

func metaGatewayRevenue(meta ReceiptMeta, spec CapabilitySpec, actual uint64) string {
	if spec.PricePerWorkUnitWei == nil || actual == 0 {
		return ""
	}
	return payment.BillFor(spec.PricePerWorkUnitWei, spec.PerUnits, actual).String()
}

// interimDebitArgs is the parameter bundle for the ticker goroutine.
type interimDebitArgs struct {
	client         payment.Client
	sender         []byte
	workID         string
	interval       time.Duration
	minRunwayUnits uint64
	graceOnInsuff  time.Duration
	state          *SessionState
	allocSeq       DebitSeqAllocator
	seq            *atomic.Uint64
	lastTickTotal  *atomic.Uint64
	cancelHandler  context.CancelFunc
	terminated     *atomic.Pointer[string]
}

// runInterimDebitTicker is the per-session goroutine that issues
// DebitBalance + SufficientBalance on each tick. Plan 0015 §3.
//
// Lifecycle:
//   - On every tick (post-LiveCounter publication): compute delta,
//     DebitBalance with the next debit_seq; SufficientBalance check.
//   - If SufficientBalance returns false: optionally wait
//     graceOnInsufficient, cancel the handler context, exit.
//   - On stop or ctx cancellation: exit immediately. The main
//     middleware path performs the final flush; the ticker does not.
func runInterimDebitTicker(ctx context.Context, stop <-chan struct{}, done chan<- struct{}, a interimDebitArgs) {
	defer close(done)
	t := time.NewTicker(a.interval)
	defer t.Stop()
	var pendingSeq uint64
	var pendingMu sync.Mutex
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-t.C:
		}

		// Skip ticks that fire before the dispatch layer publishes a
		// LiveCounter; happens when the driver has no interim view
		// (HTTP family) — the no-op tick keeps fixed cost trivial.
		lc := a.state.LiveCounter()
		if lc == nil {
			continue
		}

		current := lc.CurrentUnits()
		last := a.lastTickTotal.Load()
		delta := uint64(0)
		if current > last {
			delta = current - last
		}

		// Debit the delta if non-zero. Reuse the pending seq on retry;
		// move forward only on a non-error reply (plan 0015 §5.3).
		if delta > 0 {
			pendingMu.Lock()
			if pendingSeq == 0 {
				// Allocate from the work_id's sequence space, not from
				// this request's counter. pendingSeq holds the number
				// until the debit succeeds, so a retry re-presents the
				// same one and the payee deduplicates it — allocating
				// again on retry would double-debit.
				next, aerr := a.allocSeq(a.workID)
				if aerr != nil {
					pendingMu.Unlock()
					log.Printf("warning: debit seq allocation failed work_id=%s: %v", a.workID, aerr)
					continue
				}
				pendingSeq = next
			}
			seq := pendingSeq
			pendingMu.Unlock()
			if _, err := a.client.DebitBalance(ctx, payment.DebitBalanceRequest{
				Sender:    a.sender,
				WorkID:    a.workID,
				WorkUnits: int64(delta),
				DebitSeq:  seq,
			}); err != nil {
				// Retry with same seq next tick. Don't advance.
				log.Printf("warning: interim DebitBalance work_id=%s seq=%d delta=%d failed: %v (will retry)",
					a.workID, seq, delta, err)
				continue
			}
			pendingMu.Lock()
			a.seq.Store(seq)
			a.lastTickTotal.Store(last + delta)
			pendingSeq = 0
			pendingMu.Unlock()
		}

		// SufficientBalance runway check. Plan 0015 §6.3 ships with
		// "every tick" frequency for v0.1.
		if a.minRunwayUnits == 0 {
			continue
		}
		res, err := a.client.SufficientBalance(ctx, payment.SufficientBalanceRequest{
			Sender:       a.sender,
			WorkID:       a.workID,
			MinWorkUnits: int64(a.minRunwayUnits),
		})
		if err != nil {
			log.Printf("warning: SufficientBalance work_id=%s failed: %v (will retry)",
				a.workID, err)
			continue
		}
		if !res.Sufficient {
			if a.graceOnInsuff > 0 {
				select {
				case <-ctx.Done():
					return
				case <-stop:
					return
				case <-time.After(a.graceOnInsuff):
				}
				// Re-check after grace.
				res2, err := a.client.SufficientBalance(ctx, payment.SufficientBalanceRequest{
					Sender:       a.sender,
					WorkID:       a.workID,
					MinWorkUnits: int64(a.minRunwayUnits),
				})
				if err == nil && res2 != nil && res2.Sufficient {
					continue
				}
			}
			reason := livepeerheader.ErrInsufficientBalance
			a.terminated.Store(&reason)
			log.Printf("warning: terminating session work_id=%s reason=%s", a.workID, reason)
			a.cancelHandler()
			return
		}
	}
}

// mapClientErr translates daemon-side rejection into broker-facing
// error codes. v0.2: any RPC error from the daemon is treated as
// `payment_invalid`; defense-in-depth for bad inputs and chain
// failures alike.
func mapClientErr(err error) (int, string) {
	if errors.Is(err, errors.ErrUnsupported) {
		return http.StatusInternalServerError, livepeerheader.ErrInternalError
	}
	return http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid
}

// loadTerminationReason reads the interim-debit ticker's cause, if it
// set one. Read here rather than from the variable computed below
// because the pending debit is captured at the point of failure.
func loadTerminationReason(p *atomic.Pointer[string]) string {
	if r := p.Load(); r != nil {
		return *r
	}
	return ""
}
