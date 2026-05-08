package types

import (
	"errors"
	"testing"
)

func TestParseEthAddress_LowercasesAndValidates(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    EthAddress
		wantErr bool
	}{
		{"lower", "0xabcdef0000000000000000000000000000000000", "0xabcdef0000000000000000000000000000000000", false},
		{"upper", "0xABCDEF0000000000000000000000000000000000", "0xabcdef0000000000000000000000000000000000", false},
		{"mixed", "0xAbCdEf0000000000000000000000000000000000", "0xabcdef0000000000000000000000000000000000", false},
		{"no-prefix", "abcdef0000000000000000000000000000000000", "", true},
		{"too-short", "0xabcd", "", true},
		{"too-long", "0xabcdef000000000000000000000000000000000000", "", true},
		{"non-hex", "0xZZcdef0000000000000000000000000000000000", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEthAddress(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestParseEthAddress_ErrorWrapsSentinel(t *testing.T) {
	_, err := ParseEthAddress("not-an-address")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidEthAddress) {
		t.Fatalf("expected ErrInvalidEthAddress, got %v", err)
	}
}

func TestEthAddress_Equal(t *testing.T) {
	a, _ := ParseEthAddress("0xABCDEF0000000000000000000000000000000000")
	b, _ := ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	if !a.Equal(b) {
		t.Fatalf("equal addresses compared unequal: %s vs %s", a, b)
	}
}

func TestEthAddress_Bytes(t *testing.T) {
	a, _ := ParseEthAddress("0xabcdef0000000000000000000000000000000001")
	b := a.Bytes()
	if len(b) != 20 {
		t.Fatalf("expected 20 bytes, got %d", len(b))
	}
	if b[19] != 0x01 {
		t.Fatalf("expected last byte 0x01, got 0x%02x", b[19])
	}
}
