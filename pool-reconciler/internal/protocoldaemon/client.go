package protocoldaemon

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"time"

	protocolv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/protocol/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Config struct {
	SocketPath string
	Timeout    time.Duration
}

type Client struct {
	socketPath string
	timeout    time.Duration
}

type RoundStatus struct {
	LastRound               uint64 `json:"last_round"`
	LastIntentIDHex         string `json:"last_intent_id_hex,omitempty"`
	LastError               string `json:"last_error,omitempty"`
	CurrentRoundInitialized bool   `json:"current_round_initialized"`
}

type RoundEvent struct {
	Number       uint64 `json:"number"`
	StartBlock   uint64 `json:"start_block"`
	L1StartBlock uint64 `json:"l1_start_block"`
	Length       uint64 `json:"length"`
	Initialized  bool   `json:"initialized"`
	BlockHashHex string `json:"block_hash_hex,omitempty"`
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.SocketPath == "" {
		return nil, fmt.Errorf("protocol daemon socket path is required")
	}
	if cfg.Timeout < 0 {
		return nil, fmt.Errorf("protocol daemon timeout must be >= 0")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 1500 * time.Millisecond
	}
	return &Client{
		socketPath: cfg.SocketPath,
		timeout:    cfg.Timeout,
	}, nil
}

func (c *Client) GetRoundStatus(ctx context.Context) (RoundStatus, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return RoundStatus{}, err
	}
	defer conn.Close()

	reqCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	res, err := protocolv1.NewProtocolDaemonClient(conn).GetRoundStatus(reqCtx, &protocolv1.Empty{})
	if err != nil {
		return RoundStatus{}, fmt.Errorf("protocol daemon GetRoundStatus: %w", err)
	}
	return RoundStatus{
		LastRound:               res.GetLastRound(),
		LastIntentIDHex:         hex.EncodeToString(res.GetLastIntentId()),
		LastError:               res.GetLastError(),
		CurrentRoundInitialized: res.GetCurrentRoundInitialized(),
	}, nil
}

func (c *Client) StreamRoundEvents(ctx context.Context, fn func(RoundEvent) error) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	reqCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	stream, err := protocolv1.NewProtocolDaemonClient(conn).StreamRoundEvents(reqCtx, &protocolv1.Empty{})
	if err != nil {
		return fmt.Errorf("protocol daemon StreamRoundEvents: %w", err)
	}
	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := fn(RoundEvent{
			Number:       msg.GetNumber(),
			StartBlock:   msg.GetStartBlock(),
			L1StartBlock: msg.GetL1StartBlock(),
			Length:       msg.GetLength(),
			Initialized:  msg.GetInitialized(),
			BlockHashHex: hex.EncodeToString(msg.GetBlockHash()),
		}); err != nil {
			return err
		}
	}
}

func (c *Client) dial(ctx context.Context) (*grpc.ClientConn, error) {
	conn, err := grpc.DialContext(
		ctx,
		"unix://"+c.socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", c.socketPath)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("dial protocol daemon: %w", err)
	}
	return conn, nil
}

func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
