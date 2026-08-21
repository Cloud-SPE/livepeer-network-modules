package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// probeJob runs one paid-job exchange and checks the three things a mock
// cannot: that the ledger actually moved, that it moved by the amount
// the normative rule says, and that the signed settlement agrees with
// the ledger.
func probeJob(ctx context.Context, cfg config, payer pb.PayerDaemonClient, payee pb.PayeeDaemonClient) error {
	m, err := mint(ctx, cfg, payer, "job")
	if err != nil {
		return fmt.Errorf("mint: %w", err)
	}
	workID := m.GetWorkId()
	fmt.Printf("  minted work_id=%s ev=%s wei\n", workID,
		new(big.Int).SetBytes(m.GetExpectedValue().GetValue()))

	// A payment that credits nothing buys nothing. This is the shape of
	// the first mainnet defect, where a valid ticket credited zero and
	// work was served anyway.
	if ev := new(big.Int).SetBytes(m.GetExpectedValue().GetValue()); ev.Sign() == 0 {
		return fmt.Errorf("minted payment carries zero expected value: it would fund no work")
	}

	before, err := balanceOf(ctx, payee, senderOf(m), workID)
	if err != nil {
		return fmt.Errorf("balance before: %w", err)
	}

	req, _ := http.NewRequest(http.MethodPost, cfg.brokerURL+"/v1/job",
		bytes.NewReader([]byte(`{"model":"probe","messages":[]}`)))
	req.Header.Set("Livepeer-Capability", cfg.capability)
	req.Header.Set("Livepeer-Offering", cfg.offering)
	req.Header.Set("Livepeer-Protocol", "paid-job/v1")
	req.Header.Set("Livepeer-Request-Id", fmt.Sprintf("chain-probe-job-%d", time.Now().UnixNano()))
	req.Header.Set("Livepeer-Payment", base64.StdEncoding.EncodeToString(m.GetPaymentBytes()))
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("job request: %w", err)
	}
	defer resp.Body.Close()
	body := readAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d error=%q body=%s",
			resp.StatusCode, resp.Header.Get("Livepeer-Error"), body)
	}
	units, err := strconv.ParseUint(resp.Header.Get("Livepeer-Work-Units"), 10, 64)
	if err != nil || units == 0 {
		return fmt.Errorf("work units = %q on a successful exchange",
			resp.Header.Get("Livepeer-Work-Units"))
	}
	jobID := resp.Header.Get("Livepeer-Job-Id")
	fmt.Printf("  served units=%d job_id=%s\n", units, jobID)

	// 1. The ledger moved, and by the normative amount.
	//
	// A job request CREDITS the payment and DEBITS the work in one call,
	// so a before/after difference conflates the two. Check the whole
	// equation instead — it pins the credit and the debit together, and
	// a zero-credit payment or a zero-value debit both fail it.
	after, err := balanceOf(ctx, payee, senderOf(m), workID)
	if err != nil {
		return fmt.Errorf("balance after: %w", err)
	}
	ev := new(big.Int).SetBytes(m.GetExpectedValue().GetValue())
	want := billFor(cfg.priceWei, cfg.perUnits, units)
	wantAfter := new(big.Int).Add(before, ev)
	wantAfter.Sub(wantAfter, want)
	if after.Cmp(wantAfter) != 0 {
		moved := new(big.Int).Sub(after, before)
		return fmt.Errorf("balance moved by %s wei; want credit %s minus bill %s = %s "+
			"(before=%s after=%s, %d units at %d per %d)",
			moved, ev, want, new(big.Int).Sub(ev, want), before, after,
			units, cfg.priceWei, cfg.perUnits)
	}
	fmt.Printf("  ledger credited %s, billed %s wei (rule: ceil(%d x %d / %d))\n",
		ev, want, units, cfg.priceWei, cfg.perUnits)

	// 2. The settlement is reachable without reading a trailer, and it
	//    agrees with the ledger.
	set, err := fetchSettlement(cfg.brokerURL, jobID)
	if err != nil {
		return err
	}
	if got := new(big.Int).SetBytes(set.payload.BilledValueWei.value()); got.Cmp(want) != 0 {
		return fmt.Errorf("signed settlement says %s wei; ledger and rule say %s", got, want)
	}
	if set.payload.JobID != jobID {
		return fmt.Errorf("settlement job_id = %q; want %q — evidence that cannot name its exchange can be replayed against another",
			set.payload.JobID, jobID)
	}
	if set.payload.IssuedAt == "" {
		return fmt.Errorf("settlement carries no issued_at")
	}
	if _, err := time.Parse(time.RFC3339, set.payload.IssuedAt); err != nil {
		return fmt.Errorf("settlement issued_at %q is not RFC3339: %w", set.payload.IssuedAt, err)
	}
	if set.signature == nil || set.signature.Value == "" {
		return fmt.Errorf("settlement is UNSIGNED — a clearinghouse must refuse it")
	}
	fmt.Printf("  settlement billed=%s wei signed=%s issued_at=%s\n",
		new(big.Int).SetBytes(set.payload.BilledValueWei.value()),
		set.signature.Algorithm, set.payload.IssuedAt)
	return nil
}

func senderOf(m *pb.CreatePaymentResponse) []byte {
	var pay pb.Payment
	if err := proto.Unmarshal(m.GetPaymentBytes(), &pay); err != nil {
		return nil
	}
	return pay.GetSender()
}

// ---------------------------------------------------------------------------
// settlement envelope

type settlementEnvelope struct {
	payload   settlementPayload
	signature *settlementSignature
}

type settlementSignature struct {
	Algorithm        string `json:"algorithm"`
	Canonicalization string `json:"canonicalization"`
	Value            string `json:"value"`
}

// settlementPayload decodes only what the probe asserts on. Fields the
// probe does not check are deliberately absent rather than mirrored, so
// this does not quietly become a second schema to maintain.
type settlementPayload struct {
	JobID          string   `json:"job_id"`
	WorkID         string   `json:"work_id"`
	SessionID      string   `json:"session_id"`
	IssuedAt       string   `json:"issued_at"`
	State          string   `json:"state"`
	DebitedUnits   string   `json:"debited_units"`
	BilledValueWei bigValue `json:"billed_value_wei"`
}

// bigValue mirrors the proto BigUInt as protojson renders it: an object
// with a base64 `value`.
type bigValue struct {
	Value string `json:"value"`
}

func (b bigValue) value() []byte {
	raw, err := base64.StdEncoding.DecodeString(b.Value)
	if err != nil {
		return nil
	}
	return raw
}

func fetchSettlement(brokerURL, id string) (*settlementEnvelope, error) {
	resp, err := http.Get(brokerURL + "/v1/settlement/" + id)
	if err != nil {
		return nil, fmt.Errorf("settlement query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("settlement query status %d: %s", resp.StatusCode, readAll(resp.Body))
	}
	encoded := resp.Header.Get("Livepeer-Settlement")
	if encoded == "" {
		return nil, fmt.Errorf("settlement query returned no Livepeer-Settlement")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("settlement is not base64: %w", err)
	}
	var env struct {
		Payload   json.RawMessage      `json:"payload"`
		Signature *settlementSignature `json:"signature"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("settlement envelope: %w", err)
	}
	var payload settlementPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return nil, fmt.Errorf("settlement payload: %w", err)
	}
	return &settlementEnvelope{payload: payload, signature: env.Signature}, nil
}
