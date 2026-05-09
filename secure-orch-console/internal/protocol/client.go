package protocol

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	protocolv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/protocol/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type Client struct {
	conn grpc.ClientConnInterface
	rpc  protocolv1.ProtocolDaemonClient
}

type Snapshot struct {
	Health            Field[Health]
	Round             Field[Round]
	Reward            Field[Reward]
	ServiceRegistry   Field[Registry]
	AIServiceRegistry Field[Registry]
	Wallet            Field[Wallet]
}

type TxIntent struct {
	ID            string
	Kind          string
	Status        string
	FailedClass   string
	FailedCode    string
	FailedMessage string
	CreatedAt     string
	LastUpdatedAt string
	ConfirmedAt   string
	AttemptCount  uint32
}

type ForceActionOutcome struct {
	Submitted  bool
	IntentID   string
	Skipped    bool
	SkipReason string
	SkipCode   string
}

type Field[T any] struct {
	Value         T
	Available     bool
	Unimplemented bool
	Error         string
}

type Health struct {
	OK      bool
	Mode    string
	Version string
	ChainID uint64
}

type Round struct {
	LastRound               uint64
	LastError               string
	CurrentRoundInitialized bool
	LastIntentID            string
}

type Reward struct {
	LastRound         uint64
	OrchAddress       string
	Eligible          bool
	EligibilityReason string
	LastRewardRound   uint64
	Active            bool
	LastIntentID      string
	LastEarnedWei     string
	LastError         string
}

type Registry struct {
	URL        string
	Registered bool
}

type Wallet struct {
	Address    string
	BalanceWei string
}

func Dial(ctx context.Context, socketPath string) (*Client, error) {
	conn, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{
		conn: conn,
		rpc:  protocolv1.NewProtocolDaemonClient(conn),
	}, nil
}

func (c *Client) Close() error {
	if cc, ok := c.conn.(*grpc.ClientConn); ok {
		return cc.Close()
	}
	return nil
}

func (c *Client) Snapshot(ctx context.Context) Snapshot {
	var out Snapshot
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	out.Health = unary(callCtx, func(ctx context.Context) (Health, error) {
		resp, err := c.rpc.Health(ctx, &protocolv1.Empty{})
		if err != nil {
			return Health{}, err
		}
		return Health{
			OK:      resp.GetOk(),
			Mode:    resp.GetMode(),
			Version: resp.GetVersion(),
			ChainID: resp.GetChainId(),
		}, nil
	})
	out.Round = unary(callCtx, func(ctx context.Context) (Round, error) {
		resp, err := c.rpc.GetRoundStatus(ctx, &protocolv1.Empty{})
		if err != nil {
			return Round{}, err
		}
		return Round{
			LastRound:               resp.GetLastRound(),
			LastError:               resp.GetLastError(),
			CurrentRoundInitialized: resp.GetCurrentRoundInitialized(),
			LastIntentID:            fmt.Sprintf("%x", resp.GetLastIntentId()),
		}, nil
	})
	out.Reward = unary(callCtx, func(ctx context.Context) (Reward, error) {
		resp, err := c.rpc.GetRewardStatus(ctx, &protocolv1.Empty{})
		if err != nil {
			return Reward{}, err
		}
		return Reward{
			LastRound:         resp.GetLastRound(),
			OrchAddress:       bytesToAddress(resp.GetOrchAddress()),
			Eligible:          resp.GetEligible(),
			EligibilityReason: resp.GetEligibilityReason(),
			LastRewardRound:   resp.GetLastRewardRound(),
			Active:            resp.GetActive(),
			LastIntentID:      fmt.Sprintf("%x", resp.GetLastIntentId()),
			LastEarnedWei:     bytesToBig(resp.GetLastEarnedWei()).String(),
			LastError:         resp.GetLastError(),
		}, nil
	})
	out.ServiceRegistry = unary(callCtx, func(ctx context.Context) (Registry, error) {
		reg, err := c.rpc.IsRegistered(ctx, &protocolv1.Empty{})
		if err != nil {
			return Registry{}, err
		}
		uri, err := c.rpc.GetOnChainServiceURI(ctx, &protocolv1.Empty{})
		if err != nil {
			return Registry{}, err
		}
		return Registry{
			URL:        uri.GetUrl(),
			Registered: reg.GetRegistered(),
		}, nil
	})
	out.AIServiceRegistry = unary(callCtx, func(ctx context.Context) (Registry, error) {
		reg, err := c.rpc.IsAIRegistered(ctx, &protocolv1.Empty{})
		if err != nil {
			return Registry{}, err
		}
		uri, err := c.rpc.GetOnChainAIServiceURI(ctx, &protocolv1.Empty{})
		if err != nil {
			return Registry{}, err
		}
		return Registry{
			URL:        uri.GetUrl(),
			Registered: reg.GetRegistered(),
		}, nil
	})
	out.Wallet = unary(callCtx, func(ctx context.Context) (Wallet, error) {
		resp, err := c.rpc.GetWalletBalance(ctx, &protocolv1.Empty{})
		if err != nil {
			return Wallet{}, err
		}
		return Wallet{
			Address:    bytesToAddress(resp.GetWalletAddress()),
			BalanceWei: bytesToBig(resp.GetBalanceWei()).String(),
		}, nil
	})
	return out
}

func (c *Client) ForceInitializeRound(ctx context.Context) (ForceActionOutcome, error) {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := c.rpc.ForceInitializeRound(callCtx, &protocolv1.Empty{})
	if err != nil {
		return ForceActionOutcome{}, err
	}
	return decodeForceOutcome(resp), nil
}

func (c *Client) ForceRewardCall(ctx context.Context) (ForceActionOutcome, error) {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := c.rpc.ForceRewardCall(callCtx, &protocolv1.Empty{})
	if err != nil {
		return ForceActionOutcome{}, err
	}
	return decodeForceOutcome(resp), nil
}

func (c *Client) SetServiceURI(ctx context.Context, url string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := c.rpc.SetServiceURI(callCtx, &protocolv1.SetServiceURIRequest{Url: url})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", resp.GetId()), nil
}

func (c *Client) SetAIServiceURI(ctx context.Context, url string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := c.rpc.SetAIServiceURI(callCtx, &protocolv1.SetAIServiceURIRequest{Url: url})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", resp.GetId()), nil
}

func (c *Client) GetTxIntent(ctx context.Context, id string) (TxIntent, error) {
	decoded, err := decodeIntentID(id)
	if err != nil {
		return TxIntent{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := c.rpc.GetTxIntent(callCtx, &protocolv1.TxIntentRef{Id: decoded})
	if err != nil {
		return TxIntent{}, err
	}
	return TxIntent{
		ID:            "0x" + fmt.Sprintf("%x", resp.GetId()),
		Kind:          resp.GetKind(),
		Status:        resp.GetStatus(),
		FailedClass:   resp.GetFailedClass(),
		FailedCode:    resp.GetFailedCode(),
		FailedMessage: resp.GetFailedMessage(),
		CreatedAt:     formatUnixNano(resp.GetCreatedAtUnixNano()),
		LastUpdatedAt: formatUnixNano(resp.GetLastUpdatedAtUnixNano()),
		ConfirmedAt:   formatUnixNano(resp.GetConfirmedAtUnixNano()),
		AttemptCount:  resp.GetAttemptCount(),
	}, nil
}

func unary[T any](ctx context.Context, fn func(context.Context) (T, error)) Field[T] {
	value, err := fn(ctx)
	if err == nil {
		return Field[T]{Value: value, Available: true}
	}
	st, ok := status.FromError(err)
	if ok && st.Code() == codes.Unimplemented {
		return Field[T]{Unimplemented: true}
	}
	return Field[T]{Error: err.Error()}
}

func decodeForceOutcome(resp *protocolv1.ForceOutcome) ForceActionOutcome {
	switch outcome := resp.GetOutcome().(type) {
	case *protocolv1.ForceOutcome_Submitted:
		return ForceActionOutcome{
			Submitted: true,
			IntentID:  fmt.Sprintf("%x", outcome.Submitted.GetId()),
		}
	case *protocolv1.ForceOutcome_Skipped:
		return ForceActionOutcome{
			Skipped:    true,
			SkipReason: outcome.Skipped.GetReason(),
			SkipCode:   outcome.Skipped.GetCode().String(),
		}
	default:
		return ForceActionOutcome{
			Skipped:    true,
			SkipReason: "protocol-daemon returned an empty force outcome",
			SkipCode:   protocolv1.SkipReason_CODE_UNSPECIFIED.String(),
		}
	}
}

func bytesToAddress(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return strings.ToLower(ethcommon.BytesToAddress(b).Hex())
}

func bytesToBig(b []byte) *big.Int {
	if len(b) == 0 {
		return new(big.Int)
	}
	return new(big.Int).SetBytes(b)
}

func decodeIntentID(id string) ([]byte, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(id), "0x"))
	if trimmed == "" {
		return nil, fmt.Errorf("empty tx intent id")
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("decode tx intent id: %w", err)
	}
	return decoded, nil
}

func formatUnixNano(v uint64) string {
	if v == 0 {
		return ""
	}
	return time.Unix(0, int64(v)).UTC().Format(time.RFC3339)
}
