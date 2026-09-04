package ethclient

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"log/slog"
	"math/big"
	"net/url"
	"os"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	ethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	goethclient "github.com/ethereum/go-ethereum/ethclient"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
)

type Client struct {
	rpc     *goethclient.Client
	key     *ecdsa.PrivateKey
	from    ethcommon.Address
	chainID *big.Int
}

type SentTransfer struct {
	TxHash string `json:"tx_hash"`
	Nonce  uint64 `json:"nonce"`
}

func New(ctx context.Context, cfg config.Executor) (*Client, error) {
	urls := trimmedURLs(cfg.RPCURLs)
	if len(urls) == 0 {
		return nil, fmt.Errorf("executor.rpc_urls is required")
	}
	key, err := loadPrivateKey(cfg)
	if err != nil {
		return nil, err
	}
	rpc, chainID, err := dialFirstHealthy(ctx, urls, cfg.ChainID)
	if err != nil {
		return nil, err
	}
	return &Client{
		rpc:     rpc,
		key:     key,
		from:    crypto.PubkeyToAddress(key.PublicKey),
		chainID: chainID,
	}, nil
}

func loadPrivateKey(cfg config.Executor) (*ecdsa.PrivateKey, error) {
	if strings.TrimSpace(cfg.KeystorePath) != "" || strings.TrimSpace(cfg.KeystorePasswordPath) != "" {
		if strings.TrimSpace(cfg.KeystorePath) == "" {
			return nil, fmt.Errorf("executor.keystore_path is required")
		}
		if strings.TrimSpace(cfg.KeystorePasswordPath) == "" {
			return nil, fmt.Errorf("executor.keystore_password_path is required")
		}
		keystoreJSON, err := os.ReadFile(strings.TrimSpace(cfg.KeystorePath))
		if err != nil {
			return nil, fmt.Errorf("read keystore: %w", err)
		}
		passwordRaw, err := os.ReadFile(strings.TrimSpace(cfg.KeystorePasswordPath))
		if err != nil {
			return nil, fmt.Errorf("read keystore password: %w", err)
		}
		key, err := ethkeystore.DecryptKey(keystoreJSON, strings.TrimSpace(string(passwordRaw)))
		if err != nil {
			return nil, fmt.Errorf("decrypt keystore: %w", err)
		}
		return key.PrivateKey, nil
	}
	if strings.TrimSpace(cfg.PrivateKeyRef) == "" {
		return nil, fmt.Errorf("executor.keystore_path + executor.keystore_password_path or executor.private_key_ref is required")
	}
	rawKey, err := resolveSecret(cfg.PrivateKeyRef)
	if err != nil {
		return nil, err
	}
	key, err := crypto.HexToECDSA(strip0x(rawKey))
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return key, nil
}

func (c *Client) Close() {
	if c != nil && c.rpc != nil {
		c.rpc.Close()
	}
}

func (c *Client) FromAddress() ethcommon.Address {
	return c.from
}

func (c *Client) BalanceAt(ctx context.Context) (*big.Int, error) {
	return c.rpc.BalanceAt(ctx, c.from, nil)
}

func (c *Client) SendNativeTransfer(ctx context.Context, to ethcommon.Address, amountWei *big.Int) (SentTransfer, error) {
	nonce, err := c.rpc.PendingNonceAt(ctx, c.from)
	if err != nil {
		return SentTransfer{}, fmt.Errorf("pending nonce: %w", err)
	}
	tipCap, err := c.rpc.SuggestGasTipCap(ctx)
	if err != nil {
		return SentTransfer{}, fmt.Errorf("suggest gas tip cap: %w", err)
	}
	header, err := c.rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		return SentTransfer{}, fmt.Errorf("latest header: %w", err)
	}
	baseFee := big.NewInt(0)
	if header.BaseFee != nil {
		baseFee = new(big.Int).Set(header.BaseFee)
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tipCap)
	gasLimit, err := c.rpc.EstimateGas(ctx, ethereum.CallMsg{
		From:      c.from,
		To:        &to,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Value:     amountWei,
	})
	if err != nil {
		return SentTransfer{}, fmt.Errorf("estimate gas: %w", err)
	}
	if gasLimit == 0 {
		return SentTransfer{}, fmt.Errorf("estimate gas returned 0")
	}
	gasLimit += gasLimit / 10
	tx := ethtypes.NewTx(&ethtypes.DynamicFeeTx{
		ChainID:   c.chainID,
		Nonce:     nonce,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Gas:       gasLimit,
		To:        &to,
		Value:     amountWei,
		Data:      nil,
	})
	signed, err := ethtypes.SignTx(tx, ethtypes.LatestSignerForChainID(c.chainID), c.key)
	if err != nil {
		return SentTransfer{}, fmt.Errorf("sign tx: %w", err)
	}
	if err := c.rpc.SendTransaction(ctx, signed); err != nil {
		return SentTransfer{}, fmt.Errorf("send tx: %w", err)
	}
	return SentTransfer{TxHash: signed.Hash().Hex(), Nonce: nonce}, nil
}

func (c *Client) ConfirmTransaction(ctx context.Context, txHash string, confirmationBlocks uint64) (bool, error) {
	receipt, err := c.rpc.TransactionReceipt(ctx, ethcommon.HexToHash(txHash))
	if err != nil {
		return false, fmt.Errorf("transaction receipt: %w", err)
	}
	if receipt == nil {
		return false, fmt.Errorf("transaction receipt not found")
	}
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return false, nil
	}
	if confirmationBlocks <= 1 {
		return true, nil
	}
	head, err := c.rpc.BlockNumber(ctx)
	if err != nil {
		return false, fmt.Errorf("latest block number: %w", err)
	}
	if head+1 < receipt.BlockNumber.Uint64()+confirmationBlocks {
		return false, nil
	}
	return true, nil
}

func resolveSecret(ref string) (string, error) {
	key := strings.TrimPrefix(ref, "env://")
	if key == "" {
		return "", fmt.Errorf("secret ref must not be empty")
	}
	value, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("env var %q is not set", key)
	}
	if value == "" {
		return "", fmt.Errorf("env var %q is empty", key)
	}
	return value, nil
}

func strip0x(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "0x")
}

// dialFirstHealthy dials urls in order and returns the first client that
// connects and — when expected is non-zero — reports that chain id.
// Rejected candidates are logged at warn with their position and host
// only (provider API keys usually live in the path). When none works
// the error names how many were tried.
//
// This is startup selection, not failover: the chosen *goethclient.Client
// is held for the executor's lifetime. Runtime failover between the
// entries is tracked separately.
func dialFirstHealthy(ctx context.Context, urls []string, expected uint64) (*goethclient.Client, *big.Int, error) {
	for i, raw := range urls {
		rpc, err := goethclient.DialContext(ctx, raw)
		if err == nil {
			var chainID *big.Int
			chainID, err = rpc.ChainID(ctx)
			if err == nil && (expected == 0 || chainID.Uint64() == expected) {
				return rpc, chainID, nil
			}
			if err == nil {
				err = fmt.Errorf("rpc chain id %d does not match configured chain_id %d", chainID.Uint64(), expected)
			}
			rpc.Close()
		}
		// go-ethereum quotes the full URL in dial and transport errors,
		// and that is where provider API keys live: redact it to the host.
		slog.Warn("rpc candidate rejected", "index", i, "host", urlHost(raw), "err", strings.ReplaceAll(err.Error(), raw, urlHost(raw)))
	}
	return nil, nil, fmt.Errorf("no usable rpc endpoint among %d candidate(s)", len(urls))
}

// trimmedURLs drops blank entries so a YAML list with an empty item
// reads as "not set" rather than as a dial of "".
func trimmedURLs(in []string) []string {
	var out []string
	for _, u := range in {
		if s := strings.TrimSpace(u); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// urlHost returns the host of an RPC URL for logging, dropping
// credentials and path.
func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<invalid-url>"
	}
	return u.Host
}
