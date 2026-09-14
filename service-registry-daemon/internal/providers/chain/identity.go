package chain

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
)

// ValidateRPCChainIDs checks every configured endpoint before failover is enabled.
// Endpoint credentials are deliberately excluded from errors.
func ValidateRPCChainIDs(ctx context.Context, urls []string, expected int64) error {
	if expected <= 0 || len(urls) == 0 {
		return fmt.Errorf("chain identity: positive chain ID and RPC endpoints required")
	}
	for i, url := range urls {
		err := validateRPCChainID(ctx, url, expected)
		if err != nil {
			return fmt.Errorf("chain identity: endpoint %d: %w", i+1, err)
		}
	}
	return nil
}
func validateRPCChainID(ctx context.Context, url string, expected int64) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := ethclient.DialContext(ctx, url)
	if err != nil {
		return fmt.Errorf("cannot connect")
	}
	defer client.Close()
	id, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("cannot read chain ID")
	}
	if !id.IsInt64() || id.Int64() != expected {
		return fmt.Errorf("expected chain ID %d, got %s", expected, id)
	}
	return nil
}
