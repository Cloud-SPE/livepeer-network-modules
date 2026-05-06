package providers

import "math/big"

// DevGasPrice is a constant-value gas price for dev mode.
type DevGasPrice struct {
	wei *big.Int
}

// NewDevGasPrice returns a fake gas price provider that always reports
// 0.1 gwei × 200% = 0.2 gwei (a representative Arbitrum One value).
func NewDevGasPrice() *DevGasPrice {
	// 0.2 gwei = 2e8 wei
	return &DevGasPrice{wei: big.NewInt(200_000_000)}
}

// Current returns the configured constant gas price.
func (g *DevGasPrice) Current() *big.Int {
	return new(big.Int).Set(g.wei)
}
