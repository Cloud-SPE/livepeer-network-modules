package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

// probeWholesale proves the economic property the legacy probe cannot: one
// bounded payer-payee float backs unrelated workload authorizations, and a
// later large-ceiling request mints only the small shortfall created by actual
// prior usage. The first call models an in-path provider. The second binds an
// ephemeral caller key and models a clearinghouse delegating invocation.
func probeWholesale(ctx context.Context, cfg config, payer pb.PayerDaemonClient) error {
	if cfg.maxAuthUnits == 0 || cfg.chainID == 0 {
		return fmt.Errorf("chain-id and max-authorization-units must be positive")
	}
	if err := waitForOffering(cfg, 90*time.Second); err != nil {
		return err
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

	firstID := fmt.Sprintf("chain-probe-wholesale-provider-%d", time.Now().UnixNano())
	firstRequestID := "request-" + firstID
	firstAuth, err := createJobAuthorization(ctx, cfg, payer, firstID, firstRequestID, body, maxDebit, nil)
	if err != nil {
		return fmt.Errorf("provider authorization: %w", err)
	}
	payerAddress := firstAuth.GetPayer()
	before, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return fmt.Errorf("account before: %w", err)
	}
	if err := before.validate(); err != nil {
		return fmt.Errorf("account before: %w", err)
	}

	firstMint, err := mintAccountShortfall(ctx, cfg, payer, "provider", target, before.Available.big())
	if err != nil {
		return fmt.Errorf("provider shortfall: %w", err)
	}
	wantFirst := positiveDifference(target, before.Available.big())
	if err := assertShortfall(firstMint, wantFirst); err != nil {
		return fmt.Errorf("provider funding: %w", err)
	}
	first, err := invokeAuthorizedJob(cfg, firstRequestID, body, firstAuth.GetAuthorizationBytes(), nil, firstMint.GetPaymentBytes())
	if err != nil {
		return fmt.Errorf("provider invocation: %w", err)
	}
	if first.status != http.StatusOK {
		return fmt.Errorf("provider status %d error=%q body=%s", first.status, first.errCode, first.body)
	}
	firstUnits, err := positiveUnits(first.headers.Get("Livepeer-Work-Units"))
	if err != nil {
		return fmt.Errorf("provider invocation: %w", err)
	}
	fmt.Printf("  provider authorized max=%d units, served=%d, minted shortfall=%s wei\n",
		cfg.maxAuthUnits, firstUnits, wantFirst)

	afterFirst, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return fmt.Errorf("account after provider: %w", err)
	}
	if err := afterFirst.validate(); err != nil {
		return fmt.Errorf("account after provider: %w", err)
	}
	firstCharge := new(big.Int).Sub(afterFirst.Debited.big(), before.Debited.big())
	if want := billFor(cfg.priceWei, cfg.perUnits, firstUnits); firstCharge.Cmp(want) != 0 {
		return fmt.Errorf("provider debit=%s; normative bill=%s", firstCharge, want)
	}
	if afterFirst.Reserved.big().Sign() != 0 {
		return fmt.Errorf("provider left %s wei reserved after settlement", afterFirst.Reserved.big())
	}

	// A distinct caller key proves that possession of the authorization alone
	// is insufficient in the delegated clearinghouse shape.
	callerKey, err := crypto.GenerateKey()
	if err != nil {
		return fmt.Errorf("generate delegated caller key: %w", err)
	}
	callerPublic := crypto.FromECDSAPub(&callerKey.PublicKey)
	secondID := fmt.Sprintf("chain-probe-wholesale-delegated-%d", time.Now().UnixNano())
	secondRequestID := "request-" + secondID
	secondAuth, err := createJobAuthorization(ctx, cfg, payer, secondID, secondRequestID, body, maxDebit, callerPublic)
	if err != nil {
		return fmt.Errorf("delegated authorization: %w", err)
	}
	proof, err := invocationProof(secondAuth.GetAuthorizationBytes(), callerKey)
	if err != nil {
		return fmt.Errorf("delegated caller proof: %w", err)
	}

	secondMint, err := mintAccountShortfall(ctx, cfg, payer, "delegated", target, afterFirst.Available.big())
	if err != nil {
		return fmt.Errorf("delegated shortfall: %w", err)
	}
	wantSecond := positiveDifference(target, afterFirst.Available.big())
	if err := assertShortfall(secondMint, wantSecond); err != nil {
		return fmt.Errorf("delegated funding: %w", err)
	}
	if wantSecond.Cmp(maxDebit) >= 0 {
		return fmt.Errorf("second request minted %s wei for a %s wei ceiling; residual credit was not reused", wantSecond, maxDebit)
	}
	second, err := invokeAuthorizedJob(cfg, secondRequestID, body, secondAuth.GetAuthorizationBytes(), proof, secondMint.GetPaymentBytes())
	if err != nil {
		return fmt.Errorf("delegated invocation: %w", err)
	}
	if second.status != http.StatusOK {
		return fmt.Errorf("delegated status %d error=%q body=%s", second.status, second.errCode, second.body)
	}
	secondUnits, err := positiveUnits(second.headers.Get("Livepeer-Work-Units"))
	if err != nil {
		return fmt.Errorf("delegated invocation: %w", err)
	}
	fmt.Printf("  delegated authorized max=%d units, served=%d, minted only %s wei\n",
		cfg.maxAuthUnits, secondUnits, wantSecond)

	// A transport retry carries the same workload-bound authorization and
	// payment. It must replay the broker result before either is processed
	// again, so the account version and totals remain unchanged.
	beforeReplay, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return fmt.Errorf("account before replay: %w", err)
	}
	replay, err := invokeAuthorizedJob(cfg, secondRequestID, body, secondAuth.GetAuthorizationBytes(), proof, secondMint.GetPaymentBytes())
	if err != nil {
		return fmt.Errorf("delegated replay: %w", err)
	}
	if replay.status != second.status || replay.body != second.body {
		return fmt.Errorf("delegated replay differs: status %d/%d body %q/%q", second.status, replay.status, second.body, replay.body)
	}
	afterReplay, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return fmt.Errorf("account after replay: %w", err)
	}
	if beforeReplay.Version != afterReplay.Version || beforeReplay.Credited != afterReplay.Credited || beforeReplay.Debited != afterReplay.Debited || beforeReplay.Available != afterReplay.Available {
		return fmt.Errorf("replay mutated account: before=%+v after=%+v", beforeReplay, afterReplay)
	}
	if err := afterReplay.validate(); err != nil {
		return fmt.Errorf("account after replay: %w", err)
	}

	issued := new(big.Int).Add(wantFirst, wantSecond)
	creditedDelta := new(big.Int).Sub(afterReplay.Credited.big(), before.Credited.big())
	if creditedDelta.Cmp(issued) != 0 {
		return fmt.Errorf("issued EV=%s but account credited delta=%s", issued, creditedDelta)
	}
	debitDelta := new(big.Int).Sub(afterReplay.Debited.big(), before.Debited.big())
	availableDelta := new(big.Int).Sub(afterReplay.Available.big(), before.Available.big())
	if new(big.Int).Sub(creditedDelta, debitDelta).Cmp(availableDelta) != 0 {
		return fmt.Errorf("reconciliation failed: credited delta %s - debited delta %s != available delta %s", creditedDelta, debitDelta, availableDelta)
	}
	fmt.Printf("  replay did not mint or debit; credited=%s debited=%s available=%s reserved=%s wei\n",
		afterReplay.Credited.big(), afterReplay.Debited.big(), afterReplay.Available.big(), afterReplay.Reserved.big())
	return nil
}

func acceptedPrice(cfg config) *pb.AcceptedPrice {
	return &pb.AcceptedPrice{
		PricePerUnitWei: &pb.BigUInt{Value: big.NewInt(cfg.priceWei).Bytes()},
		UnitsPerPrice:   cfg.perUnits, WorkUnitName: cfg.workUnit,
		Capability: cfg.capability, Offering: cfg.offering,
		QuoteRef: &pb.QuoteRef{QuoteId: "chain-probe:v1", QuoteVersion: 1,
			ConstraintFingerprint: []byte("chain-probe-constraints"), RouteFingerprint: []byte("chain-probe-route")},
	}
}

func createJobAuthorization(ctx context.Context, cfg config, payer pb.PayerDaemonClient, authorizationID, requestID string, body []byte, maxDebit *big.Int, callerPublicKey []byte) (*pb.CreateSpendAuthorizationResponse, error) {
	digest := sha256.Sum256(body)
	now := time.Now().UTC()
	return payer.CreateSpendAuthorization(ctx, &pb.CreateSpendAuthorizationRequest{
		Payee: cfg.recipient, AuthorizationId: authorizationID, RequestId: requestID,
		Protocol: "paid-job/v1", AcceptedPrice: acceptedPrice(cfg), MaxDebitWei: &pb.BigUInt{Value: maxDebit.Bytes()},
		MaxTotalUnits: cfg.maxAuthUnits, NotBefore: now.Add(-time.Minute).Format(time.RFC3339Nano),
		ExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339Nano), RequestDigest: digest[:],
		CallerPublicKey: callerPublicKey, BrokerUri: strings.TrimRight(cfg.brokerURI, "/"),
		ChainId: cfg.chainID, Denomination: "wei",
	})
}

func mintAccountShortfall(ctx context.Context, cfg config, payer pb.PayerDaemonClient, tag string, target, observed *big.Int) (*pb.CreatePaymentResponse, error) {
	return payer.CreatePayment(ctx, &pb.CreatePaymentRequest{
		Recipient: cfg.recipient, TicketParamsBaseUrl: cfg.brokerURL,
		AcceptedPrice: acceptedPrice(cfg),
		Funding:       &pb.FundingIntent{FundedValueWei: &pb.BigUInt{Value: target.Bytes()}, EstimatedUnits: cfg.maxAuthUnits},
		MintRequestId: fmt.Sprintf("chain-probe-wholesale-fund-%s-%d", tag, time.Now().UnixNano()),
		AccountFunding: &pb.AccountFundingIntent{
			TargetAvailableWei:   &pb.BigUInt{Value: target.Bytes()},
			ObservedAvailableWei: &pb.BigUInt{Value: observed.Bytes()},
		},
	})
}

func assertShortfall(m *pb.CreatePaymentResponse, want *big.Int) error {
	got := new(big.Int).SetBytes(m.GetAccountShortfallWei().GetValue())
	if got.Cmp(want) != 0 {
		return fmt.Errorf("reported shortfall=%s; want %s", got, want)
	}
	ev := new(big.Int).SetBytes(m.GetExpectedValue().GetValue())
	if ev.Cmp(want) != 0 {
		return fmt.Errorf("signed EV=%s; want exact shortfall %s", ev, want)
	}
	if (want.Sign() == 0) != (len(m.GetPaymentBytes()) == 0) {
		return fmt.Errorf("payment presence does not match shortfall=%s", want)
	}
	return nil
}

func invokeAuthorizedJob(cfg config, requestID string, body, authorization, callerProof, payment []byte) (*httpResult, error) {
	headers := map[string]string{
		"Livepeer-Capability": cfg.capability, "Livepeer-Offering": cfg.offering,
		"Livepeer-Protocol": "paid-job/v1", "Livepeer-Request-Id": requestID,
		"Livepeer-Authorization": base64.StdEncoding.EncodeToString(authorization),
	}
	if len(callerProof) > 0 {
		headers["Livepeer-Caller-Proof"] = base64.StdEncoding.EncodeToString(callerProof)
	}
	if len(payment) > 0 {
		headers["Livepeer-Payment"] = base64.StdEncoding.EncodeToString(payment)
	}
	return postJSON(cfg.brokerURL+"/v1/job", headers, string(body))
}

func invocationProof(authorization []byte, key *ecdsa.PrivateKey) ([]byte, error) {
	digest := crypto.Keccak256(append([]byte("livepeer-invocation-proof/v1\x00"), authorization...))
	sig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		return nil, err
	}
	sig[64] += 27
	return sig, nil
}

type decimal string

func (d decimal) big() *big.Int {
	n, ok := new(big.Int).SetString(string(d), 10)
	if !ok {
		return new(big.Int)
	}
	return n
}

type wholesaleAccount struct {
	Payer        string  `json:"payer"`
	Payee        string  `json:"payee"`
	ChainID      uint64  `json:"chain_id"`
	Denomination string  `json:"denomination"`
	Credited     decimal `json:"credited_value_wei"`
	Reserved     decimal `json:"reserved_value_wei"`
	Debited      decimal `json:"debited_value_wei"`
	Available    decimal `json:"available_value_wei"`
	Version      uint64  `json:"version"`
	ObservedAt   string  `json:"observed_at"`
}

func (a wholesaleAccount) validate() error {
	if a.ChainID == 0 || a.Denomination != "wei" {
		return fmt.Errorf("unexpected domain chain_id=%d denomination=%q", a.ChainID, a.Denomination)
	}
	want := new(big.Int).Sub(a.Credited.big(), new(big.Int).Add(a.Reserved.big(), a.Debited.big()))
	if want.Cmp(a.Available.big()) != 0 {
		return fmt.Errorf("conservation failure: credited %s != available %s + reserved %s + debited %s", a.Credited.big(), a.Available.big(), a.Reserved.big(), a.Debited.big())
	}
	return nil
}

func queryWholesaleAccount(brokerURL string, payer []byte) (*wholesaleAccount, error) {
	body, _ := json.Marshal(map[string]string{"payer_eth_address": "0x" + hex.EncodeToString(payer)})
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Post(strings.TrimRight(brokerURL, "/")+"/v1/payment/account", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw := readAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, raw)
	}
	var out wholesaleAccount
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func positiveDifference(target, available *big.Int) *big.Int {
	if available.Cmp(target) >= 0 {
		return new(big.Int)
	}
	return new(big.Int).Sub(target, available)
}

func positiveUnits(raw string) (uint64, error) {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("work units=%q on successful exchange", raw)
	}
	return n, nil
}
