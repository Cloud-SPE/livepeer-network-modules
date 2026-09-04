package ethclient

import (
	"crypto/ecdsa"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	cckeystore "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/keystore"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// ecdsaKeystore presents the executor's already-decrypted hot-wallet key
// as chain-commons's keystore.Keystore, so the transaction intent
// processor signs with the same key the executor has always used. Sign
// is EIP-191 personal_sign and SignTx uses the latest signer for the
// chain id, matching chain-commons's own V3 keystore implementation.
type ecdsaKeystore struct {
	key  *ecdsa.PrivateKey
	addr chain.Address
}

var (
	_ cckeystore.Keystore  = (*ecdsaKeystore)(nil)
	_ cckeystore.RawSigner = (*ecdsaKeystore)(nil)
)

func newECDSAKeystore(key *ecdsa.PrivateKey) *ecdsaKeystore {
	return &ecdsaKeystore{key: key, addr: crypto.PubkeyToAddress(key.PublicKey)}
}

func (k *ecdsaKeystore) Address() chain.Address { return k.addr }

func (k *ecdsaKeystore) Sign(payload []byte) ([]byte, error) {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(payload))
	return crypto.Sign(crypto.Keccak256([]byte(prefix), payload), k.key)
}

func (k *ecdsaKeystore) RawSign(payload []byte) ([]byte, error) {
	return crypto.Sign(crypto.Keccak256(payload), k.key)
}

func (k *ecdsaKeystore) SignTx(tx *ethtypes.Transaction, chainID chain.ChainID) (*ethtypes.Transaction, error) {
	return ethtypes.SignTx(tx, ethtypes.LatestSignerForChainID(chainID.BigInt()), k.key)
}
