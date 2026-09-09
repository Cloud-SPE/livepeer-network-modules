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
	"sync"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// probeWholesale proves the economic property the legacy probe cannot: one
// bounded payer-payee float backs unrelated workload authorizations, and a
// later large-ceiling request mints only the small shortfall created by actual
// prior usage. The first call models an in-path provider. The second binds an
// ephemeral caller key and models a clearinghouse delegating invocation.
func probeWholesale(ctx context.Context, cfg config, payer pb.PayerDaemonClient, payee pb.PayeeDaemonClient) error {
	if cfg.maxAuthUnits == 0 || cfg.chainID == 0 {
		return fmt.Errorf("chain-id and max-authorization-units must be positive")
	}
	if cfg.sessionMaxAuthUnits == 0 || cfg.sessionPriceWei <= 0 || cfg.sessionPerUnits == 0 || strings.TrimSpace(cfg.sessionRunnerControlURL) == "" {
		return fmt.Errorf("session price, per-units, authorization units, and runner control URL must be positive/non-empty")
	}
	if err := waitForOffering(cfg, 90*time.Second); err != nil {
		return err
	}
	sessionCfg := wholesaleSessionConfig(cfg)
	if err := waitForOffering(sessionCfg, 90*time.Second); err != nil {
		return fmt.Errorf("session offering: %w", err)
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

	concurrencyIssued, err := probeWholesaleConcurrency(ctx, cfg, payer, payee, payerAddress, target, body)
	if err != nil {
		return err
	}
	directSessionIssued, err := probeWholesaleSession(ctx, sessionCfg, payer, payee, payerAddress, target, false)
	if err != nil {
		return fmt.Errorf("provider session: %w", err)
	}
	delegatedSessionIssued, err := probeWholesaleSession(ctx, sessionCfg, payer, payee, payerAddress, target, true)
	if err != nil {
		return fmt.Errorf("delegated session: %w", err)
	}
	final, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return fmt.Errorf("final account: %w", err)
	}
	if err := final.validate(); err != nil {
		return fmt.Errorf("final account: %w", err)
	}
	totalIssued := new(big.Int).Add(issued, concurrencyIssued)
	totalIssued.Add(totalIssued, directSessionIssued)
	totalIssued.Add(totalIssued, delegatedSessionIssued)
	if got := new(big.Int).Sub(final.Credited.big(), before.Credited.big()); got.Cmp(totalIssued) != 0 {
		return fmt.Errorf("whole-pilot issued EV=%s but credited delta=%s", totalIssued, got)
	}
	if final.Reserved.big().Sign() != 0 {
		return fmt.Errorf("whole-pilot left %s wei reserved", final.Reserved.big())
	}
	fmt.Printf("  whole-pilot reconciliation issued=%s debited_delta=%s remaining_available=%s reserved=0 wei\n",
		totalIssued, new(big.Int).Sub(final.Debited.big(), before.Debited.big()), final.Available.big())
	return nil
}

func wholesaleSessionConfig(cfg config) config {
	cfg.capability = cfg.sessionCapability
	cfg.offering = cfg.sessionOffering
	cfg.workUnit = cfg.sessionWorkUnit
	cfg.priceWei = cfg.sessionPriceWei
	cfg.perUnits = cfg.sessionPerUnits
	cfg.maxAuthUnits = cfg.sessionMaxAuthUnits
	return cfg
}

func ensureWholesaleFloat(ctx context.Context, cfg config, payer pb.PayerDaemonClient, payee pb.PayeeDaemonClient, payerAddress []byte, target *big.Int, tag string) (*big.Int, error) {
	before, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return nil, err
	}
	minted, err := mintAccountShortfall(ctx, cfg, payer, tag, target, before.Available.big())
	if err != nil {
		return nil, err
	}
	shortfall := positiveDifference(target, before.Available.big())
	if err := assertShortfall(minted, shortfall); err != nil {
		return nil, err
	}
	if shortfall.Sign() > 0 {
		funded, err := payee.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{PaymentBytes: minted.GetPaymentBytes()})
		if err != nil {
			return nil, err
		}
		if got := new(big.Int).SetBytes(funded.GetCreditedValueWei().GetValue()); got.Cmp(shortfall) != 0 {
			return nil, fmt.Errorf("payee credited %s; minted %s", got, shortfall)
		}
	}
	return shortfall, nil
}

func probeWholesaleConcurrency(ctx context.Context, cfg config, payer pb.PayerDaemonClient, payee pb.PayeeDaemonClient, payerAddress []byte, target *big.Int, body []byte) (*big.Int, error) {
	issued, err := ensureWholesaleFloat(ctx, cfg, payer, payee, payerAddress, target, "concurrency")
	if err != nil {
		return nil, fmt.Errorf("concurrency float: %w", err)
	}
	baseline, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return nil, err
	}
	maxDebit := billFor(cfg.priceWei, cfg.perUnits, cfg.maxAuthUnits)
	type attempt struct {
		id   string
		auth []byte
		res  *pb.AdmitAuthorizationResponse
		err  error
	}
	attempts := make([]attempt, 2)
	for i := range attempts {
		id := fmt.Sprintf("chain-probe-wholesale-concurrent-%d-%d", time.Now().UnixNano(), i)
		auth, err := createJobAuthorization(ctx, cfg, payer, id, "request-"+id, body, maxDebit, nil)
		if err != nil {
			return nil, fmt.Errorf("concurrency authorization %d: %w", i, err)
		}
		attempts[i] = attempt{id: id, auth: auth.GetAuthorizationBytes()}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			attempts[i].res, attempts[i].err = payee.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: attempts[i].auth})
		}(i)
	}
	close(start)
	wg.Wait()
	winners := make([]int, 0, len(attempts))
	losers := make([]int, 0, len(attempts))
	for i := range attempts {
		if attempts[i].err == nil {
			winners = append(winners, i)
		} else if status.Code(attempts[i].err) == codes.FailedPrecondition {
			losers = append(losers, i)
		} else {
			return nil, fmt.Errorf("concurrent admission %d: %w", i, attempts[i].err)
		}
	}
	affordable := new(big.Int).Quo(baseline.Available.big(), maxDebit).Uint64()
	if affordable > uint64(len(attempts)) {
		affordable = uint64(len(attempts))
	}
	if uint64(len(winners)) != affordable {
		return nil, fmt.Errorf("concurrent admissions=%d; balance %s affords %d reservations of %s", len(winners), baseline.Available.big(), affordable, maxDebit)
	}
	during, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return nil, err
	}
	wantReserved := new(big.Int).Mul(maxDebit, big.NewInt(int64(len(winners))))
	if got := new(big.Int).Sub(during.Reserved.big(), baseline.Reserved.big()); got.Cmp(wantReserved) != 0 {
		return nil, fmt.Errorf("concurrent reserved delta=%s; want %s", got, wantReserved)
	}
	if got := new(big.Int).Sub(baseline.Available.big(), during.Available.big()); got.Cmp(wantReserved) != 0 {
		return nil, fmt.Errorf("concurrent available delta=%s; want %s", got, wantReserved)
	}
	for _, i := range winners {
		if _, err := payee.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{Payer: payerAddress, AuthorizationId: attempts[i].id, ActualUnits: 0, SettlementSeq: 1}); err != nil {
			return nil, fmt.Errorf("release admitted reservation %d: %w", i, err)
		}
	}
	for _, i := range losers {
		if _, err := payee.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: attempts[i].auth}); err != nil {
			return nil, fmt.Errorf("admit refused authorization %d after release: %w", i, err)
		}
		if _, err := payee.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{Payer: payerAddress, AuthorizationId: attempts[i].id, ActualUnits: 0, SettlementSeq: 1}); err != nil {
			return nil, fmt.Errorf("release retried reservation %d: %w", i, err)
		}
	}
	after, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return nil, err
	}
	if after.Reserved.big().Sign() != 0 || after.Available != baseline.Available || after.Debited != baseline.Debited {
		return nil, fmt.Errorf("concurrency changed settled account: before=%+v after=%+v", baseline, after)
	}
	fmt.Printf("  concurrent reservation race admitted exactly affordable=%d; refused=%d later admitted after release without another mint\n", len(winners), len(losers))
	return issued, nil
}

func probeWholesaleSession(ctx context.Context, cfg config, payer pb.PayerDaemonClient, payee pb.PayeeDaemonClient, payerAddress []byte, target *big.Int, delegated bool) (*big.Int, error) {
	label := "provider"
	var callerKey *ecdsa.PrivateKey
	var callerPublic []byte
	if delegated {
		label = "delegated"
		var err error
		callerKey, err = crypto.GenerateKey()
		if err != nil {
			return nil, err
		}
		callerPublic = crypto.FromECDSAPub(&callerKey.PublicKey)
	}
	gatewayID := fmt.Sprintf("chain-probe-wholesale-session-%s-%d", label, time.Now().UnixNano())
	requestID := "request-" + gatewayID
	body := []byte(fmt.Sprintf(`{"gateway_session_id":%q,"session_params":{}}`, gatewayID))
	maxDebit := billFor(cfg.priceWei, cfg.perUnits, cfg.maxAuthUnits)
	auth, err := createSessionAuthorization(ctx, cfg, payer, gatewayID, requestID, body, maxDebit, callerPublic)
	if err != nil {
		return nil, err
	}
	before, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return nil, err
	}
	minted, err := mintAccountShortfall(ctx, cfg, payer, "session-"+label, target, before.Available.big())
	if err != nil {
		return nil, err
	}
	shortfall := positiveDifference(target, before.Available.big())
	if err := assertShortfall(minted, shortfall); err != nil {
		return nil, err
	}
	var proof []byte
	if delegated {
		proof, err = invocationProof(auth.GetAuthorizationBytes(), callerKey)
		if err != nil {
			return nil, err
		}
	}
	open, err := invokeAuthorizedSession(cfg, requestID, body, auth.GetAuthorizationBytes(), proof, minted.GetPaymentBytes())
	if err != nil {
		return nil, err
	}
	if open.status != http.StatusCreated && open.status != http.StatusOK {
		return nil, fmt.Errorf("open status %d error=%q body=%s", open.status, open.errCode, open.body)
	}
	sessionID, _ := open.field("session_id").(string)
	credential, _ := open.field("credential").(string)
	if sessionID == "" || credential == "" {
		return nil, fmt.Errorf("open response missing session identity: %s", open.body)
	}
	afterOpen, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return nil, err
	}
	reservedDelta := new(big.Int).Sub(afterOpen.Reserved.big(), before.Reserved.big())
	if reservedDelta.Sign() <= 0 || reservedDelta.Cmp(maxDebit) >= 0 {
		return nil, fmt.Errorf("initial session reservation %s is not bounded below max debit %s", reservedDelta, maxDebit)
	}
	const actualUnits = uint64(9)
	eventID := fmt.Sprintf("event-%s-%d", label, time.Now().UnixNano())
	event := fmt.Sprintf(`{"event_id":%q,"sequence":1,"event_type":"session.usage.tick","usage":{"unit":%q,"total":%d}}`, eventID, cfg.workUnit, actualUnits)
	eventResult, err := postJSON(strings.TrimRight(cfg.sessionRunnerControlURL, "/")+"/__livepeer_probe/event", nil, event)
	if err != nil {
		return nil, err
	}
	if eventResult.status != http.StatusOK {
		return nil, fmt.Errorf("usage callback status %d body=%s", eventResult.status, eventResult.body)
	}
	afterUsage, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return nil, err
	}
	if got, want := new(big.Int).Sub(afterUsage.Debited.big(), before.Debited.big()), billFor(cfg.priceWei, cfg.perUnits, actualUnits); got.Cmp(want) != 0 {
		return nil, fmt.Errorf("usage debit=%s; want %s", got, want)
	}
	topupID := fmt.Sprintf("chain-probe-wholesale-session-topup-%s-%d", label, time.Now().UnixNano())
	headers := map[string]string{"Authorization": "Bearer " + credential, "Livepeer-Request-Id": topupID}
	topup, err := postJSON(cfg.brokerURL+"/v1/session/"+sessionID+"/topup", headers, "")
	if err != nil {
		return nil, fmt.Errorf("account runway topup: %w", err)
	}
	if topup.status != http.StatusOK {
		return nil, fmt.Errorf("account runway topup status=%d body=%s", topup.status, topup.body)
	}
	replay, err := postJSON(cfg.brokerURL+"/v1/session/"+sessionID+"/topup", headers, "")
	if err != nil {
		return nil, fmt.Errorf("account runway topup replay: %w", err)
	}
	if replay.status != topup.status || replay.body != topup.body {
		return nil, fmt.Errorf("account runway topup replay diverged status=%d/%d", topup.status, replay.status)
	}
	ended, err := postJSON(cfg.brokerURL+"/v1/session/"+sessionID+"/end", map[string]string{"Authorization": "Bearer " + credential}, `{"reason":"gateway_close"}`)
	if err != nil {
		return nil, fmt.Errorf("end: %w", err)
	}
	if ended.status != http.StatusOK {
		return nil, fmt.Errorf("end status=%d body=%s", ended.status, ended.body)
	}
	authState, err := payee.GetSpendAuthorization(ctx, &pb.GetSpendAuthorizationRequest{Payer: payerAddress, AuthorizationId: gatewayID})
	if err != nil {
		return nil, err
	}
	if authState.GetState() != pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED || authState.GetActualUnits() != actualUnits {
		return nil, fmt.Errorf("authorization terminal state=%s units=%d", authState.GetState(), authState.GetActualUnits())
	}
	afterEnd, err := queryWholesaleAccount(cfg.brokerURL, payerAddress)
	if err != nil {
		return nil, err
	}
	if afterEnd.Reserved.big().Cmp(before.Reserved.big()) != 0 {
		return nil, fmt.Errorf("session left reservation: before=%s after=%s", before.Reserved.big(), afterEnd.Reserved.big())
	}
	settlement, err := fetchSettlement(cfg.brokerURL, gatewayID)
	if err != nil {
		return nil, err
	}
	if got := new(big.Int).SetBytes(settlement.payload.BilledValueWei.value()); got.Cmp(billFor(cfg.priceWei, cfg.perUnits, actualUnits)) != 0 {
		return nil, fmt.Errorf("settlement billed=%s", got)
	}
	fmt.Printf("  %s session reserved bounded runway=%s, settled %d units, released residual, replayed topup\n", label, reservedDelta, actualUnits)
	return shortfall, nil
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

func createSessionAuthorization(ctx context.Context, cfg config, payer pb.PayerDaemonClient, authorizationID, requestID string, body []byte, maxDebit *big.Int, callerPublicKey []byte) (*pb.CreateSpendAuthorizationResponse, error) {
	digest := sha256.Sum256(body)
	now := time.Now().UTC()
	return payer.CreateSpendAuthorization(ctx, &pb.CreateSpendAuthorizationRequest{
		Payee: cfg.recipient, AuthorizationId: authorizationID, RequestId: requestID,
		SessionId: authorizationID, Protocol: "paid-session/v1", AcceptedPrice: acceptedPrice(cfg),
		MaxDebitWei: &pb.BigUInt{Value: maxDebit.Bytes()}, MaxTotalUnits: cfg.maxAuthUnits,
		NotBefore: now.Add(-time.Minute).Format(time.RFC3339Nano), ExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339Nano),
		RequestDigest: digest[:], CallerPublicKey: callerPublicKey,
		BrokerUri: strings.TrimRight(cfg.brokerURI, "/"), ChainId: cfg.chainID, Denomination: "wei",
	})
}

func mintAccountShortfall(ctx context.Context, cfg config, payer pb.PayerDaemonClient, tag string, target, observed *big.Int) (*pb.CreatePaymentResponse, error) {
	return mintAccountShortfallWithID(ctx, cfg, payer,
		fmt.Sprintf("chain-probe-wholesale-fund-%s-%d", tag, time.Now().UnixNano()), target, observed)
}

func mintAccountShortfallWithID(ctx context.Context, cfg config, payer pb.PayerDaemonClient, mintRequestID string, target, observed *big.Int) (*pb.CreatePaymentResponse, error) {
	return payer.CreatePayment(ctx, &pb.CreatePaymentRequest{
		Recipient: cfg.recipient, TicketParamsBaseUrl: cfg.brokerURL,
		AcceptedPrice: acceptedPrice(cfg),
		Funding:       &pb.FundingIntent{FundedValueWei: &pb.BigUInt{Value: target.Bytes()}, EstimatedUnits: cfg.maxAuthUnits},
		MintRequestId: mintRequestID,
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

func invokeAuthorizedSession(cfg config, requestID string, body, authorization, callerProof, payment []byte) (*httpResult, error) {
	headers := map[string]string{
		"Livepeer-Capability": cfg.capability, "Livepeer-Offering": cfg.offering,
		"Livepeer-Protocol": "paid-session/v1", "Livepeer-Request-Id": requestID,
		"Livepeer-Authorization": base64.StdEncoding.EncodeToString(authorization),
	}
	if len(callerProof) > 0 {
		headers["Livepeer-Caller-Proof"] = base64.StdEncoding.EncodeToString(callerProof)
	}
	if len(payment) > 0 {
		headers["Livepeer-Payment"] = base64.StdEncoding.EncodeToString(payment)
	}
	return postJSON(cfg.brokerURL+"/v1/session", headers, string(body))
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
