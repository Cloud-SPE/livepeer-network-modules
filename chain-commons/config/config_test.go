package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
)

func writeKS(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "keystore.json")
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write keystore: %v", err)
	}
	return p
}

func validConfig(t *testing.T) Config {
	c := Default()
	c.EthURLs = []string{"https://example/rpc"}
	c.ChainID = 42161
	c.KeystorePath = writeKS(t)
	c.KeystorePassword = "test"
	c.ControllerAddr = chain.Address{0x01}
	c.StorePath = filepath.Join(t.TempDir(), "db.bolt")
	return c
}

func TestDefault(t *testing.T) {
	c := Default()
	if c.GasLimit == 0 {
		t.Errorf("Default GasLimit should be > 0")
	}
	if c.RPC.MaxRetries == 0 {
		t.Errorf("Default RPC.MaxRetries should be > 0")
	}
	if c.TxIntent.KeyParamsEncoding != "rlp" {
		t.Errorf("Default KeyParamsEncoding = %q, want rlp", c.TxIntent.KeyParamsEncoding)
	}
}

func TestValidate_Success(t *testing.T) {
	c := validConfig(t)
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestValidate_MissingEthURLs(t *testing.T) {
	c := validConfig(t)
	c.EthURLs = nil
	if err := c.Validate(); err == nil {
		t.Errorf("Validate() should fail when EthURLs empty")
	} else if !strings.Contains(err.Error(), "EthURLs") {
		t.Errorf("error should mention EthURLs, got: %v", err)
	}
}

func TestValidate_MissingChainID(t *testing.T) {
	c := validConfig(t)
	c.ChainID = 0
	if err := c.Validate(); err == nil {
		t.Errorf("Validate() should fail when ChainID=0")
	}
}

func TestValidate_KeystoreNotReadable(t *testing.T) {
	c := validConfig(t)
	c.KeystorePath = "/no/such/file"
	if err := c.Validate(); err == nil {
		t.Errorf("Validate() should fail when KeystorePath unreadable")
	}
}

func TestValidate_NoControllerAndNoSkip(t *testing.T) {
	c := validConfig(t)
	c.ControllerAddr = chain.Address{}
	if err := c.Validate(); err == nil {
		t.Errorf("Validate() should fail with zero ControllerAddr and SkipController=false")
	}
}

func TestValidate_SkipControllerOK(t *testing.T) {
	c := validConfig(t)
	c.ControllerAddr = chain.Address{}
	c.SkipController = true
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() with SkipController=true should pass: %v", err)
	}
}

func TestValidate_GasPriceMinGreaterThanMax(t *testing.T) {
	c := validConfig(t)
	c.GasPriceMin = Wei(100)
	c.GasPriceMax = Wei(50)
	if err := c.Validate(); err == nil {
		t.Errorf("Validate() should fail when GasPriceMin > GasPriceMax")
	}
}

func TestValidate_BadKeyParamsEncoding(t *testing.T) {
	c := validConfig(t)
	c.TxIntent.KeyParamsEncoding = "bson"
	if err := c.Validate(); err == nil {
		t.Errorf("Validate() should fail on unknown KeyParamsEncoding")
	}
}

func TestValidate_BadGasBump(t *testing.T) {
	c := validConfig(t)
	c.TxIntent.ReplacementGasBump = 200
	if err := c.Validate(); err == nil {
		t.Errorf("Validate() should fail on ReplacementGasBump out of range")
	}
}

func TestValidate_ZeroReorgConfirmations(t *testing.T) {
	c := validConfig(t)
	c.ReorgConfirmations = 0
	if err := c.Validate(); err == nil {
		t.Errorf("Validate() should fail on ReorgConfirmations=0")
	}
}

func TestWei(t *testing.T) {
	w := Wei(100)
	if w.Uint64() != 100 {
		t.Errorf("Wei(100) = %s", w)
	}
}
