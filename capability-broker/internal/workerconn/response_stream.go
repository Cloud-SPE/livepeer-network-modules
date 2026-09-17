package workerconn

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// responseStream is read by exactly one HTTP consumer. Close may race with Read.
// Trailers are mutated only by Read immediately before it returns EOF.
type responseStream struct {
	ctx       context.Context
	cancel    context.CancelFunc
	session   *SessionForwarder
	id        string
	messages  <-chan TunnelMessage
	trailers  http.Header
	remaining []byte
	seq       uint64
	ack       bool
	terminal  error
	once      sync.Once
	stopped   chan struct{}
}

func (b *responseStream) stop() {
	b.once.Do(func() { b.session.unregisterPending(b.id); close(b.stopped); b.cancel() })
}
func (b *responseStream) Close() error {
	select {
	case <-b.stopped:
		return nil
	default:
	}
	b.stop()
	return b.session.SendMessage(TunnelMessage{Type: "request_cancel", ID: b.id})
}
func (b *responseStream) fail(err error) (int, error) {
	b.terminal = err
	_ = b.Close()
	return 0, err
}
func (b *responseStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.terminal != nil {
		return 0, b.terminal
	}
	for {
		if err := b.ctx.Err(); err != nil {
			return b.fail(err)
		}
		select {
		case <-b.stopped:
			return 0, io.ErrClosedPipe
		default:
		}
		if len(b.remaining) > 0 {
			n := copy(p, b.remaining)
			b.remaining = b.remaining[n:]
			return n, nil
		}
		if b.ack {
			// A terminal frame may already be queued when the connection closes.
			// No credit is needed then; still consume the received terminal proof.
			select {
			case <-b.session.done:
			default:
				if err := b.session.SendMessage(TunnelMessage{Type: "response_ack", ID: b.id, Seq: b.seq}); err != nil {
					// The reader will distinguish a queued end from interruption.
					_ = b.session.Close()
				}
			}
			b.ack = false
		}
		msg, err := b.session.receive(b.ctx, b.messages)
		if err != nil {
			return b.fail(err)
		}
		select {
		case <-b.stopped:
			return 0, io.ErrClosedPipe
		default:
		}

		switch msg.Type {
		case "response_chunk":
			if msg.Seq != b.seq+1 || len(msg.BodyBase64) > 43692 {
				return b.fail(fmt.Errorf("invalid response chunk sequence or size"))
			}
			data, err := base64.StdEncoding.DecodeString(msg.BodyBase64)
			if err != nil || len(data) == 0 || len(data) > 32768 {
				return b.fail(fmt.Errorf("invalid response chunk"))
			}
			b.seq = msg.Seq
			b.remaining = data
			b.ack = true
		case "response_end":
			if msg.Seq != b.seq {
				return b.fail(fmt.Errorf("invalid response completion sequence"))
			}
			for k, v := range msg.Trailers {
				b.trailers[http.CanonicalHeaderKey(k)] = v
			}
			b.terminal = io.EOF
			b.stop()
			return 0, io.EOF
		case "response_error":
			return b.fail(fmt.Errorf("worker response failed: %s", msg.Error))
		default:
			return b.fail(fmt.Errorf("unexpected response frame %s", msg.Type))
		}
	}
}
