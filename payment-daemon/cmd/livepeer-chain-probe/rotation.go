package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// probeRotation drives a real recipient rotation under a LIVE session.
//
// This is the path with no other way to test it. A mock cannot rotate a
// rand it never had, and conformance cannot either — the suite treats
// payment envelopes as opaque strings by design. What follows is the
// actual sequence a gateway hits when its payee rotates:
//
//	open → payee rotates → next payment is rejected → payer evicts its
//	cached session → re-mint gets a new identity → top-up declaring the
//	predecessor rebinds the live session onto it
//
// and then asserts the properties that make a rotation safe: same
// session, same credential, one generation forward, cumulative units
// unbroken, and the whole chain in the signed settlement.
func probeRotation(ctx context.Context, cfg config, payer pb.PayerDaemonClient,
	payee pb.PayeeDaemonClient, admin pb.PayeeAdminClient) error {

	runner, err := startFakeRunner(cfg.runnerBind)
	if err != nil {
		return fmt.Errorf("start fake runner: %w", err)
	}
	defer runner.close()
	if err := waitForOffering(cfg, 90*time.Second); err != nil {
		return err
	}

	// --- open ---------------------------------------------------------
	m, err := mint(ctx, cfg, payer, "rot-open")
	if err != nil {
		return fmt.Errorf("mint for open: %w", err)
	}
	sender := senderOf(m)
	open, err := postJSON(cfg.brokerURL+"/v1/session", map[string]string{
		"Livepeer-Capability": cfg.capability,
		"Livepeer-Offering":   cfg.offering,
		"Livepeer-Protocol":   "paid-session/v1",
		"Livepeer-Request-Id": fmt.Sprintf("rot-open-%d", time.Now().UnixNano()),
		"Livepeer-Payment":    base64.StdEncoding.EncodeToString(m.GetPaymentBytes()),
	}, `{"gateway_session_id":"rotation-probe","session_params":{}}`)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	if open.status != http.StatusCreated && open.status != http.StatusOK {
		return fmt.Errorf("open status %d: %s", open.status, open.body)
	}
	sessionID, _ := open.field("session_id").(string)
	credential, _ := open.field("credential").(string)
	originalWorkID, _ := open.field("work_id").(string)
	fmt.Printf("  opened session=%s work_id=%s\n", sessionID, originalWorkID)

	cb, ok := runner.lastCreate()
	if !ok {
		return fmt.Errorf("broker never called the runner")
	}
	// Deliver work BEFORE rotating, so the rebind has real accounting to
	// carry across — a rotation over an idle session proves much less.
	const beforeUnits = 42
	if st, body, err := runner.postEvent(cb, fmt.Sprintf(
		`{"event_id":"ev-pre","sequence":1,"event_type":"session.usage.tick","usage":{"unit":%q,"total":%d}}`,
		cfg.workUnit, beforeUnits)); err != nil || st < 200 || st > 299 {
		return fmt.Errorf("pre-rotation usage: status %d %s %v", st, body, err)
	}

	// --- the payee rotates under the live session ---------------------
	reset, err := admin.ResetSession(ctx, &pb.ResetSessionRequest{
		Sender:     sender,
		Recipient:  cfg.recipient,
		Capability: cfg.capability,
		Offering:   cfg.offering,
	})
	if err != nil {
		return fmt.Errorf("ResetSession: %w", err)
	}
	if !reset.GetReset_() {
		return fmt.Errorf("payee reported no rotation (old_work_id=%q)", reset.GetOldWorkId())
	}
	fmt.Printf("  payee rotated its rand away from %s\n", reset.GetOldWorkId())

	// --- the gateway discovers it the way a gateway does ---------------
	stale, err := mint(ctx, cfg, payer, "rot-stale")
	if err != nil {
		return fmt.Errorf("mint against the stale identity: %w", err)
	}
	if stale.GetWorkId() != originalWorkID {
		return fmt.Errorf("payer did not reuse its cached identity (%s vs %s) — "+
			"the probe cannot exercise the rejection path",
			stale.GetWorkId(), originalWorkID)
	}
	staleTopUp, err := postJSON(cfg.brokerURL+"/v1/session/"+sessionID+"/topup", map[string]string{
		"Authorization":       "Bearer " + credential,
		"Livepeer-Request-Id": fmt.Sprintf("rot-stale-%d", time.Now().UnixNano()),
		"Livepeer-Payment":    base64.StdEncoding.EncodeToString(stale.GetPaymentBytes()),
	}, "")
	if err != nil {
		return err
	}
	if staleTopUp.errCode != "recipient_rotated" {
		return fmt.Errorf("stale top-up gave status %d error %q; want recipient_rotated — "+
			"a gateway acts on this code, so anything else strands it",
			staleTopUp.status, staleTopUp.errCode)
	}
	fmt.Printf("  stale payment refused with %s\n", staleTopUp.errCode)

	// The payer must be told, or it keeps minting against the dead rand.
	if _, err := payer.ReportPaymentResult(ctx, &pb.ReportPaymentResultRequest{
		WorkId:          originalWorkID,
		Capability:      cfg.capability,
		Offering:        cfg.offering,
		RejectionReason: pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND,
	}); err == nil {
		return fmt.Errorf("ReportPaymentResult returned success; it MUST report Aborted so a " +
			"caller knows to retry exactly once")
	}
	fmt.Printf("  payer evicted the stale session\n")

	// --- rebind -------------------------------------------------------
	// A rotation retry is a NEW mint intent, so a new idempotency key.
	fresh, err := mint(ctx, cfg, payer, "rot-fresh")
	if err != nil {
		return fmt.Errorf("mint against the new identity: %w", err)
	}
	if fresh.GetWorkId() == originalWorkID {
		return fmt.Errorf("re-mint produced the same identity %s; the eviction did not take",
			fresh.GetWorkId())
	}
	rebind, err := postJSON(cfg.brokerURL+"/v1/session/"+sessionID+"/topup", map[string]string{
		"Authorization":        "Bearer " + credential,
		"Livepeer-Request-Id":  fmt.Sprintf("rot-rebind-%d", time.Now().UnixNano()),
		"Livepeer-Rebind-From": originalWorkID,
		"Livepeer-Payment":     base64.StdEncoding.EncodeToString(fresh.GetPaymentBytes()),
	}, "")
	if err != nil {
		return err
	}
	if rebind.status != http.StatusOK {
		return fmt.Errorf("rebind status %d error %q: %s", rebind.status, rebind.errCode, rebind.body)
	}
	if got, _ := rebind.field("work_id").(string); got != fresh.GetWorkId() {
		return fmt.Errorf("rebind returned work_id %q; want the successor %q", got, fresh.GetWorkId())
	}
	fmt.Printf("  rebound onto %s\n", fresh.GetWorkId())

	// --- the session must still work, and still be the same session ----
	const afterUnits = 70
	if st, body, err := runner.postEvent(cb, fmt.Sprintf(
		`{"event_id":"ev-post","sequence":2,"event_type":"session.usage.tick","usage":{"unit":%q,"total":%d}}`,
		cfg.workUnit, afterUnits)); err != nil || st < 200 || st > 299 {
		return fmt.Errorf("post-rotation usage: status %d %s %v — the session did not survive",
			st, body, err)
	}
	newBalance, err := balanceOf(ctx, payee, sender, fresh.GetWorkId())
	if err != nil {
		return fmt.Errorf("balance on the successor: %w", err)
	}
	if newBalance.Sign() <= 0 {
		return fmt.Errorf("successor identity holds no balance (%s) — the rebind funded nothing", newBalance)
	}

	status, err := postJSON(cfg.brokerURL+"/v1/session/"+sessionID+"/end", map[string]string{
		"Authorization": "Bearer " + credential,
	}, `{"reason":"gateway_close"}`)
	if err != nil {
		return err
	}
	if status.status != http.StatusOK {
		return fmt.Errorf("end status %d: %s", status.status, status.body)
	}

	set, err := fetchSettlement(cfg.brokerURL, sessionID)
	if err != nil {
		return err
	}
	if set.payload.RotationGeneration != 1 {
		return fmt.Errorf("settlement rotation_generation = %d; want 1", set.payload.RotationGeneration)
	}
	if set.payload.PredecessorWorkID != originalWorkID {
		return fmt.Errorf("settlement predecessor = %q; want %q",
			set.payload.PredecessorWorkID, originalWorkID)
	}
	if set.payload.WorkID != fresh.GetWorkId() {
		return fmt.Errorf("settlement work_id = %q; want the successor %q",
			set.payload.WorkID, fresh.GetWorkId())
	}
	// Cumulative accounting spans the rotation: the runner claimed 70
	// total, not 70 on top of a reset counter.
	if got := set.payload.cumulative(); got != afterUnits {
		return fmt.Errorf("settlement debited_units = %d; want %d — accounting reset across the rotation",
			got, afterUnits)
	}
	if set.payload.GatewaySessionID != "rotation-probe" {
		return fmt.Errorf("settlement gateway_session_id = %q; a record must name the session "+
			"its consumer issued", set.payload.GatewaySessionID)
	}
	if set.signature == nil || set.signature.Value == "" {
		return fmt.Errorf("rotation settlement is UNSIGNED")
	}
	fmt.Printf("  settled generation=%d predecessor=%s units=%d billed=%s wei signed\n",
		set.payload.RotationGeneration, short(set.payload.PredecessorWorkID),
		set.payload.cumulative(), new(big.Int).SetBytes(set.payload.BilledValueWei.value()))
	return nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}
