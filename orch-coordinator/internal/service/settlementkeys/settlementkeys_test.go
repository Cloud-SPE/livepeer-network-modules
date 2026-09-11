package settlementkeys

import (
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/verify"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/types"
)

const (
	orchA   = "0xd00354656922168815fcd1e51cbddb9e359e3c7f"
	brokerA = "https://ai2-rig-broker.xode.app"
)

func newKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	k, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k, "0x" + hex.EncodeToString(crypto.FromECDSAPub(&k.PublicKey))
}

// announce signs a statement the way the broker does: JCS bytes,
// EIP-191, v in {27,28}.
func announce(t *testing.T, key *ecdsa.PrivateKey, st types.BrokerSettlementStatement) types.BrokerSettlementAnnouncement {
	t.Helper()
	canonical, err := canonicalStatement(st)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := crypto.Sign(verify.PersonalSignDigest(canonical), key)
	if err != nil {
		t.Fatal(err)
	}
	sig[64] += 27
	return types.BrokerSettlementAnnouncement{
		Statement: st,
		Signature: &types.BrokerSettlementSignature{Algorithm: "secp256k1", Canonicalization: "jcs", Value: "0x" + hex.EncodeToString(sig)},
	}
}

func statement(pub string) types.BrokerSettlementStatement {
	return types.BrokerSettlementStatement{
		OrchEthAddress: orchA, PublicKey: pub, BaseURL: brokerA, IssuedAt: "2026-09-08T12:00:00Z",
	}
}

func TestVerify_ProvenKey(t *testing.T) {
	key, pub := newKey(t)
	st := statement(pub)
	st.NotBefore, st.ExpiresAt = "2026-09-07T00:00:00Z", "2027-09-07T00:00:00Z"
	d := Verify(announce(t, key, st), strings.ToUpper(orchA), brokerA+"/")
	if !d.Proven {
		t.Fatalf("not proven: %s", d.Reason)
	}
	if d.PublicKey != pub || d.BaseURL != brokerA || d.NotBefore.Year() != 2026 || d.ExpiresAt.Year() != 2027 {
		t.Fatalf("discovered = %+v", d)
	}
}

// The canonical statement matches the broker's byte for byte: this is
// the JCS of the same struct, `&` escaped as encoding/json does.
func TestCanonicalStatement_MatchesBroker(t *testing.T) {
	got, err := canonicalStatement(types.BrokerSettlementStatement{
		OrchEthAddress: "0xaa", PublicKey: "0x04bb", BaseURL: "https://a.example/x?y=1&z=2", IssuedAt: "2026-09-08T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"base_url":"https://a.example/x?y=1\u0026z=2","issued_at":"2026-09-08T12:00:00Z","orch_eth_address":"0xaa","public_key":"0x04bb"}`
	if string(got) != want {
		t.Fatalf("canonical:\n got %s\nwant %s", got, want)
	}
}

func TestVerify_Rejections(t *testing.T) {
	key, pub := newKey(t)
	_, otherPub := newKey(t)
	good := announce(t, key, statement(pub))

	tampered := good
	tampered.Statement.IssuedAt = "2026-09-09T00:00:00Z"
	unsigned := types.BrokerSettlementAnnouncement{Statement: statement(pub), Error: "no signing key"}
	wrongKey := announce(t, key, statement(otherPub))
	wrongOrch := announce(t, key, func() types.BrokerSettlementStatement {
		st := statement(pub)
		st.OrchEthAddress = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		return st
	}())
	wrongURL := announce(t, key, func() types.BrokerSettlementStatement {
		st := statement(pub)
		st.BaseURL = "https://evil.example"
		return st
	}())
	badWindow := announce(t, key, func() types.BrokerSettlementStatement {
		st := statement(pub)
		st.NotBefore, st.ExpiresAt = "2027-01-01T00:00:00Z", "2026-01-01T00:00:00Z"
		return st
	}())
	badScheme := good
	badScheme.Signature = &types.BrokerSettlementSignature{Algorithm: "ed25519", Canonicalization: "jcs", Value: good.Signature.Value}

	for _, tc := range []struct {
		name string
		a    types.BrokerSettlementAnnouncement
		want error
	}{
		{"tampered", tampered, ErrUnproven},
		{"unsigned", unsigned, ErrUnsigned},
		{"claims another key", wrongKey, ErrUnproven},
		{"another orchestrator", wrongOrch, ErrWrongOrch},
		{"another broker URL", wrongURL, ErrWrongBaseURL},
		{"reversed window", badWindow, ErrMalformedWindow},
		{"scheme", badScheme, ErrScheme},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Verify(tc.a, orchA, brokerA)
			if d.Proven {
				t.Fatal("proven")
			}
			if !strings.Contains(d.Reason, tc.want.Error()) {
				t.Fatalf("reason %q, want %v", d.Reason, tc.want)
			}
		})
	}
}

func TestLedger_WindowIsStableThenReanchors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	validity := 30 * 24 * time.Hour
	nb, exp := l.Window("0x04aa", t0, validity)
	if !nb.Equal(t0) || !exp.Equal(t0.Add(validity)) {
		t.Fatalf("first window %s..%s", nb, exp)
	}
	// Stable across scrapes and across a restart.
	nb2, _ := l.Window("0x04aa", t0.Add(10*24*time.Hour), validity)
	if !nb2.Equal(t0) {
		t.Fatalf("window moved on a later scrape: %s", nb2)
	}
	reopened, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	nb3, _ := reopened.Window("0x04aa", t0.Add(10*24*time.Hour), validity)
	if !nb3.Equal(t0) {
		t.Fatalf("window moved after restart: %s", nb3)
	}
	// Two thirds elapsed: re-anchor, so the delegation renews by a sign
	// cycle instead of lapsing.
	t1 := t0.Add(21 * 24 * time.Hour)
	nb4, exp4 := reopened.Window("0x04aa", t1, validity)
	if !nb4.Equal(t1) || !exp4.Equal(t1.Add(validity)) {
		t.Fatalf("no re-anchor at two thirds: %s..%s", nb4, exp4)
	}
}

func TestMerge_PrecedenceCarryOverAndExpiry(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	win := &MemoryWindower{}
	cfgKey := types.SettlementKey{PublicKey: "0x04cc", NotBefore: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
	out, meta := Merge(MergeInput{
		Config: []types.SettlementKey{cfgKey},
		Brokers: []BrokerKeys{
			{Name: "ai2-rig", BaseURL: brokerA, Keys: []Discovered{
				{PublicKey: "0x04aa", Proven: true}, // unbounded → default window
				{PublicKey: "0x04bb", Proven: true, NotBefore: now.Add(-time.Hour), ExpiresAt: now.Add(48 * time.Hour)},  // broker window
				{PublicKey: "0x04dd", Proven: false, Reason: "nope"},                                                     // never delegated
				{PublicKey: "0x04ee", Proven: true, NotBefore: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-time.Hour)}, // expired
				{PublicKey: "0x04cc", Proven: true, NotBefore: now, ExpiresAt: now.Add(72 * time.Hour)},                  // config wins
			}},
		},
		Published: []types.SettlementKey{
			{PublicKey: "0x04ff", NotBefore: now.Add(-24 * time.Hour), ExpiresAt: now.Add(24 * time.Hour)}, // carried through rotation
			{PublicKey: "0x0400", NotBefore: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-time.Minute)},   // expired, dropped
			{PublicKey: "0x04aa", NotBefore: now.Add(-100 * time.Hour), ExpiresAt: now.Add(time.Hour)},     // broker wins
		},
		Now: now, Validity: 30 * 24 * time.Hour, Windows: win,
	})
	got := map[string]types.MetadataSettlementKey{}
	for _, m := range meta {
		got[m.PublicKey] = m
	}
	wantOrder := []string{"0x04aa", "0x04bb", "0x04cc", "0x04ff"}
	if len(out) != len(wantOrder) {
		t.Fatalf("keys = %+v", out)
	}
	for i, pk := range wantOrder {
		if out[i].PublicKey != pk {
			t.Fatalf("order[%d] = %s, want %s", i, out[i].PublicKey, pk)
		}
	}
	if m := got["0x04aa"]; m.Source != SourceBroker || m.WindowSource != WindowDefault || !m.NotBefore.Equal(now) || !m.ExpiresAt.Equal(now.Add(30*24*time.Hour)) {
		t.Fatalf("unbounded broker key: %+v", m)
	}
	if m := got["0x04bb"]; m.Source != SourceBroker || m.WindowSource != WindowBroker || m.Broker != "ai2-rig" {
		t.Fatalf("broker-windowed key: %+v", m)
	}
	if m := got["0x04cc"]; m.Source != SourceConfig || !m.ExpiresAt.Equal(cfgKey.ExpiresAt) {
		t.Fatalf("config precedence: %+v", m)
	}
	if m := got["0x04ff"]; m.Source != SourcePublished || m.WindowSource != WindowPublished {
		t.Fatalf("carry-over: %+v", m)
	}
	if out2, meta2 := Merge(MergeInput{Now: now}); out2 != nil || meta2 != nil {
		t.Fatalf("empty merge must be nil so the manifest omits the block: %v %v", out2, meta2)
	}
}

// Without a windower an unbounded key is reported but not delegated:
// a window nobody chose is not a window.
func TestMerge_UnboundedKeyNeedsAWindower(t *testing.T) {
	out, _ := Merge(MergeInput{
		Brokers: []BrokerKeys{{Name: "b", Keys: []Discovered{{PublicKey: "0x04aa", Proven: true}}}},
		Now:     time.Now(),
	})
	if out != nil {
		t.Fatalf("delegated without a window: %+v", out)
	}
	if !errors.Is(ErrUnproven, ErrUnproven) {
		t.Fatal("sanity")
	}
}
