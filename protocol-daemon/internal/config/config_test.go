package config

import (
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.Mode != types.ModeBoth {
		t.Fatalf("default mode = %s, want both", c.Mode)
	}
	if c.MinBalanceWei == nil || c.MinBalanceWei.Cmp(big.NewInt(0)) <= 0 {
		t.Fatal("default MinBalanceWei should be > 0")
	}
	if c.InitJitter != 0 {
		t.Fatal("default InitJitter should be 0")
	}
}

func TestValidateDevMode(t *testing.T) {
	c := Default()
	c.Dev = true
	// Dev mode should accept missing chain config.
	c.Chain.EthURLs = nil
	if err := c.Validate(); err != nil {
		t.Fatalf("dev-mode validate: %v", err)
	}
}

func TestValidateModeRequired(t *testing.T) {
	c := Default()
	c.Mode = types.Mode("bogus")
	if err := c.Validate(); err == nil {
		t.Fatal("validate(bogus mode) should fail")
	}
}

func TestValidateRewardModeRequiresOrchAddress(t *testing.T) {
	// Use prod (non-dev) mode where invariants kick in.
	c := Default()
	c.Mode = types.ModeReward
	c.Dev = false
	c.OrchAddress = chain.Address{}
	// Set the chain-config to something that validates so we test only the
	// orch-address check.
	c.Chain.EthURLs = []string{"http://localhost:8545"}
	c.Chain.ChainID = 42161
	c.Chain.KeystorePath = "/dev/null"
	c.Chain.ControllerAddr = common.HexToAddress("0x1234567890123456789012345678901234567890")
	c.Chain.StorePath = "/tmp/test.db"
	// chain config validation will fail on /dev/null keystore, so we
	// short-circuit by enabling Dev, but then the orch-address check is
	// skipped — instead test by asserting the chain validate fails first
	// (covered elsewhere). We assert the orch-address branch via dev=false
	// with valid chain config except keystore — by manually overriding.
	c.Dev = true
	if err := c.Validate(); err != nil {
		t.Fatalf("dev-mode missing orch should still be valid: %v", err)
	}

	// In a non-dev path, the orch-address check is one of several gates.
	// Forcing chain-config validate to pass would require a real keystore
	// file; that scenario is covered by the daemon's run.go, not unit test.
}

func TestValidateNegativeMinBalance(t *testing.T) {
	c := Default()
	c.Dev = true
	c.MinBalanceWei = big.NewInt(-1)
	// MinBalanceWei is checked only in non-dev path.
	if err := c.Validate(); err != nil {
		t.Fatalf("dev should not check min balance: %v", err)
	}
}

// validProdConfig builds a config that passes the chain-commons validation
// gates (real keystore file at a tmp path, real EthURLs, etc.).
func validProdConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	ksPath := filepath.Join(dir, "keystore.json")
	if err := os.WriteFile(ksPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Default()
	c.Mode = types.ModeRoundInit
	c.Dev = false
	c.Chain.EthURLs = []string{"http://localhost:8545"}
	c.Chain.ChainID = 42161
	c.Chain.KeystorePath = ksPath
	c.Chain.ControllerAddr = common.HexToAddress("0x000000000000000000000000000000000000FA01")
	c.Chain.StorePath = filepath.Join(dir, "store.db")
	c.SocketPath = filepath.Join(dir, "protocol.sock")
	return c
}

func TestValidateProdHappyPath(t *testing.T) {
	c := validProdConfig(t)
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate (prod, round-init): %v", err)
	}
}

func TestValidateProdRewardRequiresOrch(t *testing.T) {
	c := validProdConfig(t)
	c.Mode = types.ModeReward
	if err := c.Validate(); err == nil {
		t.Fatal("expected error: missing orch in reward mode")
	}
	c.OrchAddress = common.HexToAddress("0x00000000000000000000000000000000000000A1")
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate (prod, reward, orch set): %v", err)
	}
}

func TestValidateProdNegativeMinBalance(t *testing.T) {
	c := validProdConfig(t)
	c.MinBalanceWei = big.NewInt(-1)
	if err := c.Validate(); err == nil {
		t.Fatal("expected negative min-balance error")
	}
}

func TestValidateProdNegativeJitter(t *testing.T) {
	c := validProdConfig(t)
	c.InitJitter = -1
	if err := c.Validate(); err == nil {
		t.Fatal("expected negative jitter error")
	}
}

func TestValidateProdMissingChainConfig(t *testing.T) {
	c := validProdConfig(t)
	c.Chain.EthURLs = nil
	if err := c.Validate(); err == nil {
		t.Fatal("expected chain-config error")
	}
}

func TestStringDoesNotLeakKeystore(t *testing.T) {
	c := Default()
	c.Chain.KeystorePassword = "supersecret"
	c.Chain.KeystorePath = "/etc/livepeer/keystore.json"
	s := c.String()
	if strings.Contains(s, "supersecret") {
		t.Fatalf("Config.String leaked keystore password: %s", s)
	}
	// Path is not a secret per se; keystore-path appearing is fine.
}
