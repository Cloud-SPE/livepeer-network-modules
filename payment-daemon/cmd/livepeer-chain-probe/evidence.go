package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// probeEvidence exercises the reconciliation surfaces a clearinghouse
// depends on, against a real signing broker.
//
// These decide how much a customer is charged when a settlement goes
// missing, so the failure they guard against is silent: a consumer that
// cannot find an outcome charges conservatively, and a broker that
// reports the wrong one charges wrongly.
func probeEvidence(ctx context.Context, cfg config, payer pb.PayerDaemonClient) error {
	runner, err := startFakeRunner(cfg.runnerBind)
	if err != nil {
		return fmt.Errorf("start fake runner: %w", err)
	}
	defer runner.close()
	if err := waitForOffering(cfg, 90*time.Second); err != nil {
		return err
	}

	m, err := mint(ctx, cfg, payer, "evidence")
	if err != nil {
		return fmt.Errorf("mint: %w", err)
	}
	requestID := fmt.Sprintf("chain-probe-evidence-%d", time.Now().UnixNano())

	req, _ := http.NewRequest(http.MethodPost, cfg.brokerURL+"/v1/job",
		bytes.NewReader([]byte(`{"model":"probe","messages":[]}`)))
	req.Header.Set("Livepeer-Capability", cfg.capability)
	req.Header.Set("Livepeer-Offering", cfg.offering)
	req.Header.Set("Livepeer-Protocol", "paid-job/v1")
	req.Header.Set("Livepeer-Request-Id", requestID)
	req.Header.Set("Livepeer-Payment", base64.StdEncoding.EncodeToString(m.GetPaymentBytes()))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("job request: %w", err)
	}
	_ = readAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("exchange status %d", resp.StatusCode)
	}

	// 1. Found by the id the CONSUMER issued, carrying real evidence.
	out, err := getExchange(cfg.brokerURL, requestID)
	if err != nil {
		return err
	}
	if out.Outcome != "SETTLED" {
		return fmt.Errorf("lookup outcome = %q; want SETTLED", out.Outcome)
	}
	if out.Settlement == "" {
		return fmt.Errorf("SETTLED with no signed settlement — that reports an exchange as " +
			"costed when nothing supports the figure")
	}
	if out.JobID == "" {
		return fmt.Errorf("no broker job id returned")
	}
	fmt.Printf("  settled exchange found by request_id, settlement present\n")

	// 2. An id nobody has heard of is silence, not a claim.
	unknown := fmt.Sprintf("chain-probe-unknown-%d", time.Now().UnixNano())
	out, err = getExchange(cfg.brokerURL, unknown)
	if err != nil {
		return err
	}
	if out.Outcome != "NO_RECORD" {
		return fmt.Errorf("unknown id outcome = %q; want NO_RECORD — a broker that has not "+
			"been asked has made no claim", out.Outcome)
	}
	fmt.Printf("  unknown request_id reports NO_RECORD, not a claim\n")

	// 3. A signed non-admission for an id that never arrived.
	naEnv, err := askNonAdmission(cfg, unknown)
	if err != nil {
		return err
	}
	if naEnv == "" {
		return fmt.Errorf("non-admission returned no signed envelope")
	}
	fmt.Printf("  non-admission signed for an unseen request_id\n")

	// 4. And the SAME record on the second ask. Re-signing one fact
	//    under a later observed_at gives a consumer two statements it
	//    cannot tell apart from a conflict.
	again, err := askNonAdmission(cfg, unknown)
	if err != nil {
		return err
	}
	if again != naEnv {
		return fmt.Errorf("a second non-admission query re-signed the same fact")
	}

	// 5. It now surfaces through the ordinary lookup too, so a consumer
	//    does not have to know which endpoint to try.
	out, err = getExchange(cfg.brokerURL, unknown)
	if err != nil {
		return err
	}
	if out.Outcome != "NOT_ADMITTED" {
		return fmt.Errorf("after swearing non-admission the lookup says %q; want NOT_ADMITTED",
			out.Outcome)
	}
	fmt.Printf("  non-admission surfaces through the exchange lookup\n")

	// 6. Asking for non-admission on an ADMITTED request must hand back
	//    the outcome, not a bare refusal — the caller is about to decide
	//    what to charge.
	body := nonAdmissionBody(requestID)
	res, err := postJSON(cfg.brokerURL+"/v1/non-admission/"+requestID,
		map[string]string{"Content-Type": "application/json"}, body)
	if err != nil {
		return err
	}
	var admitted exchangeOutcome
	_ = json.Unmarshal([]byte(res.body), &admitted)
	if admitted.Outcome != "SETTLED" || admitted.Settlement == "" {
		return fmt.Errorf("non-admission on an admitted request gave outcome %q settlement=%v; "+
			"want SETTLED with the evidence, so the caller does not charge conservatively "+
			"against a settlement this broker holds", admitted.Outcome, admitted.Settlement != "")
	}
	fmt.Printf("  non-admission on an admitted request returns its settlement\n")
	return nil
}

type exchangeOutcome struct {
	Outcome    string `json:"outcome"`
	JobID      string `json:"job_id"`
	Settlement string `json:"settlement"`
}

func getExchange(brokerURL, requestID string) (*exchangeOutcome, error) {
	resp, err := http.Get(brokerURL + "/v1/exchange/" + requestID)
	if err != nil {
		return nil, fmt.Errorf("exchange lookup: %w", err)
	}
	defer resp.Body.Close()
	var out exchangeOutcome
	if err := json.Unmarshal([]byte(readAll(resp.Body)), &out); err != nil {
		return nil, fmt.Errorf("decode exchange lookup: %w", err)
	}
	return &out, nil
}

func nonAdmissionBody(requestID string) string {
	return fmt.Sprintf(`{"protocol":"paid-job/v1","work_id":"probe-%s",`+
		`"sender":"%s","recipient":"%s","quote_id":"probe-quote","quote_version":1,`+
		`"constraint_fingerprint":"aabb","route_fingerprint":"ccdd","job_issued_at":"%s"}`,
		requestID, strings.Repeat("0a", 20), hex.EncodeToString(make([]byte, 20)),
		time.Now().UTC().Format(time.RFC3339Nano))
}

func askNonAdmission(cfg config, requestID string) (string, error) {
	res, err := postJSON(cfg.brokerURL+"/v1/non-admission/"+requestID,
		map[string]string{"Content-Type": "application/json"}, nonAdmissionBody(requestID))
	if err != nil {
		return "", err
	}
	if res.status != http.StatusOK {
		return "", fmt.Errorf("non-admission status %d: %s", res.status, res.body)
	}
	var body struct {
		NonAdmission string `json:"non_admission"`
	}
	if err := json.Unmarshal([]byte(res.body), &body); err != nil {
		return "", fmt.Errorf("decode non-admission: %w", err)
	}
	return body.NonAdmission, nil
}
