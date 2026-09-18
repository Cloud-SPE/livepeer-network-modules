package chaincommons

import (
	"bytes"
	"errors"
	"log/slog"
	"math/big"
	"strings"
	"testing"

	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
	cmetrics "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/metrics"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/keystore/inmemory"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
)

func TestLogger_RedactsURLFieldsToHost(t *testing.T) {
	var buf bytes.Buffer
	l := Logger(slog.New(slog.NewTextHandler(&buf, nil)))
	l.Warn("rpc.endpoint_failed",
		logger.String("url", "https://user:pw@rpc.example.com/v2/SECRETKEY"),
		logger.String("class", "transient"))
	l.With(logger.String("url", "https://rpc2.example.com/KEY2")).Info("hi")
	out := buf.String()
	if strings.Contains(out, "SECRETKEY") || strings.Contains(out, "KEY2") || strings.Contains(out, "user:pw") {
		t.Fatalf("secret leaked into log: %s", out)
	}
	if !strings.Contains(out, "url=rpc.example.com") || !strings.Contains(out, "url=rpc2.example.com") {
		t.Fatalf("host not logged: %s", out)
	}
	if !strings.Contains(out, "class=transient") {
		t.Fatalf("other fields must pass through: %s", out)
	}
	l.Debug("d")
	l.Error("e", logger.Err(nil))
}

func TestLogger_RedactsURLsInsideErrorText(t *testing.T) {
	var buf bytes.Buffer
	l := Logger(slog.New(slog.NewTextHandler(&buf, nil)))
	l.Info("rpc.endpoint_failed",
		logger.Err(errors.New(`Post "https://rpc.example.com/v2/SECRETKEY": dial tcp: connection refused`)),
		logger.String("detail", "tried http://other.example.com/KEY2 next"),
		logger.Int("attempt", 3))
	out := buf.String()
	if strings.Contains(out, "SECRETKEY") || strings.Contains(out, "KEY2") {
		t.Fatalf("secret leaked through error text: %s", out)
	}
	if !strings.Contains(out, "rpc.example.com") || !strings.Contains(out, "other.example.com") || !strings.Contains(out, "attempt=3") {
		t.Fatalf("hosts and other fields must survive: %s", out)
	}
	if got := RedactURLs("no urls here"); got != "no urls here" {
		t.Errorf("RedactURLs changed a plain string: %q", got)
	}
}

func TestLogger_NilFallsBackToDefault(t *testing.T) {
	if Logger(nil) == nil {
		t.Fatal("nil logger must yield a usable adapter")
	}
}

func TestRecorder_ForwardsToDynamic(t *testing.T) {
	p := metrics.NewPrometheus()
	r := Recorder(p)
	r.CounterAdd("livepeer_chain_rpc_calls_total", cmetrics.Labels{"method": "ChainID", "result": "ok"}, 1)
	r.GaugeSet("livepeer_chain_g", nil, 3)
	r.HistogramObserve("livepeer_chain_h", nil, 0.5)
	mf, err := p.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, m := range mf {
		names[m.GetName()] = true
	}
	for _, want := range []string{"livepeer_chain_rpc_calls_total", "livepeer_chain_g", "livepeer_chain_h"} {
		if !names[want] {
			t.Errorf("series %s not registered", want)
		}
	}
}

type notDynamic struct{ metrics.Recorder }

func TestRecorder_NonDynamicRecorderIsNoop(t *testing.T) {
	r := Recorder(notDynamic{})
	r.CounterAdd("x", nil, 1) // must not panic
	if Recorder(nil) == nil {
		t.Fatal("nil recorder must yield a no-op")
	}
}

func TestHost(t *testing.T) {
	if got := Host("https://user:pw@rpc.example.com/v2/SECRETKEY"); got != "rpc.example.com" {
		t.Fatalf("Host = %q", got)
	}
	if got := Host("::not a url"); got != "<invalid-url>" {
		t.Fatalf("Host invalid = %q", got)
	}
}

func TestKeystore_DelegatesToTheLoadedKey(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ks, err := inmemory.New(key)
	if err != nil {
		t.Fatal(err)
	}
	adapted, err := Keystore(ks, ks)
	if err != nil {
		t.Fatal(err)
	}
	want := crypto.PubkeyToAddress(key.PublicKey)
	if adapted.Address() != want {
		t.Fatalf("Address = %s, want %s", adapted.Address().Hex(), want.Hex())
	}

	// SignTx: the signed transaction recovers to the key's address on
	// the chain id the processor passes as chain.ChainID.
	tx := ethtypes.NewTx(&ethtypes.DynamicFeeTx{ChainID: big.NewInt(1337), Nonce: 1, Gas: 21_000,
		GasFeeCap: big.NewInt(10), GasTipCap: big.NewInt(1), To: &want})
	signed, err := adapted.SignTx(tx, chain.ChainID(1337))
	if err != nil {
		t.Fatal(err)
	}
	from, err := ethtypes.Sender(ethtypes.LatestSignerForChainID(big.NewInt(1337)), signed)
	if err != nil || from != want {
		t.Fatalf("sender = %s, %v", from.Hex(), err)
	}

	// Sign: EIP-191 personal-sign, identical bytes to the daemon's own.
	payload := []byte("ticket bytes")
	got, err := adapted.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	direct, _ := ks.Sign(payload)
	if !bytes.Equal(got, direct) {
		t.Fatal("adapter Sign differs from the daemon keystore's Sign")
	}
}

func TestKeystore_RejectsNil(t *testing.T) {
	key, _ := crypto.GenerateKey()
	ks, _ := inmemory.New(key)
	if _, err := Keystore(nil, ks); err == nil {
		t.Error("nil keystore accepted")
	}
	if _, err := Keystore(ks, nil); err == nil {
		t.Error("nil signer accepted")
	}
}
