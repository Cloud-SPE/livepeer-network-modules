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

// probeDebitRetry exercises the accounting_pending lifecycle against a
// REAL ledger: work is delivered, the debit does not land, and the
// exchange has to reach a signed terminal settlement anyway.
//
// The failure is injected from outside — the operator stops the payee
// while the backend is still working, and starts it again afterwards.
// The probe cannot do that to a daemon it did not start, so it prints
// what it needs and waits.
//
// This is the path with the most to hide. It changes when a payee
// session closes, adds a background loop that moves money, and every
// part of it was verified only against a mock, which is the position
// that produced eight mainnet defects the last time.
func probeDebitRetry(ctx context.Context, cfg config, payer pb.PayerDaemonClient) error {
	fmt.Printf("  this run needs the payee STOPPED while the backend works,\n")
	fmt.Printf("  then STARTED again — the operator script does that.\n\n")

	m, err := mint(ctx, cfg, payer, "retry")
	if err != nil {
		return fmt.Errorf("mint: %w", err)
	}
	fmt.Printf("  minted work_id=%s\n", m.GetWorkId())

	requestID := fmt.Sprintf("chain-probe-retry-%d", time.Now().UnixNano())
	req, _ := http.NewRequest(http.MethodPost, cfg.brokerURL+"/v1/job",
		bytes.NewReader([]byte(`{"model":"probe","messages":[]}`)))
	req.Header.Set("Livepeer-Capability", cfg.capability)
	req.Header.Set("Livepeer-Offering", cfg.offering)
	req.Header.Set("Livepeer-Protocol", "paid-job/v1")
	req.Header.Set("Livepeer-Request-Id", requestID)
	req.Header.Set("Livepeer-Payment", base64.StdEncoding.EncodeToString(m.GetPaymentBytes()))
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("job request: %w", err)
	}
	body := readAll(resp.Body)
	_ = resp.Body.Close()
	jobID := resp.Header.Get("Livepeer-Job-Id")
	fmt.Printf("  exchange returned status=%d job_id=%s\n", resp.StatusCode, jobID)
	if jobID == "" {
		return fmt.Errorf("no job id; body=%s", body)
	}

	// The exchange was delivered. Its debit was not, so the settlement
	// query must say so rather than reporting a settled exchange or a
	// terminal loss.
	st, pending, err := settlementState(cfg.brokerURL, jobID)
	if err != nil {
		return err
	}
	if st != http.StatusAccepted || pending != "accounting_pending" {
		return fmt.Errorf("after a failed debit the query gave status %d state %q; want 202 "+
			"accounting_pending — a delivered exchange whose debit is outstanding is neither "+
			"settled nor a terminal loss", st, pending)
	}
	fmt.Printf("  query says accounting_pending (delivered, debit outstanding)\n")
	fmt.Printf("  waiting for the retrier to land the debit against the real ledger...\n")

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Second)
		st, pending, err = settlementState(cfg.brokerURL, jobID)
		if err != nil {
			return err
		}
		if st == http.StatusOK {
			break
		}
		fmt.Printf("    still %s\n", pending)
	}
	if st != http.StatusOK {
		return fmt.Errorf("the exchange never reached a terminal settlement (last state %q); "+
			"a job that cannot terminate is an encumbrance nobody can release", pending)
	}

	set, err := fetchSettlement(cfg.brokerURL, jobID)
	if err != nil {
		return err
	}
	if set.signature == nil || set.signature.Value == "" {
		return fmt.Errorf("terminal settlement after retry is UNSIGNED")
	}
	if set.payload.State == "DEBIT_FAILED" {
		return fmt.Errorf("settlement says DEBIT_FAILED; the retry was supposed to land")
	}
	if set.payload.DebitedUnits == "0" || set.payload.DebitedUnits == "" {
		return fmt.Errorf("settlement attests debited_units=%q after a successful retry",
			set.payload.DebitedUnits)
	}
	fmt.Printf("  settled after retry: debited=%s billed=%s wei signed\n",
		set.payload.DebitedUnits, new(big.Int).SetBytes(set.payload.BilledValueWei.value()))
	return nil
}

// settlementState returns the status code and the state field.
func settlementState(brokerURL, jobID string) (int, string, error) {
	resp, err := http.Get(brokerURL + "/v1/settlement/" + jobID)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw := readAll(resp.Body)
	var body struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal([]byte(raw), &body)
	return resp.StatusCode, body.State, nil
}
