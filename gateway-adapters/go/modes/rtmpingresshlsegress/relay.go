package rtmpingresshlsegress

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

// relay is the per-customer-connection forwarder. It owns the outbound
// RTMP client connection to the broker's rtmp_ingest_url and pumps the
// inbound publish stream through. The relay is created lazily by the
// connHandler on first OnPublish accept.
type relay struct {
	sessionID string
	upstream  *url.URL
	streamKey string

	mu       sync.Mutex
	conn     *rtmp.ClientConn
	stream   *rtmp.Stream
	closed   atomic.Bool
	bytesOut atomic.Uint64
}

// chunkStreamIDs follow common RTMP convention: control on 2,
// command/data on 3, video on 4, audio on 6. yutopp/go-rtmp does not
// export named constants for these; the spec wants any unused
// chunk-stream ID, so the convention is what matters for interop.
const (
	chunkStreamIDVideo = 4
	chunkStreamIDAudio = 6
)

// dial opens an RTMP client connection to the broker, performs the
// connect + createStream + publish handshake, and returns the relay.
func dial(sess *Session) (*relay, error) {
	u, err := url.Parse(sess.RTMPIngestURL)
	if err != nil {
		return nil, fmt.Errorf("parse rtmp_ingest_url: %w", err)
	}
	if u.Scheme != "rtmp" && u.Scheme != "rtmps" {
		return nil, errors.New("rtmp_ingest_url scheme must be rtmp or rtmps")
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host = net.JoinHostPort(u.Host, defaultPort(u.Scheme))
	}

	conn, err := rtmp.Dial(u.Scheme, host, &rtmp.ConnConfig{})
	if err != nil {
		return nil, fmt.Errorf("rtmp dial broker %s: %w", host, err)
	}

	app, streamName := splitRTMPPath(u.Path)
	if err := conn.Connect(&rtmpmsg.NetConnectionConnect{
		Command: rtmpmsg.NetConnectionConnectCommand{
			App:      app,
			TCURL:    trimPath(u),
			FlashVer: "FMLE/3.0 (compatible; livepeer-gateway)",
		},
	}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rtmp connect: %w", err)
	}

	stream, err := conn.CreateStream(&rtmpmsg.NetConnectionCreateStream{}, 1024)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rtmp createStream: %w", err)
	}

	publishName := streamName
	if publishName == "" {
		publishName = sess.SessionID
	}
	if err := stream.Publish(&rtmpmsg.NetStreamPublish{
		PublishingName: publishName,
		PublishingType: "live",
	}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rtmp publish: %w", err)
	}

	return &relay{
		sessionID: sess.SessionID,
		upstream:  u,
		streamKey: sess.StreamKey,
		conn:      conn,
		stream:    stream,
	}, nil
}

// writeAudio forwards an inbound audio tag.
func (r *relay) writeAudio(timestamp uint32, payload io.Reader) error {
	return r.writeMedia(timestamp, payload, true)
}

// writeVideo forwards an inbound video tag.
func (r *relay) writeVideo(timestamp uint32, payload io.Reader) error {
	return r.writeMedia(timestamp, payload, false)
}

func (r *relay) writeMedia(timestamp uint32, payload io.Reader, isAudio bool) error {
	if r.closed.Load() {
		return io.ErrClosedPipe
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stream == nil {
		return io.ErrClosedPipe
	}

	buf, err := io.ReadAll(payload)
	if err != nil {
		return err
	}
	r.bytesOut.Add(uint64(len(buf)))

	if isAudio {
		return r.stream.Write(chunkStreamIDAudio, timestamp, &rtmpmsg.AudioMessage{
			Payload: bytesReader(buf),
		})
	}
	return r.stream.Write(chunkStreamIDVideo, timestamp, &rtmpmsg.VideoMessage{
		Payload: bytesReader(buf),
	})
}

// Close terminates the outbound RTMP client. Safe to call multiple
// times.
func (r *relay) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil {
		err := r.conn.Close()
		r.conn = nil
		r.stream = nil
		return err
	}
	return nil
}

// BytesOut returns the cumulative count of media-payload bytes
// forwarded to the broker.
func (r *relay) BytesOut() uint64 { return r.bytesOut.Load() }

// splitRTMPPath separates the app from the stream-name in an
// rtmp:// URL path. For `rtmp://broker/live/sess_xyz`, app is `live`
// and streamName is `sess_xyz`.
func splitRTMPPath(p string) (app, streamName string) {
	cleaned := path.Clean("/" + strings.TrimPrefix(p, "/"))
	parts := strings.SplitN(strings.TrimPrefix(cleaned, "/"), "/", 2)
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return parts[0], ""
	default:
		return parts[0], parts[1]
	}
}

// trimPath returns the URL up to the app segment, suitable for the
// `tcUrl` parameter of an RTMP connect command.
func trimPath(u *url.URL) string {
	app, _ := splitRTMPPath(u.Path)
	if app == "" {
		return u.Scheme + "://" + u.Host
	}
	host := u.Host
	if u.Port() == "" {
		host = u.Hostname() + ":" + defaultPort(u.Scheme)
	}
	return u.Scheme + "://" + host + "/" + app
}

func defaultPort(scheme string) string {
	if scheme == "rtmps" {
		return "443"
	}
	return "1935"
}

// bytesReader returns an io.Reader over a byte slice with no extra
// allocation per write.
func bytesReader(p []byte) io.Reader { return &sliceReader{p: p} }

type sliceReader struct {
	p []byte
	o int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.o >= len(r.p) {
		return 0, io.EOF
	}
	n := copy(p, r.p[r.o:])
	r.o += n
	return n, nil
}

