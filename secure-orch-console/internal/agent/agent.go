// Package agent is the plan 0042 daemon mode: the outbound-only pull
// → debounce → classify → sign-or-hold → push → confirm loop that
// replaces the hand-carry sign cycle. The agent lives inside
// secure-orch-console because the console already owns every
// primitive the loop needs — the Signer, the canonicalizer, the
// differ, last-signed.json, and the audit log (plan 0042 §4).
//
// Hard constraints (§1): no listener — every connection is initiated
// from here; the cold key never leaves the host (the existing Signer
// is the only signing seam); trust expansion is policy config, not
// code; every decision is audited.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/diff"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/lastsigned"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/policy"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/signing"
)

// Config is the agent's flag-driven configuration.
type Config struct {
	PolicyPath     string
	LastSignedPath string
	HeldDir        string
	// PauseFile is kill switch #1 (§8): its presence pauses pull AND
	// sign, checkable over a plain SSH session with the console down.
	PauseFile string
	// PollInterval is the conditional-GET cadence; ±10% jitter is
	// applied per cycle.
	PollInterval    time.Duration
	PushMaxAttempts int
}

// AlertFunc is the outbound-only alert hook (held item, forbidden
// candidate, publish failure, policy failure, rate-limit pause).
// Best-effort by contract — the audit log is the system of record.
type AlertFunc func(kind string, fields map[string]any)

// Agent owns the loop state. Not safe for concurrent use; Run is the
// only goroutine that touches it.
type Agent struct {
	cfg    Config
	client *Client
	signer signing.Signer
	log    *audit.Log
	logger *slog.Logger
	held   *HeldQueue
	alert  AlertFunc
	now    func() time.Time
	// sleep is ctx-aware; injected so push-backoff tests don't wait.
	sleep func(ctx context.Context, d time.Duration)

	metrics *Metrics

	// rlMu guards rl and rlPauseAudited: the run loop touches them each
	// cycle, and the console clears a latched pause from an HTTP
	// goroutine.
	rlMu           sync.Mutex
	rl             *policy.RateLimiter
	rlMax          int
	policyHash     string
	policyInvalid  bool
	pauseSeen      bool
	rlPauseAudited bool
	expiryWarned   bool

	publishedIssued time.Time
	publishedExpiry time.Time
	// publishedRenewalThreshold is the coordinator's own threshold from
	// the last candidate seen (plan 0043 §3.7), so the expiry warning
	// and the renewal classification agree on when a re-sign is due.
	publishedRenewalThreshold time.Duration

	lastETag     string
	pending      *Candidate
	pendingSince time.Time

	confirmedSeq *uint64
}

// New wires an Agent. signer and log are required; alert may be nil.
func New(cfg Config, client *Client, signer signing.Signer, log *audit.Log, logger *slog.Logger, alert AlertFunc) *Agent {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 60 * time.Second
	}
	if cfg.PushMaxAttempts <= 0 {
		cfg.PushMaxAttempts = 6
	}
	if logger == nil {
		logger = slog.Default()
	}
	if alert == nil {
		alert = func(string, map[string]any) {}
	}
	return &Agent{
		cfg:     cfg,
		client:  client,
		signer:  signer,
		log:     log,
		logger:  logger,
		held:    &HeldQueue{Dir: cfg.HeldDir},
		alert:   alert,
		metrics: NewMetrics(),
		now:     time.Now,
		sleep: func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		},
	}
}

// Run loops until ctx is canceled.
func (a *Agent) Run(ctx context.Context) {
	a.audit(audit.Event{Kind: audit.KindAgentStart, Note: "agent loop starting"})
	a.alert("agent_start", nil)
	defer func() {
		a.audit(audit.Event{Kind: audit.KindAgentStop})
		a.alert("agent_stop", nil)
	}()
	for {
		a.Cycle(ctx)
		jitter := time.Duration(rand.Int63n(int64(a.cfg.PollInterval)/5+1)) - a.cfg.PollInterval/10
		select {
		case <-ctx.Done():
			return
		case <-time.After(a.cfg.PollInterval + jitter):
		}
	}
}

// Cycle runs one pass of the state machine (§6). Exported so tests
// and the operator-approve flow can drive it deterministically.
func (a *Agent) Cycle(ctx context.Context) {
	if a.checkPaused() {
		return
	}
	pol, ok := a.loadPolicy()
	if !ok {
		return
	}
	a.reconcilePush(ctx)
	a.pull(ctx)
	a.maybeHandle(ctx, pol)
	a.updateGauges()
	a.checkExpiryWarning()
}

// updateGauges refreshes the per-cycle gauges (§9).
func (a *Agent) updateGauges() {
	depth := 0
	if item, _, err := a.held.Current(); err == nil && item != nil {
		depth = 1
	}
	a.metrics.SetHeldDepth(depth)
	if !a.publishedExpiry.IsZero() {
		a.metrics.SetPublishedExpiry(a.publishedExpiry)
	}
}

// checkExpiryWarning is the self-contained half of the §9 hard
// requirement: when the published manifest has burned through half
// the renewal buffer without a successful re-publish, fire the
// webhook once per crossing. The Prometheus gauge backs the
// operator's own alerting; this makes the warning reach the webhook
// even without a scrape stack.
func (a *Agent) checkExpiryWarning() {
	if a.publishedExpiry.IsZero() || !a.publishedExpiry.After(a.publishedIssued) {
		return
	}
	ttl := a.publishedExpiry.Sub(a.publishedIssued)
	threshold := a.publishedRenewalThreshold
	if threshold <= 0 {
		threshold = ttl / 3
	}
	warnAt := threshold / 2
	remaining := a.publishedExpiry.Sub(a.now())
	if remaining < warnAt {
		if !a.expiryWarned {
			a.expiryWarned = true
			a.logger.Error("agent: published manifest nearing expiry without re-publish", "remaining", remaining)
			a.alert("manifest_expiry_warning", map[string]any{"remaining_seconds": int64(remaining.Seconds())})
		}
		return
	}
	a.expiryWarned = false
}

// checkPaused implements kill switch #1 and audits the transitions.
func (a *Agent) checkPaused() bool {
	_, err := os.Stat(a.cfg.PauseFile)
	paused := err == nil
	if paused && !a.pauseSeen {
		a.audit(audit.Event{Kind: audit.KindAgentPaused, Note: "pause file present: " + a.cfg.PauseFile})
	}
	if !paused && a.pauseSeen {
		a.audit(audit.Event{Kind: audit.KindAgentResumed})
	}
	a.pauseSeen = paused
	return paused
}

// loadPolicy reloads at each cycle's start (§7). A load failure
// pauses the whole cycle — fail closed, never a stale or default
// policy — and alerts on the transition into the failed state.
func (a *Agent) loadPolicy() (policy.Policy, bool) {
	loaded, err := policy.Load(a.cfg.PolicyPath)
	if err != nil {
		if !a.policyInvalid {
			a.audit(audit.Event{Kind: audit.KindPolicyInvalid, Note: err.Error()})
			a.alert("policy_invalid", map[string]any{"error": err.Error()})
		}
		a.policyInvalid = true
		return policy.Policy{}, false
	}
	a.policyInvalid = false
	if loaded.SHA256 != a.policyHash {
		a.policyHash = loaded.SHA256
		a.audit(audit.Event{Kind: audit.KindPolicyLoaded, Fields: map[string]any{"policy_sha256": loaded.SHA256, "policy_version": loaded.Policy.PolicyVersion}})
	}
	if a.rl == nil || a.rlMax != loaded.Policy.RateLimit.MaxAutoSignsPerHour {
		a.rl = policy.NewRateLimiter(loaded.Policy.RateLimit.MaxAutoSignsPerHour)
		a.rlMax = loaded.Policy.RateLimit.MaxAutoSignsPerHour
	}
	return loaded.Policy, true
}

// reconcilePush is the crash-recovery rule (§8): last-signed is
// written atomically before the first push attempt, so on any path —
// including a crash mid-push — "last-signed ahead of published"
// means resume pushing. The post-sign push rides this same rule.
func (a *Agent) reconcilePush(ctx context.Context) {
	envelope, err := lastsigned.Load(a.cfg.LastSignedPath)
	if err != nil || envelope == nil {
		return
	}
	seq, sha, err := envelopeSeqAndHash(envelope)
	if err != nil {
		a.logger.Warn("agent: last-signed unparseable", "err", err)
		return
	}
	if a.confirmedSeq != nil && *a.confirmedSeq == seq {
		return
	}
	pub, err := a.client.FetchPublished(ctx)
	switch {
	case errors.Is(err, ErrNotPublished):
		// fall through to push
	case err != nil:
		a.logger.Warn("agent: fetch published", "err", err)
		return
	case pub.PublicationSeq == seq && pub.CanonicalSHA256 == sha:
		a.confirmedSeq = &seq
		a.publishedIssued, a.publishedExpiry = pub.IssuedAt, pub.ExpiresAt
		return
	case pub.PublicationSeq > seq:
		// Published is ahead of our last-signed — someone else
		// signed. Never push backwards; surface loudly.
		a.logger.Error("agent: published seq ahead of last-signed", "published", pub.PublicationSeq, "last_signed", seq)
		return
	}
	a.pushWithRetry(ctx, envelope, seq, sha)
}

func (a *Agent) pushWithRetry(ctx context.Context, envelope []byte, seq uint64, sha string) bool {
	for attempt := 0; attempt < a.cfg.PushMaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		a.audit(audit.Event{Kind: audit.KindPushAttempt, Seq: &seq, CanonHash: sha, Fields: map[string]any{"attempt": attempt + 1}})
		err := a.client.PushSigned(ctx, envelope)
		if err == nil {
			pub, cErr := a.client.FetchPublished(ctx)
			if cErr == nil && pub.PublicationSeq == seq && pub.CanonicalSHA256 == sha {
				a.audit(audit.Event{Kind: audit.KindPublishConfirmed, Seq: &seq, CanonHash: sha})
				a.confirmedSeq = &seq
				a.publishedIssued, a.publishedExpiry = pub.IssuedAt, pub.ExpiresAt
				a.metrics.RecordPublishConfirm(a.now())
				return true
			}
			if cErr != nil {
				a.logger.Warn("agent: publish confirm", "err", cErr)
			}
		} else {
			a.logger.Warn("agent: push", "attempt", attempt+1, "err", err)
		}
		if attempt < a.cfg.PushMaxAttempts-1 {
			backoff := retryBackoff(attempt)
			a.sleep(ctx, backoff+time.Duration(rand.Int63n(int64(backoff)/10+1)))
		}
	}
	a.audit(audit.Event{Kind: audit.KindPublishFailed, Seq: &seq, CanonHash: sha, Fields: map[string]any{"attempts": a.cfg.PushMaxAttempts}})
	a.alert("publish_failed", map[string]any{"publication_seq": seq, "canonical_sha256": sha})
	return false
}

// pull does the conditional fetch (§6 steps 1–3). A 304 is silent; a
// 200 records candidate_pulled and (re)starts the stability window.
func (a *Agent) pull(ctx context.Context) {
	cand, err := a.client.FetchCandidate(ctx, a.lastETag)
	if err != nil {
		if errors.Is(err, ErrNoCandidate) {
			a.metrics.IncPoll(PollNoCandidate)
		} else {
			a.metrics.IncPoll(PollError)
			a.logger.Warn("agent: fetch candidate", "err", err)
		}
		return
	}
	if cand == nil {
		a.metrics.IncPoll(PollNotModified)
		return
	}
	a.metrics.IncPoll(PollPulled)
	a.lastETag = cand.ETag
	seq := cand.PublicationSeq
	a.audit(audit.Event{Kind: audit.KindCandidatePulled, Seq: &seq, CanonHash: cand.CanonicalSHA256, Fields: map[string]any{"etag": cand.ETag}})
	a.pending = cand
	a.pendingSince = a.now()
}

// maybeHandle acts once the pending candidate's ETag has been stable
// for the policy's stability window (§6 step 3).
func (a *Agent) maybeHandle(ctx context.Context, pol policy.Policy) {
	if a.pending == nil {
		return
	}
	window := time.Duration(pol.StabilityWindowSeconds) * time.Second
	if a.now().Sub(a.pendingSince) < window {
		return
	}
	cand := a.pending
	a.pending = nil
	a.handle(ctx, cand, pol)
}

func (a *Agent) handle(ctx context.Context, cand *Candidate, pol policy.Policy) {
	envelope, err := lastsigned.Load(a.cfg.LastSignedPath)
	if err != nil {
		a.logger.Warn("agent: load last-signed", "err", err)
		return
	}
	d, err := diff.Compute(envelope, cand.ManifestBytes)
	if err != nil {
		a.logger.Warn("agent: diff", "err", err)
		return
	}

	in := policy.ClassifyInput{Bounds: pol.BenignBounds, FirstSign: envelope == nil}
	var lastSeqPtr *uint64
	if envelope != nil {
		remaining, threshold := a.renewalClock(envelope, cand)
		a.publishedRenewalThreshold = threshold
		in.RemainingValidity = remaining
		in.RenewalThreshold = threshold
		seq, _, err := envelopeSeqAndHash(envelope)
		if err == nil {
			lastSeqPtr = &seq
		}
	}
	cls := policy.Classify(d, in)
	dec := policy.Decide(cls, pol)
	a.metrics.IncDecision(dec.Action.String())

	seq := cand.PublicationSeq
	base := map[string]any{"etag": cand.ETag, "class": cls.Class.String(), "action": dec.Action.String(), "findings": len(cls.Findings)}
	a.audit(audit.Event{Kind: audit.KindClassified, Seq: &seq, CanonHash: cand.CanonicalSHA256, Fields: base})

	switch dec.Action {
	case policy.ActionSkip:
		a.audit(audit.Event{Kind: audit.KindNoOp, Seq: &seq, CanonHash: cand.CanonicalSHA256, Fields: map[string]any{"etag": cand.ETag}})
	case policy.ActionRefuse:
		a.audit(audit.Event{Kind: audit.KindRefused, Seq: &seq, CanonHash: cand.CanonicalSHA256, Fields: map[string]any{"etag": cand.ETag, "findings": findingsField(cls.Findings)}})
		a.alert("forbidden_candidate", map[string]any{"etag": cand.ETag, "findings": findingsField(cls.Findings)})
	case policy.ActionAutoSign:
		// The lock covers only the limiter decision. autoSign records
		// the signature through the same mutex, so holding it across
		// the call would deadlock.
		a.rlMu.Lock()
		allowed := a.rl.Allow(a.now())
		if allowed {
			a.rlPauseAudited = false
		}
		shouldAudit := !allowed && a.rl.Paused() && !a.rlPauseAudited
		if shouldAudit {
			a.rlPauseAudited = true
		}
		a.rlMu.Unlock()

		if allowed {
			a.autoSign(ctx, cand, lastSeqPtr, cls)
			return
		}
		if shouldAudit {
			a.audit(audit.Event{Kind: audit.KindRateLimitPause, Fields: map[string]any{"max_per_hour": a.rlMax}})
			a.alert("rate_limit_pause", map[string]any{"max_per_hour": a.rlMax})
		}
		// Paused auto-signing degrades to hold: the candidate still
		// reaches the operator, nothing signs.
		a.hold(cand, cls, dec)
	case policy.ActionHold:
		a.hold(cand, cls, dec)
	}
}

// renewalClock derives (remaining validity, renewal threshold) from
// the last-signed manifest. The TTL comes from the manifest's own
// issued_at→expires_at span, so the agent needs no TTL flag of its
// own. Unparseable timestamps degrade to renewal-due — for an
// unchanged candidate the worst case is one early re-sign.
// renewalClock reports how much validity the published manifest has
// left, and the threshold below which an unchanged candidate is a
// renewal rather than a no-op.
//
// The threshold comes from the CANDIDATE (plan 0043 §3.7): the
// coordinator applies it when deciding whether to refresh an unchanged
// candidate's window, and publishes the effective value in
// metadata.json. It used to be a console-side fraction that an operator
// had to keep equal to the coordinator's flag by hand — and when the
// two drifted, renewals arrived before the console considered them due
// and sat in the queue until they expired.
//
// A candidate that carries no threshold is from a coordinator older
// than that field; ttl/3 is the coordinator's own default, so the
// fallback keeps the two aligned rather than inventing a third rule.
func (a *Agent) renewalClock(envelope []byte, cand *Candidate) (remaining, threshold time.Duration) {
	issuedStr, expiresStr := expirySplit(envelope)
	issued, err1 := time.Parse(time.RFC3339Nano, issuedStr)
	expires, err2 := time.Parse(time.RFC3339Nano, expiresStr)
	if err1 != nil || err2 != nil || !expires.After(issued) {
		return 0, time.Nanosecond
	}
	ttl := expires.Sub(issued)
	return expires.Sub(a.now()), renewalThresholdFor(cand, ttl)
}

// renewalThresholdFor reads the coordinator's published threshold,
// falling back to its documented default.
func renewalThresholdFor(cand *Candidate, ttl time.Duration) time.Duration {
	if cand != nil {
		if secs := candidateRenewalThresholdSeconds(cand.MetadataBytes); secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return ttl / 3
}

// candidateRenewalThresholdSeconds pulls renewal_threshold_seconds out
// of a candidate's metadata.json. Metadata is operator-facing
// provenance, never signed, so a malformed one is not fatal: it just
// means the default applies.
func candidateRenewalThresholdSeconds(metadataBytes []byte) int64 {
	if len(metadataBytes) == 0 {
		return 0
	}
	var meta struct {
		RenewalThresholdSeconds int64 `json:"renewal_threshold_seconds"`
	}
	if err := json.Unmarshal(metadataBytes, &meta); err != nil {
		return 0
	}
	return meta.RenewalThresholdSeconds
}

func (a *Agent) autoSign(ctx context.Context, cand *Candidate, lastSeq *uint64, cls policy.Classification) {
	envelope, seq, err := signCandidate(cand.ManifestBytes, lastSeq, a.signer)
	if err != nil {
		a.logger.Error("agent: auto-sign", "err", err)
		return
	}
	// Atomic write BEFORE the first push attempt (§8): a crash
	// mid-push never loses a signature; reconcilePush resumes it.
	if err := lastsigned.WriteAtomic(a.cfg.LastSignedPath, envelope); err != nil {
		a.logger.Error("agent: write last-signed", "err", err)
		return
	}
	a.rlMu.Lock()
	a.rl.RecordSign(a.now())
	a.rlMu.Unlock()
	_, sha, err := envelopeSeqAndHash(envelope)
	if err != nil {
		a.logger.Error("agent: hash signed envelope", "err", err)
		return
	}
	a.audit(audit.Event{
		Kind:       audit.KindAutoSign,
		EthAddress: a.signer.Address().String(),
		Seq:        &seq,
		CanonHash:  sha,
		Fields:     map[string]any{"etag": cand.ETag, "class": cls.Class.String(), "policy_sha256": a.policyHash},
	})
	a.confirmedSeq = nil
	a.pushWithRetry(ctx, envelope, seq, sha)
}

func (a *Agent) hold(cand *Candidate, cls policy.Classification, dec policy.Decision) {
	seq := cand.PublicationSeq
	item := HeldItem{
		ETag:            cand.ETag,
		HeldAt:          a.now().UTC(),
		PublicationSeq:  seq,
		CanonicalSHA256: cand.CanonicalSHA256,
		Class:           cls.Class.String(),
		ShadowAutoSign:  dec.ShadowAutoSign,
		Findings:        cls.Findings,
	}
	prev, err := a.held.Put(item, cand.ManifestBytes)
	if err != nil {
		a.logger.Error("agent: hold", "err", err)
		return
	}
	if prev != nil && prev.ETag != item.ETag {
		prevSeq := prev.PublicationSeq
		a.audit(audit.Event{Kind: audit.KindHeldSuperseded, Seq: &prevSeq, CanonHash: prev.CanonicalSHA256, Fields: map[string]any{"etag": prev.ETag, "superseded_by": item.ETag}})
	}
	a.audit(audit.Event{Kind: audit.KindHeld, Seq: &seq, CanonHash: cand.CanonicalSHA256, Fields: map[string]any{"etag": cand.ETag, "class": item.Class, "findings": findingsField(cls.Findings)}})
	if dec.ShadowAutoSign {
		// Burn-in calibration evidence (§10): the policy would have
		// signed this; the operator's manual verdict grades the dial.
		a.audit(audit.Event{Kind: audit.KindWouldAutoSign, Seq: &seq, CanonHash: cand.CanonicalSHA256, Fields: map[string]any{"etag": cand.ETag, "policy_sha256": a.policyHash}})
	}
	a.alert("held", map[string]any{"etag": cand.ETag, "class": item.Class, "publication_seq": seq})
}

// Metrics exposes the agent's Prometheus surface for the console's
// /metrics route.
func (a *Agent) Metrics() *Metrics { return a.metrics }

// Held exposes the held queue (the console UI's "Pending changes"
// view reads it; the approve flow clears it).
func (a *Agent) Held() *HeldQueue { return a.held }

func (a *Agent) audit(ev audit.Event) {
	if err := a.log.Append(ev); err != nil {
		a.logger.Warn("agent: audit append", "kind", string(ev.Kind), "err", err)
	}
}

func findingsField(fs []policy.Finding) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]any{
			"class": f.ClassName, "code": f.Code,
			"capability_id": f.CapabilityID, "offering_id": f.OfferingID,
			"detail": f.Detail,
		})
	}
	return out
}

// --- operator controls -----------------------------------------------------

// RateLimitPaused reports whether the auto-sign rate limiter has
// latched. The limiter latches rather than throttling: exceeding the
// bound means something upstream is misbehaving, and quietly signing
// slower would hide it.
func (a *Agent) RateLimitPaused() bool {
	a.rlMu.Lock()
	defer a.rlMu.Unlock()
	return a.rl.Paused()
}

// ClearRateLimit releases a latched pause after an operator has looked
// at why it latched (plan 0043 §3.7).
//
// Until now the only way out was restarting the console, which also
// discarded everything else the agent knew. Clearing is a deliberate,
// audited gesture: it names the actor, and it forgets the window so the
// next breach latches on fresh evidence rather than on signatures the
// operator has already accounted for.
func (a *Agent) ClearRateLimit(actor string) bool {
	a.rlMu.Lock()
	wasPaused := a.rl.Paused()
	if wasPaused {
		a.rl.Clear()
		a.rlPauseAudited = false
	}
	a.rlMu.Unlock()
	if wasPaused {
		a.audit(audit.Event{
			Kind:  audit.KindAgentResumed,
			Actor: actor,
			Note:  "auto-sign rate limit cleared by operator",
		})
	}
	return wasPaused
}
