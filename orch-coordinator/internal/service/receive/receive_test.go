package receive

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/verify"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/candidates"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/published"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/candidate"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

func newSvc(t *testing.T) (*Service, *ecdsa.PrivateKey, string) {
	t.Helper()
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.ToLower(ethcrypto.PubkeyToAddress(priv.PublicKey).Hex())
	dir := t.TempDir()
	store, err := published.New(filepath.Join(dir, "published"))
	if err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(filepath.Join(dir, "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	return New(store, nil, log, addr, "0.1.0", nil), priv, addr
}

func sampleManifest(addr string, seq uint64) types.ManifestPayload {
	now := time.Now().UTC()
	return types.ManifestPayload{
		SpecVersion:    "0.1.0",
		PublicationSeq: seq,
		IssuedAt:       now,
		ExpiresAt:      now.Add(24 * time.Hour),
		Orch:           types.Orch{EthAddress: addr},
		Capabilities: []types.CapabilityTuple{{
			CapabilityID:    "cap",
			OfferingID:      "off",
			InteractionMode: "http-stream@v1",
			WorkUnit:        types.WorkUnit{Name: "tokens"},
			PricePerUnitWei: "100",
			WorkerURL:       "https://worker.example/",
		}},
	}
}

func signManifest(t *testing.T, priv *ecdsa.PrivateKey, p types.ManifestPayload) []byte {
	t.Helper()
	canonical, err := candidate.CanonicalBytes(manifestPayloadMap(p))
	if err != nil {
		t.Fatal(err)
	}
	digest := verify.PersonalSignDigest(canonical)
	sig, err := ethcrypto.Sign(digest, priv)
	if err != nil {
		t.Fatal(err)
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	sm := types.SignedManifest{
		Manifest: p,
		Signature: types.Signature{
			Algorithm: "secp256k1",
			Value:     "0x" + hex.EncodeToString(sig),
		},
	}
	body, err := json.Marshal(sm)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestReceive_HappyPath(t *testing.T) {
	svc, priv, addr := newSvc(t)
	body := signManifest(t, priv, sampleManifest(addr, 1))
	res, err := svc.Receive(body, "test-uploader")
	if err != nil {
		t.Fatal(err)
	}
	if res.PublicationSeq != 1 {
		t.Fatalf("seq: %d", res.PublicationSeq)
	}
	livePath := svc.store.Path()
	if _, _, err := svc.store.Read(); err != nil {
		t.Fatalf("expected published bytes, got %v", err)
	}
	_ = livePath
}

func TestReceive_RejectsRollback(t *testing.T) {
	svc, priv, addr := newSvc(t)
	if _, err := svc.Receive(signManifest(t, priv, sampleManifest(addr, 5)), "u1"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Receive(signManifest(t, priv, sampleManifest(addr, 4)), "u2")
	var ve *VerifyError
	if !errIs(err, &ve) || ve.Code != audit.OutcomeRollbackRejected {
		t.Fatalf("expected rollback_rejected, got %v", err)
	}
}

func TestReceive_RejectsExpired(t *testing.T) {
	svc, priv, addr := newSvc(t)
	p := sampleManifest(addr, 1)
	p.ExpiresAt = time.Now().UTC().Add(-time.Hour)
	body := signManifest(t, priv, p)
	_, err := svc.Receive(body, "x")
	var ve *VerifyError
	if !errIs(err, &ve) || ve.Code != audit.OutcomeWindowInvalid {
		t.Fatalf("expected window_invalid, got %v", err)
	}
}

func TestReceive_RejectsWrongSigner(t *testing.T) {
	svc, _, _ := newSvc(t)
	wrong, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	wrongAddr := strings.ToLower(ethcrypto.PubkeyToAddress(wrong.PublicKey).Hex())
	body := signManifest(t, wrong, sampleManifest(wrongAddr, 1))
	_, err = svc.Receive(body, "x")
	var ve *VerifyError
	if !errIs(err, &ve) {
		t.Fatalf("expected VerifyError, got %v", err)
	}
}

func TestReceive_RejectsSpecDrift(t *testing.T) {
	svc, priv, addr := newSvc(t)
	p := sampleManifest(addr, 1)
	p.SpecVersion = "0.2.0"
	body := signManifest(t, priv, p)
	_, err := svc.Receive(body, "x")
	var ve *VerifyError
	if !errIs(err, &ve) || ve.Code != audit.OutcomeDriftRejected {
		t.Fatalf("expected drift_rejected, got %v", err)
	}
}

func TestReceive_RejectsNonHTTPSWorkerURL(t *testing.T) {
	svc, priv, addr := newSvc(t)
	p := sampleManifest(addr, 1)
	p.Capabilities[0].WorkerURL = "http://insecure/"
	body := signManifest(t, priv, p)
	_, err := svc.Receive(body, "x")
	var ve *VerifyError
	if !errIs(err, &ve) || ve.Code != audit.OutcomeSchemaInvalid {
		t.Fatalf("expected schema_invalid, got %v", err)
	}
}

func TestReceive_AuditCapturesAcceptAndReject(t *testing.T) {
	svc, priv, addr := newSvc(t)
	if _, err := svc.Receive(signManifest(t, priv, sampleManifest(addr, 1)), "ok"); err != nil {
		t.Fatal(err)
	}
	p := sampleManifest(addr, 0) // rollback
	if _, err := svc.Receive(signManifest(t, priv, p), "bad"); err == nil {
		t.Fatal("expected reject")
	}
	got, err := svc.audit.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("expected ≥2 events, got %d", len(got))
	}
	outcomes := make(map[audit.Outcome]int)
	for _, e := range got {
		outcomes[e.Outcome]++
	}
	if outcomes[audit.OutcomeAccepted] == 0 || outcomes[audit.OutcomeRollbackRejected] == 0 {
		t.Fatalf("missing outcomes: %+v", outcomes)
	}
}

func TestReceive_RejectsSignedManifestThatDoesNotMatchLatestCandidate(t *testing.T) {
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.ToLower(ethcrypto.PubkeyToAddress(priv.PublicKey).Hex())
	dir := t.TempDir()
	pubStore, err := published.New(filepath.Join(dir, "published"))
	if err != nil {
		t.Fatal(err)
	}
	candStore, err := candidates.New(filepath.Join(dir, "candidates"), 0)
	if err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(filepath.Join(dir, "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })

	p1 := sampleManifest(addr, 1)
	p2 := sampleManifest(addr, 1)
	p2.Capabilities[0].PricePerUnitWei = "101"
	latestCandidate, err := candidate.CanonicalBytes(manifestPayloadMap(p2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candStore.Save(candidates.Snapshot{
		Timestamp:     time.Now().UTC(),
		ManifestBytes: latestCandidate,
	}); err != nil {
		t.Fatal(err)
	}

	svc := New(pubStore, candStore, log, addr, "0.1.0", nil)
	_, err = svc.Receive(signManifest(t, priv, p1), "u1")
	var ve *VerifyError
	if !errIs(err, &ve) || ve.Code != audit.OutcomeDriftRejected {
		t.Fatalf("expected drift_rejected, got %v", err)
	}
}

func TestReceive_AcceptedPublishAdvancesNextPublicationSeq(t *testing.T) {
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.ToLower(ethcrypto.PubkeyToAddress(priv.PublicKey).Hex())
	dir := t.TempDir()
	pubStore, err := published.New(filepath.Join(dir, "published"))
	if err != nil {
		t.Fatal(err)
	}
	candStore, err := candidates.New(filepath.Join(dir, "candidates"), 0)
	if err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(filepath.Join(dir, "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	p := sampleManifest(addr, 3)
	latestCandidate, err := candidate.CanonicalBytes(manifestPayloadMap(p))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candStore.Save(candidates.Snapshot{
		Timestamp:     time.Now().UTC(),
		ManifestBytes: latestCandidate,
	}); err != nil {
		t.Fatal(err)
	}
	builder := &stubSeqSetter{}
	svc := New(pubStore, candStore, log, addr, "0.1.0", builder)
	if _, err := svc.Receive(signManifest(t, priv, p), "u1"); err != nil {
		t.Fatal(err)
	}
	if builder.seq != 4 {
		t.Fatalf("next seq = %d, want 4", builder.seq)
	}
}

type stubSeqSetter struct{ seq uint64 }

func (s *stubSeqSetter) SetPublicationSeq(seq uint64) { s.seq = seq }

// errIs is a fmt-friendly errors.As that works for our nested
// VerifyError pointer.
func errIs(err error, target **VerifyError) bool {
	for err != nil {
		if v, ok := err.(*VerifyError); ok {
			*target = v
			return true
		}
		type unwrap interface{ Unwrap() error }
		u, ok := err.(unwrap)
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return false
}

var _ = fmt.Sprintf
var _ = os.ErrNotExist
