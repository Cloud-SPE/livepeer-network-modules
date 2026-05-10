package sender

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/types"
)

// TicketParamsRequest is the sender-side input to the quote-free
// payee-issued TicketParams fetch.
type TicketParamsRequest struct {
	BaseURL    string
	Sender     []byte
	Recipient  []byte
	FaceValue  *big.Int
	Capability string
	Offering   string
}

// TicketParamsFetcher resolves authoritative payee-issued TicketParams
// for quote-free payments.
type TicketParamsFetcher interface {
	Fetch(ctx context.Context, req TicketParamsRequest) (*types.TicketParams, error)
}

// HTTPTicketParamsFetcher fetches TicketParams from a broker-side
// `/v1/payment/ticket-params` proxy.
type HTTPTicketParamsFetcher struct {
	httpClient *http.Client
}

// NewHTTPTicketParamsFetcher returns a fetcher that requires the caller
// to provide the broker base URL on each request.
func NewHTTPTicketParamsFetcher() *HTTPTicketParamsFetcher {
	return &HTTPTicketParamsFetcher{
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (f *HTTPTicketParamsFetcher) Fetch(ctx context.Context, req TicketParamsRequest) (*types.TicketParams, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("ticket params fetcher base URL is empty")
	}
	if req.FaceValue == nil || req.FaceValue.Sign() <= 0 {
		return nil, fmt.Errorf("face_value must be positive")
	}

	body, err := json.Marshal(ticketParamsHTTPRequest{
		SenderETHAddress:    hexAddress(req.Sender),
		RecipientETHAddress: hexAddress(req.Recipient),
		FaceValueWei:        req.FaceValue.String(),
		Capability:          req.Capability,
		Offering:            req.Offering,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/payment/ticket-params", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("post ticket params: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("ticket params status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var parsed ticketParamsHTTPResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return parsed.TicketParams.toTypes()
}

type ticketParamsHTTPRequest struct {
	SenderETHAddress    string `json:"sender_eth_address"`
	RecipientETHAddress string `json:"recipient_eth_address"`
	FaceValueWei        string `json:"face_value_wei"`
	Capability          string `json:"capability"`
	Offering            string `json:"offering"`
}

type ticketParamsHTTPResponse struct {
	TicketParams ticketParamsJSON `json:"ticket_params"`
}

type ticketParamsJSON struct {
	Recipient         string                     `json:"recipient"`
	FaceValue         string                     `json:"face_value"`
	WinProb           string                     `json:"win_prob"`
	RecipientRandHash string                     `json:"recipient_rand_hash"`
	Seed              string                     `json:"seed"`
	ExpirationBlock   string                     `json:"expiration_block"`
	ExpirationParams  ticketExpirationParamsJSON `json:"expiration_params"`
}

type ticketExpirationParamsJSON struct {
	CreationRound          int64  `json:"creation_round"`
	CreationRoundBlockHash string `json:"creation_round_block_hash"`
}

func (j ticketParamsJSON) toTypes() (*types.TicketParams, error) {
	recipient, err := parseHexAddress(j.Recipient, 20)
	if err != nil {
		return nil, fmt.Errorf("invalid recipient: %w", err)
	}
	faceValue, ok := new(big.Int).SetString(j.FaceValue, 10)
	if !ok {
		return nil, fmt.Errorf("invalid face_value %q", j.FaceValue)
	}
	winProb, ok := new(big.Int).SetString(j.WinProb, 10)
	if !ok {
		return nil, fmt.Errorf("invalid win_prob %q", j.WinProb)
	}
	randHash, err := parseHexAddress(j.RecipientRandHash, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid recipient_rand_hash: %w", err)
	}
	seed, err := parseHexAddress(j.Seed, -1)
	if err != nil {
		return nil, fmt.Errorf("invalid seed: %w", err)
	}
	expirationBlock := new(big.Int)
	if strings.TrimSpace(j.ExpirationBlock) != "" {
		if _, ok := expirationBlock.SetString(j.ExpirationBlock, 10); !ok {
			return nil, fmt.Errorf("invalid expiration_block %q", j.ExpirationBlock)
		}
	}
	var exp *types.TicketExpirationParams
	if j.ExpirationParams.CreationRound != 0 || strings.TrimSpace(j.ExpirationParams.CreationRoundBlockHash) != "" {
		blockHash, err := parseHexAddress(j.ExpirationParams.CreationRoundBlockHash, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid creation_round_block_hash: %w", err)
		}
		exp = &types.TicketExpirationParams{
			CreationRound:          j.ExpirationParams.CreationRound,
			CreationRoundBlockHash: blockHash,
		}
	}
	return &types.TicketParams{
		Recipient:         recipient,
		FaceValue:         faceValue,
		WinProb:           winProb,
		RecipientRandHash: randHash,
		Seed:              seed,
		ExpirationBlock:   expirationBlock,
		ExpirationParams:  exp,
	}, nil
}

func hexAddress(raw []byte) string {
	return "0x" + hex.EncodeToString(raw)
}

func parseHexAddress(raw string, expectedLen int) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		if expectedLen < 0 {
			return []byte{}, nil
		}
		return nil, fmt.Errorf("value is empty")
	}
	if !strings.HasPrefix(strings.ToLower(s), "0x") {
		return nil, fmt.Errorf("must be 0x-prefixed hex")
	}
	out, err := hex.DecodeString(s[2:])
	if err != nil {
		return nil, fmt.Errorf("must be hex: %w", err)
	}
	if expectedLen >= 0 && len(out) != expectedLen {
		return nil, fmt.Errorf("must be %d bytes", expectedLen)
	}
	return out, nil
}
