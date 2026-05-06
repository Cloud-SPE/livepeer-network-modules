// fixturegen produces the canonical Payment fixture consumed by
// internal/compat/wire_test.go.
//
// It constructs a fully-populated `net.Payment` using go-livepeer's own
// proto types, marshals it to the protobuf wire format, and writes the
// bytes to ../testdata/payment-canonical.bin. The resulting byte
// sequence is the ground-truth reference for the round-trip wire-compat
// test in the parent module.
//
// This tool lives in a sibling Go module (separate go.mod) so importing
// go-livepeer does not pollute the daemon binary's dependency graph
// (libp2p, ipfs-core, ffmpeg cgo bindings would otherwise leak in).
//
// Run ad-hoc:
//
//	cd payment-daemon/internal/compat/fixturegen
//	go run .
package main

import (
	"fmt"
	"os"
	"path/filepath"

	lpnet "github.com/livepeer/go-livepeer/net"
	"google.golang.org/protobuf/proto"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fixturegen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	payment := buildCanonicalPayment()
	bytes, err := proto.Marshal(payment)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	outPath := filepath.Join("..", "testdata", "payment-canonical.bin")
	if err := os.WriteFile(outPath, bytes, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("wrote %d bytes to %s\n", len(bytes), outPath)
	return nil
}

// buildCanonicalPayment constructs a fully-populated Payment that
// exercises every field in every nested message. Values are
// deterministic — rerunning the generator produces byte-identical
// output. The byte patterns (0x01..0x14, 0xa0..0xb3, etc.) are chosen so
// hex dumps are easy to spot-check.
func buildCanonicalPayment() *lpnet.Payment {
	recipient := []byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a,
		0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14,
	}
	sender := []byte{
		0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9,
		0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf, 0xb0, 0xb1, 0xb2, 0xb3,
	}
	faceValue := deterministicBytes(0x20, 32)
	winProb := deterministicBytes(0x30, 32)
	recipientRandHash := deterministicBytes(0x40, 32)
	seed := deterministicBytes(0x50, 32)
	expirationBlock := deterministicBytes(0x60, 8)
	creationRoundHash := deterministicBytes(0x70, 32)

	expirationParams := &lpnet.TicketExpirationParams{
		CreationRound:          12345,
		CreationRoundBlockHash: creationRoundHash,
	}

	ticketParams := &lpnet.TicketParams{
		Recipient:         recipient,
		FaceValue:         faceValue,
		WinProb:           winProb,
		RecipientRandHash: recipientRandHash,
		Seed:              seed,
		ExpirationBlock:   expirationBlock,
		ExpirationParams:  expirationParams,
	}

	priceInfo := &lpnet.PriceInfo{
		PricePerUnit:  1_000_000,
		PixelsPerUnit: 100,
	}

	ticketSenderParams := []*lpnet.TicketSenderParams{
		{SenderNonce: 1, Sig: deterministicBytes(0x80, 65)},
		{SenderNonce: 2, Sig: deterministicBytes(0x81, 65)},
		{SenderNonce: 3, Sig: deterministicBytes(0x82, 65)},
	}

	return &lpnet.Payment{
		TicketParams:       ticketParams,
		Sender:             sender,
		ExpirationParams:   expirationParams,
		TicketSenderParams: ticketSenderParams,
		ExpectedPrice:      priceInfo,
	}
}

func deterministicBytes(seed byte, n int) []byte {
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = seed + byte(i)
	}
	return out
}
