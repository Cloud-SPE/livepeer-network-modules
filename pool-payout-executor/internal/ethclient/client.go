package ethclient

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"strings"

	ethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethereum "github.com/ethereum/go-ethereum"
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
	if strings.TrimSpace(cfg.RPCURL) == "" {
		return nil, fmt.Errorf("executor.rpc_url is required")
	}
	key, err := loadPrivateKey(cfg)
	if err != nil {
		return nil, err
	}
	rpc, err := goethclient.DialContext(ctx, cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial rpc: %w", err)
	}
	chainID, err := rpc.ChainID(ctx)
	if err != nil {
		rpc.Close()
		return nil, fmt.Errorf("fetch chain id: %w", err)
	}
	if cfg.ChainID != 0 && chainID.Uint64() != cfg.ChainID {
		rpc.Close()
		return nil, fmt.Errorf("rpc chain id %d does not match configured chain_id %d", chainID.Uint64(), cfg.ChainID)
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
