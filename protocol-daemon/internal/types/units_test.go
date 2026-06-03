package types

import (
	"math/big"
	"testing"
)

func TestPercentToPPM(t *testing.T) {
	cases := []struct {
		in      string
		want    uint64
		wantErr bool
	}{
		{"0", 0, false},
		{"100", 1_000_000, false},
		{"50", 500_000, false},
		{"10", 100_000, false},
		{"95.5", 955_000, false},
		{"33.3333", 333_333, false},
		// 1 ppm == 0.0001%; finer precision truncates toward zero.
		{"95.55551", 955_555, false},
		{" 25 ", 250_000, false}, // surrounding whitespace tolerated
		{"100.0001", 0, true},    // over 100
		{"-1", 0, true},          // negative rejected
		{"1e2", 0, true},         // scientific notation rejected
		{"", 0, true},
		{"abc", 0, true},
		{"1.2.3", 0, true},
	}
	for _, c := range cases {
		got, err := PercentToPPM(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("PercentToPPM(%q) = %d, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("PercentToPPM(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("PercentToPPM(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPPMToPercentRoundTrip(t *testing.T) {
	cases := []struct {
		ppm  uint64
		want string
	}{
		{1_000_000, "100"},
		{955_000, "95.5"},
		{100_000, "10"},
		{0, "0"},
		{333_333, "33.3333"},
	}
	for _, c := range cases {
		if got := PPMToPercent(c.ppm); got != c.want {
			t.Errorf("PPMToPercent(%d) = %q, want %q", c.ppm, got, c.want)
		}
	}
}

func TestDecimalToWei(t *testing.T) {
	cases := []struct {
		in      string
		want    string // decimal big.Int
		wantErr bool
	}{
		{"1", "1000000000000000000", false},
		{"0.03", "30000000000000000", false},
		{"0", "0", false},
		{"12.5", "12500000000000000000", false},
		// sub-wei precision truncates toward zero
		{"0.0000000000000000009", "0", false},
		{"1.000000000000000001", "1000000000000000001", false},
		{"-1", "", true},
		{"", "", true},
		{"0x10", "", true},
	}
	for _, c := range cases {
		got, err := DecimalToWei(c.in, EthDecimals)
		if c.wantErr {
			if err == nil {
				t.Errorf("DecimalToWei(%q) = %v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("DecimalToWei(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("DecimalToWei(%q) = %s, want %s", c.in, got.String(), c.want)
		}
	}
}

func TestWeiToDecimalRoundTrip(t *testing.T) {
	cases := []struct {
		wei  string
		want string
	}{
		{"1000000000000000000", "1"},
		{"30000000000000000", "0.03"},
		{"0", "0"},
		{"12500000000000000000", "12.5"},
	}
	for _, c := range cases {
		wei, _ := new(big.Int).SetString(c.wei, 10)
		if got := WeiToDecimal(wei, EthDecimals); got != c.want {
			t.Errorf("WeiToDecimal(%s) = %q, want %q", c.wei, got, c.want)
		}
	}
	if got := WeiToDecimal(nil, EthDecimals); got != "0" {
		t.Errorf("WeiToDecimal(nil) = %q, want %q", got, "0")
	}
}
