// Package envelope mints `Livepeer-Payment` header values for runner
// fixtures by delegating to the local `payer-daemon` over gRPC.
//
// v0.2 architectural shift: the runner no longer hand-rolls Payment
// proto bytes. Instead it dials a sender-mode payment-daemon co-located
// in the conformance compose stack and calls
// `PayerDaemon.CreatePayment(face_value, recipient, capability,
// offering)`. The daemon returns the wire-format Payment bytes; this
// package base64-encodes them for the HTTP header.
//
// The daemon dependency makes the runner the FIRST end-to-end
// integration of the new wire-compat sender path. The OpenAI-compat
// gateway is the second (plan 0014 C7).
package envelope

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// Mint mints a fresh Payment envelope for a (capability, offering)
// pair and returns the base64-encoded wire bytes plus the sender
// address embedded in the envelope. The sender is needed by the
// conformance runner's plan-0015 fixtures to call PayeeDaemon.GetBalance
// against a specific session.
func Mint(ctx context.Context, capability, offering string) (envelopeBase64 string, sender []byte, err error) {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return "", nil, errors.New("envelope: payer-daemon client not initialized; call Init() at startup")
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := c.CreatePayment(cctx, &pb.CreatePaymentRequest{
		FaceValue:  uint64Bytes(defaultFaceValue),
		Recipient:  hexBytes(defaultRecipientHex),
		Capability: capability,
		Offering:   offering,
	})
	if err != nil {
		return "", nil, fmt.Errorf("payer-daemon CreatePayment: %w", err)
	}
	var payload pb.Payment
	if err := proto.Unmarshal(resp.GetPaymentBytes(), &payload); err != nil {
		return "", nil, fmt.Errorf("decode minted payment: %w", err)
	}
	return base64.StdEncoding.EncodeToString(resp.GetPaymentBytes()), append([]byte(nil), payload.GetSender()...), nil
}

const (
	// DefaultRecipient is the 20-byte recipient address the runner
	// uses when minting envelopes. Stable across runs so the daemon
	// can dedupe sessions.
	defaultRecipientHex = "1234567890abcdef1234567890abcdef12345678"

	// defaultFaceValue is the runner's chosen target spend per
	// request, in wei. The receiver may return a larger
	// `TicketParams.face_value` plus a lower `win_prob` per the
	// quote-free flow; the runner doesn't care.
	defaultFaceValue uint64 = 1000
)

// client + setup mutex for the package-level singleton. The runner
// initializes once at startup via Init.
var (
	mu     sync.Mutex
	client pb.PayerDaemonClient
	conn   *grpc.ClientConn
)

// Init dials the payer-daemon at socketPath, Health-probes it, and
// stores a package-level client used by SubstituteHeaders. Idempotent
// — calling more than once is fine.
func Init(ctx context.Context, socketPath string) error {
	mu.Lock()
	defer mu.Unlock()
	if client != nil {
		return nil
	}
	c, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial payer-daemon at %s: %w", socketPath, err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pc := pb.NewPayerDaemonClient(c)
	if _, err := pc.Health(probeCtx, &pb.HealthRequest{}); err != nil {
		_ = c.Close()
		return fmt.Errorf("payer-daemon health probe at %s: %w", socketPath, err)
	}
	conn = c
	client = pc
	return nil
}

// Shutdown closes the gRPC connection. Optional; the OS reaps it on
// process exit.
func Shutdown() {
	mu.Lock()
	defer mu.Unlock()
	if conn != nil {
		_ = conn.Close()
		conn = nil
		client = nil
	}
}

// SubstituteHeaders walks the fixture's request headers, replaces the
// `<runner-generated-payment-blob>` placeholder on `Livepeer-Payment`
// with a freshly-minted base64 envelope from the payer-daemon, and
// returns the modified map. Other headers pass through unchanged.
//
// If neither `Livepeer-Capability` nor `Livepeer-Offering` is set in
// the headers map (e.g., a fixture exercising rejection paths), the
// placeholder is left untouched.
func SubstituteHeaders(headers map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[k] = v
	}
	const placeholder = "<runner-generated-payment-blob>"
	const livepeerPayment = "Livepeer-Payment"

	if out[livepeerPayment] != placeholder {
		return out, nil
	}
	cap := lookupCanonical(out, "Livepeer-Capability")
	off := lookupCanonical(out, "Livepeer-Offering")
	if cap == "" || off == "" {
		// Leave the placeholder; the fixture is exercising rejection.
		return out, nil
	}
	envelope, err := mintEnvelope(cap, off)
	if err != nil {
		return nil, err
	}
	out[livepeerPayment] = envelope
	return out, nil
}

func mintEnvelope(capability, offering string) (string, error) {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return "", errors.New("envelope: payer-daemon client not initialized; call Init() at startup")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := c.CreatePayment(ctx, &pb.CreatePaymentRequest{
		FaceValue:  uint64Bytes(defaultFaceValue),
		Recipient:  hexBytes(defaultRecipientHex),
		Capability: capability,
		Offering:   offering,
	})
	if err != nil {
		return "", fmt.Errorf("payer-daemon CreatePayment: %w", err)
	}
	return base64.StdEncoding.EncodeToString(resp.GetPaymentBytes()), nil
}

func lookupCanonical(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func uint64Bytes(n uint64) []byte {
	if n == 0 {
		return nil
	}
	out := make([]byte, 0, 8)
	for n > 0 {
		out = append([]byte{byte(n & 0xff)}, out...)
		n >>= 8
	}
	return out
}

func hexBytes(s string) []byte {
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi := hexNibble(s[2*i])
		lo := hexNibble(s[2*i+1])
		out[i] = (hi << 4) | lo
	}
	return out
}

func hexNibble(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	}
	return 0
}
