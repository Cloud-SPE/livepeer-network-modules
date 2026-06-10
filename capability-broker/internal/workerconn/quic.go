package workerconn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/quic-go/quic-go"
)

const QUICNextProto = "livepeer-pool-worker/1"

type QUICSessionForwarder struct {
	conn *quic.Conn
	done chan struct{}
}

func NewQUICSessionForwarder(conn *quic.Conn) *QUICSessionForwarder {
	q := &QUICSessionForwarder{conn: conn, done: make(chan struct{})}
	go func() {
		<-conn.Context().Done()
		close(q.done)
	}()
	return q
}

func (q *QUICSessionForwarder) Done() <-chan struct{} {
	return q.done
}

func (q *QUICSessionForwarder) Close() error {
	return q.conn.CloseWithError(0, "closed")
}

func (q *QUICSessionForwarder) Forward(ctx context.Context, req backend.ForwardRequest) (*http.Response, error) {
	stream, err := q.conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	msg := TunnelMessage{
		Type:    MessageTypeRequest,
		ID:      fmt.Sprintf("req-%d", time.Now().UnixNano()),
		Method:  defaultMethod(req.Method),
		URL:     req.URL,
		Headers: headerMap(req.Headers),
	}
	if err := WriteQUICFrameHeader(stream, msg); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if req.Body != nil {
		if _, err := io.Copy(stream, req.Body); err != nil {
			_ = stream.Close()
			return nil, err
		}
	}
	if err := stream.Close(); err != nil {
		return nil, err
	}
	resp, err := ReadQUICFrameHeader(stream)
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("worker request failed: %s", resp.Error)
	}
	return &http.Response{
		StatusCode: resp.StatusCode,
		Header:     http.Header(resp.Headers),
		Body:       &quicResponseBody{Stream: stream},
	}, nil
}

func ReadQUICRegister(ctx context.Context, conn *quic.Conn) (TunnelMessage, error) {
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		return TunnelMessage{}, err
	}
	defer func() { _ = stream.Close() }()
	var msg TunnelMessage
	if err := json.NewDecoder(stream).Decode(&msg); err != nil {
		return TunnelMessage{}, err
	}
	if msg.Type != MessageTypeRegister {
		return TunnelMessage{}, fmt.Errorf("expected register message, got %q", msg.Type)
	}
	if err := json.NewEncoder(stream).Encode(TunnelMessage{Type: MessageTypeResponse, ID: msg.ID, StatusCode: http.StatusOK}); err != nil {
		return TunnelMessage{}, err
	}
	return msg, nil
}

func WriteQUICFrameHeader(w io.Writer, msg TunnelMessage) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(raw) > 16*1024*1024 {
		return fmt.Errorf("quic frame header too large")
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(raw)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func ReadQUICFrameHeader(r io.Reader) (TunnelMessage, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return TunnelMessage{}, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > 16*1024*1024 {
		return TunnelMessage{}, fmt.Errorf("invalid quic frame header length %d", n)
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		return TunnelMessage{}, err
	}
	var msg TunnelMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return TunnelMessage{}, err
	}
	return msg, nil
}

type quicResponseBody struct {
	*quic.Stream
}

func (b *quicResponseBody) Close() error {
	return b.Stream.Close()
}

func RegisterBackendIDs(msg TunnelMessage) []string {
	raw := ""
	if values := msg.Headers["Backend-Ids"]; len(values) > 0 {
		raw = values[0]
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func RegisterAuthorization(msg TunnelMessage) string {
	if values := msg.Headers["Authorization"]; len(values) > 0 {
		return strings.TrimSpace(values[0])
	}
	return ""
}

func ServerTLSConfig() (*tls.Config, error) {
	cert, err := selfSignedCertificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{QUICNextProto}}, nil
}

func ClientTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, NextProtos: []string{QUICNextProto}}
}

func selfSignedCertificate() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return tls.X509KeyPair(certPEM, keyPEM)
}

var _ backend.Forwarder = (*QUICSessionForwarder)(nil)
