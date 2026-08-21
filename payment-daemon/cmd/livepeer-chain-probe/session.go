package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// probeSession runs a paid-session against a real chain: open, claim
// usage, top up, end. This path had never touched a real payee before —
// every prior run used the mock, which is exactly what hid the job-path
// defects.
func probeSession(ctx context.Context, cfg config, payer pb.PayerDaemonClient, payee pb.PayeeDaemonClient) error {
	runner, err := startFakeRunner(cfg.runnerBind)
	if err != nil {
		return fmt.Errorf("start fake runner: %w", err)
	}
	defer runner.close()
	fmt.Printf("  fake runner at %s (point the offering's backend here)\n", runner.url)

	m, err := mint(ctx, cfg, payer, "session-open")
	if err != nil {
		return fmt.Errorf("mint for open: %w", err)
	}
	sender := senderOf(m)

	open, err := postJSON(cfg.brokerURL+"/v1/session", map[string]string{
		"Livepeer-Capability": cfg.capability,
		"Livepeer-Offering":   cfg.offering,
		"Livepeer-Protocol":   "paid-session/v1",
		"Livepeer-Request-Id": fmt.Sprintf("chain-probe-open-%d", time.Now().UnixNano()),
		"Livepeer-Payment":    base64.StdEncoding.EncodeToString(m.GetPaymentBytes()),
	}, `{"gateway_session_id":"chain-probe","session_params":{}}`)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	if open.status != http.StatusCreated && open.status != http.StatusOK {
		return fmt.Errorf("open status %d error=%q body=%s", open.status, open.errCode, open.body)
	}
	sessionID, _ := open.field("session_id").(string)
	credential, _ := open.field("credential").(string)
	workID, _ := open.field("work_id").(string)
	if sessionID == "" || credential == "" || workID == "" {
		return fmt.Errorf("open response missing identity: %s", open.body)
	}
	fmt.Printf("  opened session=%s work_id=%s\n", sessionID, workID)

	// The work_id must be the payee's identity, not one the broker
	// invented — the defect that made every session unbillable in chain
	// mode.
	if workID != m.GetWorkId() {
		return fmt.Errorf("session work_id %q is not the payment's %q: the broker minted its own identity",
			workID, m.GetWorkId())
	}

	cb, ok := runner.lastCreate()
	if !ok {
		return fmt.Errorf("broker never called the runner")
	}

	before, err := balanceOf(ctx, payee, sender, workID)
	if err != nil {
		return fmt.Errorf("balance before: %w", err)
	}

	// Claim usage as the runner would.
	const claimed = 42
	st, body, err := runner.postEvent(cb, fmt.Sprintf(
		`{"event_id":"ev-1","sequence":1,"event_type":"session.usage.tick","usage":{"unit":%q,"total":%d}}`,
		cfg.workUnit, claimed))
	if err != nil {
		return fmt.Errorf("usage event: %w", err)
	}
	if st < 200 || st > 299 {
		return fmt.Errorf("usage event status %d: %s", st, body)
	}

	after, err := balanceOf(ctx, payee, sender, workID)
	if err != nil {
		return fmt.Errorf("balance after usage: %w", err)
	}
	debited := new(big.Int).Sub(before, after)
	want := billFor(cfg.priceWei, cfg.perUnits, claimed)
	if debited.Sign() == 0 {
		return fmt.Errorf("ledger debited NOTHING for %d claimed units — the session bills free", claimed)
	}
	if debited.Cmp(want) != 0 {
		return fmt.Errorf("ledger debited %s wei for %d units; the rule says %s", debited, claimed, want)
	}
	fmt.Printf("  usage %d units debited %s wei\n", claimed, debited)

	// Top up: funding and lifetime move together, and the request id
	// makes the retry safe.
	tu, err := mint(ctx, cfg, payer, "session-topup")
	if err != nil {
		return fmt.Errorf("mint for top-up: %w", err)
	}
	topupID := fmt.Sprintf("chain-probe-topup-%d", time.Now().UnixNano())
	tr, err := postJSON(cfg.brokerURL+"/v1/session/"+sessionID+"/topup", map[string]string{
		"Authorization":       "Bearer " + credential,
		"Livepeer-Request-Id": topupID,
		"Livepeer-Payment":    base64.StdEncoding.EncodeToString(tu.GetPaymentBytes()),
	}, "")
	if err != nil {
		return fmt.Errorf("top-up: %w", err)
	}
	if tr.status != http.StatusOK {
		return fmt.Errorf("top-up status %d error=%q body=%s", tr.status, tr.errCode, tr.body)
	}
	fmt.Printf("  topped up\n")

	// The same request id must replay, not fund twice.
	replay, err := postJSON(cfg.brokerURL+"/v1/session/"+sessionID+"/topup", map[string]string{
		"Authorization":       "Bearer " + credential,
		"Livepeer-Request-Id": topupID,
		"Livepeer-Payment":    base64.StdEncoding.EncodeToString(tu.GetPaymentBytes()),
	}, "")
	if err != nil {
		return fmt.Errorf("top-up replay: %w", err)
	}
	if replay.status != tr.status {
		return fmt.Errorf("top-up replay status %d != original %d", replay.status, tr.status)
	}
	if a, b := tr.field("lease"), replay.field("lease"); fmt.Sprint(a) != fmt.Sprint(b) {
		return fmt.Errorf("top-up replay returned a different lease: %v vs %v — the retry funded again", a, b)
	}
	fmt.Printf("  top-up replay returned the recorded outcome\n")

	// End, and check the settlement.
	end, err := postJSON(cfg.brokerURL+"/v1/session/"+sessionID+"/end", map[string]string{
		"Authorization": "Bearer " + credential,
	}, `{"reason":"gateway_close"}`)
	if err != nil {
		return fmt.Errorf("end: %w", err)
	}
	if end.status != http.StatusOK {
		return fmt.Errorf("end status %d body=%s", end.status, end.body)
	}
	if runner.terminated() == 0 {
		return fmt.Errorf("session ended but the runner was never terminated")
	}

	set, err := fetchSettlement(cfg.brokerURL, sessionID)
	if err != nil {
		return err
	}
	if set.payload.SessionID != sessionID {
		return fmt.Errorf("settlement session_id = %q; want %q", set.payload.SessionID, sessionID)
	}
	if set.payload.DebitedUnits != fmt.Sprint(claimed) {
		return fmt.Errorf("settlement debited_units = %q; want %d", set.payload.DebitedUnits, claimed)
	}
	if set.signature == nil || set.signature.Value == "" {
		return fmt.Errorf("session settlement is UNSIGNED")
	}
	if got := new(big.Int).SetBytes(set.payload.BilledValueWei.value()); got.Cmp(want) != 0 {
		return fmt.Errorf("settlement billed %s wei; ledger and rule say %s", got, want)
	}
	fmt.Printf("  settled state=%s debited=%s billed=%s wei signed\n",
		set.payload.State, set.payload.DebitedUnits,
		new(big.Int).SetBytes(set.payload.BilledValueWei.value()))
	return nil
}

// ---------------------------------------------------------------------------

type httpResult struct {
	status  int
	errCode string
	body    string
	decoded map[string]any
}

func (r *httpResult) field(name string) any {
	if r.decoded == nil {
		_ = json.Unmarshal([]byte(r.body), &r.decoded)
	}
	return r.decoded[name]
}

func postJSON(url string, headers map[string]string, body string) (*httpResult, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return &httpResult{
		status:  resp.StatusCode,
		errCode: resp.Header.Get("Livepeer-Error"),
		body:    readAll(resp.Body),
	}, nil
}
