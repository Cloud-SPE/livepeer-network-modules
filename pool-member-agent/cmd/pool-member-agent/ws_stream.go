package main

import (
	"context"
	"fmt"
	"io"
	"sync"
)

const responseWindow = 8
const responseChunkSize = 32 * 1024

type wsExchange struct {
	cancel             context.CancelFunc
	credit             chan struct{}
	sent, acknowledged uint64 // protected by wsExchanges.mu
}
type wsExchanges struct {
	ctx    context.Context
	send   func(tunnelMessage) error
	mu     sync.Mutex
	active map[string]*wsExchange
}

func newWSExchanges(ctx context.Context, send func(tunnelMessage) error) *wsExchanges {
	return &wsExchanges{ctx: ctx, send: send, active: make(map[string]*wsExchange)}
}
func (x *wsExchanges) close() {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, e := range x.active {
		e.cancel()
	}
}
func (x *wsExchanges) control(msg tunnelMessage) {
	x.mu.Lock()
	defer x.mu.Unlock()
	e := x.active[msg.ID]
	if e == nil {
		return
	}
	if msg.Type == "request_cancel" {
		e.cancel()
		return
	}
	// Cumulative/replayed or unsolicited credit is never accepted.
	if msg.Seq != e.acknowledged+1 || msg.Seq > e.sent {
		e.cancel()
		return
	}
	e.acknowledged = msg.Seq
	select {
	case e.credit <- struct{}{}:
	default:
		e.cancel()
	}
}
func (x *wsExchanges) start(routes runnerRoutes, msg tunnelMessage) error {
	x.mu.Lock()
	if msg.ID == "" || x.active[msg.ID] != nil {
		x.mu.Unlock()
		return fmt.Errorf("missing or duplicate tunnel request ID")
	}
	ctx, cancel := context.WithCancel(x.ctx)
	e := &wsExchange{cancel: cancel, credit: make(chan struct{}, responseWindow)}
	for i := 0; i < responseWindow; i++ {
		e.credit <- struct{}{}
	}
	x.active[msg.ID] = e
	x.mu.Unlock()
	go func() {
		defer cancel()
		defer func() { x.mu.Lock(); delete(x.active, msg.ID); x.mu.Unlock() }()
		if msg.ResponseMode == "" {
			_ = x.send(forwardTunnelRequest(ctx, routes, msg))
			return
		}
		if msg.ResponseMode != "chunks-v1" {
			_ = x.send(tunnelMessage{Type: "response_error", ID: msg.ID, Error: "unsupported response mode"})
			return
		}
		if err := x.forward(ctx, routes, msg, e); err != nil {
			// Error text does not expose runner URLs, credentials or request payloads.
			_ = x.send(tunnelMessage{Type: "response_error", ID: msg.ID, Error: "runner response interrupted"})
		}
	}()
	return nil
}
func (x *wsExchanges) forward(ctx context.Context, routes runnerRoutes, msg tunnelMessage, e *wsExchange) error {
	resp, err := openTunnelResponse(ctx, routes, msg)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := x.send(tunnelMessage{Type: "response_start", ID: msg.ID, StatusCode: resp.StatusCode, Headers: map[string][]string(resp.Header)}); err != nil {
		return err
	}
	buf := make([]byte, responseChunkSize)
	for {
		// Reserve credit before reading from the local server, bounding memory and
		// allowing TCP backpressure to reach it when the gateway stops consuming.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.credit:
		}
		n, err := resp.Body.Read(buf)
		if n > 0 {
			x.mu.Lock()
			e.sent++
			seq := e.sent
			x.mu.Unlock()
			if sendErr := x.send(tunnelMessage{Type: "response_chunk", ID: msg.ID, Seq: seq, BodyBase64: base64Encode(buf[:n])}); sendErr != nil {
				return sendErr
			}
		} else {
			// A zero-byte read did not use the reserved chunk slot.
			e.credit <- struct{}{}
		}
		if err == io.EOF {
			x.mu.Lock()
			seq := e.sent
			x.mu.Unlock()
			return x.send(tunnelMessage{Type: "response_end", ID: msg.ID, Seq: seq, Trailers: map[string][]string(resp.Trailer)})
		}
		if err != nil {
			return err
		}
	}
}
