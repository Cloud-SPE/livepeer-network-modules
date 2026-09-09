package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

const wholesaleRecoveryCheckpointVersion = 1

type wholesaleRecoveryCheckpoint struct {
	Version               int              `json:"version"`
	Phase                 string           `json:"phase"`
	CreatedAt             string           `json:"created_at"`
	VerifiedAt            string           `json:"verified_at,omitempty"`
	Payer                 string           `json:"payer"`
	AuthorizationID       string           `json:"authorization_id"`
	MintRequestID         string           `json:"mint_request_id"`
	TargetAvailableWei    string           `json:"target_available_wei"`
	ObservedAvailableWei  string           `json:"observed_available_wei"`
	MaxDebitWei           string           `json:"max_debit_wei"`
	PaymentSHA256         string           `json:"payment_sha256"`
	ExpectedValueWei      string           `json:"expected_value_wei"`
	TicketsCreated        uint32           `json:"tickets_created"`
	AccountAfterAdmission wholesaleAccount `json:"account_after_admission"`
	SettlementJobID       string           `json:"settlement_job_id"`
	SettlementRequestID   string           `json:"settlement_request_id"`
	SettlementState       string           `json:"settlement_state"`
	SettlementBilledWei   string           `json:"settlement_billed_value_wei"`
}

func prepareWholesaleRecovery(ctx context.Context, cfg config, payer pb.PayerDaemonClient, payee pb.PayeeDaemonClient) error {
	if strings.TrimSpace(cfg.checkpointFile) == "" {
		return fmt.Errorf("--checkpoint-file is required")
	}
	if _, err := os.Stat(cfg.checkpointFile); err == nil {
		return fmt.Errorf("checkpoint already exists: %s", cfg.checkpointFile)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect checkpoint: %w", err)
	}
	if err := waitForOffering(cfg, 90*time.Second); err != nil {
		return err
	}
	if cfg.maxAuthUnits == 0 || cfg.chainID == 0 {
		return fmt.Errorf("chain-id and max-authorization-units must be positive")
	}
	body := []byte(`{"model":"probe","messages":[]}`)
	maxDebit := billFor(cfg.priceWei, cfg.perUnits, cfg.maxAuthUnits)
	target := new(big.Int).Set(maxDebit)
	if cfg.accountFloat != nil {
		target.Set(cfg.accountFloat)
	}
	if target.Cmp(maxDebit) < 0 {
		return fmt.Errorf("account float %s cannot admit authorization maximum %s", target, maxDebit)
	}

	stamp := time.Now().UnixNano()
	completedAuthID := fmt.Sprintf("chain-probe-recovery-completed-%d", stamp)
	completedRequestID := "request-" + completedAuthID
	heldAuthID := fmt.Sprintf("chain-probe-recovery-held-%d", stamp)
	heldRequestID := "request-" + heldAuthID
	mintID := fmt.Sprintf("chain-probe-recovery-held-fund-%d", stamp)
	completedAuth, err := createJobAuthorization(ctx, cfg, payer, completedAuthID, completedRequestID, body, maxDebit, nil)
	if err != nil {
		return fmt.Errorf("completed authorization: %w", err)
	}
	payerAddress := completedAuth.GetPayer()
	accountBefore, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return fmt.Errorf("account before completed exchange: %w", err)
	}
	if err := accountBefore.validate(); err != nil {
		return fmt.Errorf("account before completed exchange: %w", err)
	}
	cp := wholesaleRecoveryCheckpoint{
		Version: wholesaleRecoveryCheckpointVersion, Phase: "preparing", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Payer: "0x" + hex.EncodeToString(payerAddress), AuthorizationID: heldAuthID, MintRequestID: mintID,
		TargetAvailableWei: target.String(), MaxDebitWei: maxDebit.String(), SettlementRequestID: completedRequestID,
	}
	// Persist every future id before the first ticket can be signed. A failed
	// prepare therefore leaves an explicit, non-rerunnable recovery marker
	// instead of silently inviting an operator to mint the same intent again.
	if err := writeRecoveryCheckpoint(cfg.checkpointFile, &cp, true); err != nil {
		return err
	}
	completedMint, err := mintAccountShortfallWithID(ctx, cfg, payer,
		fmt.Sprintf("chain-probe-recovery-completed-fund-%d", stamp), target, accountBefore.Available.big())
	if err != nil {
		return fmt.Errorf("completed exchange funding: %w", err)
	}
	if err := assertShortfall(completedMint, positiveDifference(target, accountBefore.Available.big())); err != nil {
		return fmt.Errorf("completed exchange funding: %w", err)
	}
	completed, err := invokeAuthorizedJob(cfg, completedRequestID, body, completedAuth.GetAuthorizationBytes(), nil, completedMint.GetPaymentBytes())
	if err != nil {
		return fmt.Errorf("completed exchange: %w", err)
	}
	if completed.status != http.StatusOK {
		return fmt.Errorf("completed exchange status=%d error=%q body=%s", completed.status, completed.errCode, completed.body)
	}
	jobID := completed.headers.Get("Livepeer-Job-Id")
	if jobID == "" {
		return fmt.Errorf("completed exchange returned no Livepeer-Job-Id")
	}
	settlement, err := fetchSettlement(cfg.brokerURL, jobID)
	if err != nil {
		return fmt.Errorf("completed settlement: %w", err)
	}
	if settlement.payload.RequestID != completedRequestID || settlement.payload.State == "" {
		return fmt.Errorf("completed settlement identity/state mismatch")
	}

	beforeAdmission, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return fmt.Errorf("account before held admission: %w", err)
	}
	if err := beforeAdmission.validate(); err != nil {
		return fmt.Errorf("account before held admission: %w", err)
	}
	heldAuth, err := createJobAuthorization(ctx, cfg, payer, heldAuthID, heldRequestID, body, maxDebit, nil)
	if err != nil {
		return fmt.Errorf("held authorization: %w", err)
	}
	cp.ObservedAvailableWei = beforeAdmission.Available.big().String()
	if err := writeRecoveryCheckpoint(cfg.checkpointFile, &cp, false); err != nil {
		return err
	}
	heldMint, err := mintAccountShortfallWithID(ctx, cfg, payer, mintID, target, beforeAdmission.Available.big())
	if err != nil {
		return fmt.Errorf("held funding: %w", err)
	}
	shortfall := positiveDifference(target, beforeAdmission.Available.big())
	if err := assertShortfall(heldMint, shortfall); err != nil {
		return fmt.Errorf("held funding: %w", err)
	}
	if shortfall.Sign() == 0 {
		return fmt.Errorf("recovery needs a positive shortfall to prove durable mint replay; account already has %s wei available", beforeAdmission.Available.big())
	}
	paymentDigest := sha256.Sum256(heldMint.GetPaymentBytes())
	cp.Phase = "funded"
	cp.PaymentSHA256 = hex.EncodeToString(paymentDigest[:])
	cp.ExpectedValueWei = new(big.Int).SetBytes(heldMint.GetExpectedValue().GetValue()).String()
	cp.TicketsCreated = heldMint.GetTicketsCreated()
	if err := writeRecoveryCheckpoint(cfg.checkpointFile, &cp, false); err != nil {
		return err
	}
	admitted, err := payee.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{
		AuthorizationBytes: heldAuth.GetAuthorizationBytes(), PaymentBytes: heldMint.GetPaymentBytes(),
	})
	if err != nil {
		return fmt.Errorf("held admission: %w", err)
	}
	if admitted.GetState() != pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED {
		return fmt.Errorf("held admission state=%s", admitted.GetState())
	}
	afterAdmission, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return fmt.Errorf("account after held admission: %w", err)
	}
	if err := afterAdmission.validate(); err != nil {
		return fmt.Errorf("account after held admission: %w", err)
	}
	if afterAdmission.Reserved.big().Cmp(maxDebit) < 0 {
		return fmt.Errorf("held admission reserved=%s; want at least %s", afterAdmission.Reserved.big(), maxDebit)
	}

	cp.Phase = "admitted"
	cp.AccountAfterAdmission = *afterAdmission
	cp.SettlementJobID = jobID
	cp.SettlementState = settlement.payload.State
	cp.SettlementBilledWei = new(big.Int).SetBytes(settlement.payload.BilledValueWei.value()).String()
	if err := writeRecoveryCheckpoint(cfg.checkpointFile, &cp, false); err != nil {
		return err
	}
	fmt.Printf("  checkpoint=%s payer=%s held_authorization=%s mint=%s payment_sha256=%s\n",
		cfg.checkpointFile, cp.Payer, cp.AuthorizationID, cp.MintRequestID, cp.PaymentSHA256)
	return nil
}

func verifyWholesaleRecovery(ctx context.Context, cfg config, payer pb.PayerDaemonClient, payee pb.PayeeDaemonClient) error {
	cp, err := readRecoveryCheckpoint(cfg.checkpointFile)
	if err != nil {
		return err
	}
	if cp.Version != wholesaleRecoveryCheckpointVersion || cp.Phase != "admitted" {
		return fmt.Errorf("checkpoint version/phase=%d/%q; want %d/admitted", cp.Version, cp.Phase, wholesaleRecoveryCheckpointVersion)
	}
	if err := cp.AccountAfterAdmission.validate(); err != nil {
		return fmt.Errorf("checkpoint account: %w", err)
	}
	payerAddress, err := hexTo20(cp.Payer)
	if err != nil {
		return fmt.Errorf("checkpoint payer: %w", err)
	}
	target, ok := new(big.Int).SetString(cp.TargetAvailableWei, 10)
	if !ok || target.Sign() <= 0 {
		return fmt.Errorf("checkpoint target is invalid")
	}
	observed, ok := new(big.Int).SetString(cp.ObservedAvailableWei, 10)
	if !ok || observed.Sign() < 0 {
		return fmt.Errorf("checkpoint observed balance is invalid")
	}
	maxDebit, ok := new(big.Int).SetString(cp.MaxDebitWei, 10)
	if !ok || maxDebit.Sign() <= 0 {
		return fmt.Errorf("checkpoint max debit is invalid")
	}

	replayedMint, err := mintAccountShortfallWithID(ctx, cfg, payer, cp.MintRequestID, target, observed)
	if err != nil {
		return fmt.Errorf("durable mint replay: %w", err)
	}
	digest := sha256.Sum256(replayedMint.GetPaymentBytes())
	if hex.EncodeToString(digest[:]) != cp.PaymentSHA256 || replayedMint.GetTicketsCreated() != cp.TicketsCreated ||
		new(big.Int).SetBytes(replayedMint.GetExpectedValue().GetValue()).String() != cp.ExpectedValueWei {
		return fmt.Errorf("durable mint replay differs from checkpoint")
	}
	if got := senderOf(replayedMint); hex.EncodeToString(got) != strings.TrimPrefix(cp.Payer, "0x") {
		return fmt.Errorf("durable mint replay payer differs from checkpoint")
	}

	accountAfterRestart, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return fmt.Errorf("account after restart: %w", err)
	}
	if err := accountAfterRestart.validate(); err != nil {
		return fmt.Errorf("account after restart: %w", err)
	}
	if !sameWholesaleAccountTotals(accountAfterRestart, &cp.AccountAfterAdmission) {
		return fmt.Errorf("account changed across restart: checkpoint=%+v restarted=%+v", cp.AccountAfterAdmission, accountAfterRestart)
	}
	auth, err := payee.GetSpendAuthorization(ctx, &pb.GetSpendAuthorizationRequest{Payer: payerAddress, AuthorizationId: cp.AuthorizationID})
	if err != nil {
		return fmt.Errorf("held authorization after restart: %w", err)
	}
	if auth.GetState() != pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED || new(big.Int).SetBytes(auth.GetReservedValueWei().GetValue()).Cmp(maxDebit) != 0 {
		return fmt.Errorf("held authorization after restart state=%s reserved=%s", auth.GetState(), new(big.Int).SetBytes(auth.GetReservedValueWei().GetValue()))
	}
	settlement, err := fetchSettlement(cfg.brokerURL, cp.SettlementJobID)
	if err != nil {
		return fmt.Errorf("broker settlement after restart: %w", err)
	}
	if settlement.payload.RequestID != cp.SettlementRequestID || settlement.payload.State != cp.SettlementState ||
		new(big.Int).SetBytes(settlement.payload.BilledValueWei.value()).String() != cp.SettlementBilledWei {
		return fmt.Errorf("broker settlement changed across restart")
	}

	settled, err := payee.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{
		Payer: payerAddress, AuthorizationId: cp.AuthorizationID, ActualUnits: 0, SettlementSeq: 1,
	})
	if err != nil {
		return fmt.Errorf("settle held authorization: %w", err)
	}
	if settled.GetReplayed() {
		return fmt.Errorf("first recovery settlement unexpectedly replayed")
	}
	afterSettlement, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return err
	}
	if err := afterSettlement.validate(); err != nil {
		return fmt.Errorf("account after settlement: %w", err)
	}
	settlementReplay, err := payee.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{
		Payer: payerAddress, AuthorizationId: cp.AuthorizationID, ActualUnits: 0, SettlementSeq: 1,
	})
	if err != nil {
		return fmt.Errorf("settlement replay: %w", err)
	}
	if !settlementReplay.GetReplayed() {
		return fmt.Errorf("second recovery settlement was not identified as replay")
	}
	afterReplay, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return err
	}
	if err := afterReplay.validate(); err != nil {
		return fmt.Errorf("account after settlement replay: %w", err)
	}
	if !sameWholesaleAccountTotals(afterSettlement, afterReplay) {
		return fmt.Errorf("settlement replay mutated account: first=%+v replay=%+v", afterSettlement, afterReplay)
	}
	wantReserved := new(big.Int).Sub(accountAfterRestart.Reserved.big(), maxDebit)
	if afterReplay.Reserved.big().Cmp(wantReserved) != 0 || afterReplay.Debited != accountAfterRestart.Debited ||
		new(big.Int).Sub(afterReplay.Available.big(), accountAfterRestart.Available.big()).Cmp(maxDebit) != 0 {
		return fmt.Errorf("released account totals are wrong after recovery: before=%+v after=%+v", accountAfterRestart, afterReplay)
	}
	cp.Phase = "verified"
	cp.VerifiedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeRecoveryCheckpoint(cfg.checkpointFile, cp, false); err != nil {
		return err
	}
	fmt.Printf("  restart preserved mint, account, authorization, and settlement; released=%s wei; replay changed nothing\n", maxDebit)
	return nil
}

func sameWholesaleAccountTotals(a, b *wholesaleAccount) bool {
	return a != nil && b != nil && a.Payer == b.Payer && a.Payee == b.Payee && a.ChainID == b.ChainID &&
		a.Denomination == b.Denomination && a.Credited == b.Credited && a.Reserved == b.Reserved &&
		a.Debited == b.Debited && a.Available == b.Available && a.Version == b.Version
}

func readRecoveryCheckpoint(path string) (*wholesaleRecoveryCheckpoint, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("--checkpoint-file is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read checkpoint: %w", err)
	}
	var cp wholesaleRecoveryCheckpoint
	if err := json.Unmarshal(raw, &cp); err != nil {
		return nil, fmt.Errorf("decode checkpoint: %w", err)
	}
	return &cp, nil
}

func writeRecoveryCheckpoint(path string, cp *wholesaleRecoveryCheckpoint, exclusive bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}
	raw, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if exclusive {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("create checkpoint: %w", err)
		}
		if _, err = f.Write(raw); err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return fmt.Errorf("write checkpoint: %w", err)
		}
		if closeErr != nil {
			return fmt.Errorf("close checkpoint: %w", closeErr)
		}
		return syncCheckpointDir(filepath.Dir(path))
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close checkpoint: %w", closeErr)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace checkpoint: %w", err)
	}
	return syncCheckpointDir(filepath.Dir(path))
}

func syncCheckpointDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open checkpoint directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync checkpoint directory: %w", err)
	}
	return nil
}
