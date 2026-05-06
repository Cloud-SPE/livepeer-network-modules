package signing

import (
	"errors"
	"testing"
)

func TestParseEthAddress_LowerCases(t *testing.T) {
	a, err := ParseEthAddress("0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	if err != nil {
		t.Fatal(err)
	}
	if a.String() != "0xabcdef1234567890abcdef1234567890abcdef12" {
		t.Fatalf("not lower-cased: %s", a)
	}
}

func TestParseEthAddress_Rejects(t *testing.T) {
	cases := []string{
		"abcdef1234567890abcdef1234567890abcdef12",
		"0x12",
		"0xZZZ",
		"",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := ParseEthAddress(c); err == nil || !errors.Is(err, ErrInvalidEthAddress) {
				t.Fatalf("expected ErrInvalidEthAddress, got %v", err)
			}
		})
	}
}

func TestEthAddress_EqualCaseInsensitive(t *testing.T) {
	a, _ := ParseEthAddress("0xAB" + "00000000000000000000000000000000000000")
	b := EthAddress("0xab00000000000000000000000000000000000000")
	if !a.Equal(b) {
		t.Fatalf("Equal failed: %s vs %s", a, b)
	}
}
