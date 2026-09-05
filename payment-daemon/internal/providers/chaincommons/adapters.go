// Package chaincommons adapts the payment-daemon's logging, metrics and
// keystore surfaces to the interfaces chain-commons expects, so the
// shared chain glue (multi-RPC failover, Controller resolver, gas
// oracle, transaction intents) logs through the daemon's slog handler,
// lands its series on the daemon's /metrics, and signs with the
// daemon's already-unlocked key.
package chaincommons

import (
	"errors"
	"log/slog"
	"net/url"
	"regexp"

	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/keystore"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
	cmetrics "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/metrics"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
)

// Logger wraps an *slog.Logger as a chain-commons logger.Logger. A nil
// logger yields slog.Default().
//
// Every URL in a field value is reduced to its host before it reaches
// the handler: chain-commons logs the endpoint on failover, go-ethereum
// quotes the full URL inside transport errors, and RPC providers put
// API keys in the path — none of which may reach a log line.
func Logger(l *slog.Logger) logger.Logger {
	if l == nil {
		l = slog.Default()
	}
	return &slogAdapter{l: l}
}

type slogAdapter struct{ l *slog.Logger }

func (s *slogAdapter) toAttrs(fields []logger.Field) []any {
	a := make([]any, 0, len(fields)*2)
	for _, f := range fields {
		a = append(a, f.Key, redactValue(f.Value))
	}
	return a
}

// urlPattern matches anything that looks like an http(s) URL inside a
// string. Kept loose on purpose: over-redacting a log field is cheap,
// leaking a provider key is not.
var urlPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

// RedactURLs replaces every URL in s with its host.
func RedactURLs(s string) string {
	return urlPattern.ReplaceAllStringFunc(s, Host)
}

func redactValue(v any) any {
	switch x := v.(type) {
	case string:
		return RedactURLs(x)
	case error:
		if x == nil {
			return v
		}
		return RedactURLs(x.Error())
	}
	return v
}

func (s *slogAdapter) Debug(msg string, fields ...logger.Field) { s.l.Debug(msg, s.toAttrs(fields)...) }
func (s *slogAdapter) Info(msg string, fields ...logger.Field)  { s.l.Info(msg, s.toAttrs(fields)...) }
func (s *slogAdapter) Warn(msg string, fields ...logger.Field)  { s.l.Warn(msg, s.toAttrs(fields)...) }
func (s *slogAdapter) Error(msg string, fields ...logger.Field) { s.l.Error(msg, s.toAttrs(fields)...) }
func (s *slogAdapter) With(fields ...logger.Field) logger.Logger {
	return &slogAdapter{l: s.l.With(s.toAttrs(fields)...)}
}

// Recorder wraps the daemon's metrics.Recorder as a chain-commons
// metrics.Recorder. Emissions land on the daemon's registry under the
// names chain-commons chooses (livepeer_chain_*), alongside the
// daemon's own livepeer_payment_* series, which are untouched. A
// recorder that does not implement metrics.Dynamic gets a no-op.
func Recorder(rec metrics.Recorder) cmetrics.Recorder {
	d, ok := rec.(metrics.Dynamic)
	if !ok || d == nil {
		return cmetrics.NoOp()
	}
	return &recorderAdapter{d: d}
}

type recorderAdapter struct{ d metrics.Dynamic }

func (r *recorderAdapter) CounterAdd(name string, labels cmetrics.Labels, delta float64) {
	r.d.CounterAdd(name, labels, delta)
}

func (r *recorderAdapter) GaugeSet(name string, labels cmetrics.Labels, value float64) {
	r.d.GaugeSet(name, labels, value)
}

func (r *recorderAdapter) HistogramObserve(name string, labels cmetrics.Labels, value float64) {
	r.d.HistogramObserve(name, labels, value)
}

// Host returns the host part of an RPC URL for logging. Credentials and
// path (where provider API keys usually live) are dropped; an
// unparseable value yields "<invalid-url>".
func Host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<invalid-url>"
	}
	return u.Host
}

// Keystore wraps the daemon's KeyStore + TxSigner pair (one loaded key,
// two interfaces) as the single chain-commons keystore.Keystore the
// transaction-intent processor signs with. Address, EIP-191 Sign and
// EIP-155/1559 SignTx all delegate; nothing is re-derived.
func Keystore(ks providers.KeyStore, signer providers.TxSigner) (keystore.Keystore, error) {
	if ks == nil {
		return nil, errors.New("chaincommons: nil keystore")
	}
	if signer == nil {
		return nil, errors.New("chaincommons: nil tx signer")
	}
	return &keystoreAdapter{ks: ks, signer: signer}, nil
}

type keystoreAdapter struct {
	ks     providers.KeyStore
	signer providers.TxSigner
}

func (k *keystoreAdapter) Address() chain.Address {
	return ethcommon.BytesToAddress(k.ks.Address())
}

func (k *keystoreAdapter) Sign(payload []byte) ([]byte, error) {
	return k.ks.Sign(payload)
}

func (k *keystoreAdapter) SignTx(tx *ethtypes.Transaction, chainID chain.ChainID) (*ethtypes.Transaction, error) {
	return k.signer.SignTx(tx, chainID.BigInt())
}
