package rtmp

import (
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

// connHandler implements rtmp.Handler for one accepted connection.
//
// The publishing-name carries `<session_id>/<stream_key>` per the
// path-based URL convention shared with mux/twitch/youtube. The handler
// splits the parts, asks the listener's SessionLookup to validate the
// stream key, and on success starts piping audio/video tag bytes into
// the per-session sink.
type connHandler struct {
	rtmp.DefaultHandler

	listener *Listener
	remote   string

	mu        sync.Mutex
	sink      Sink
	sessionID string
	streamKey string
	closed    bool
}

func (h *connHandler) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	publishingName := strings.TrimSpace(cmd.PublishingName)
	if publishingName == "" {
		return errors.New("rtmp: empty publishing name")
	}

	if l := h.listener; l != nil {
		if cap := l.cfg.MaxConcurrent; cap > 0 && l.active.Load() >= int64(cap) {
			log.Printf("rtmp: rejected publish — max concurrent (%d) reached", cap)
			return errors.New("rtmp: max concurrent streams reached")
		}
	}

	sessionID, streamKey, ok := splitPublishingName(publishingName)
	if !ok {
		if h.listener.cfg.RequireStreamKey {
			log.Printf("rtmp: rejected publish — malformed publishing name (key prefix=%s)", redactKey(publishingName))
			return errors.New("rtmp: publishing name must be <session_id>/<stream_key>")
		}
		sessionID = publishingName
	}

	sink, accepted, replaceFirst := h.listener.lookup.LookupAndAccept(sessionID, streamKey)
	if !accepted {
		log.Printf("rtmp: rejected publish — session/key mismatch (session=%s key_prefix=%s)",
			sessionID, redactKey(streamKey))
		return errors.New("rtmp: session not found or stream key mismatch")
	}

	prior := h.listener.trackPublisher(sessionID, h)
	if prior != nil {
		switch h.listener.cfg.DuplicatePolicy {
		case DuplicateReplace:
			if !replaceFirst {
				log.Printf("rtmp: replacing prior publisher (session=%s)", sessionID)
			}
			prior.evict()
		default:
			h.listener.untrackPublisher(sessionID, h)
			_ = sink.Close()
			log.Printf("rtmp: rejected duplicate publish (session=%s)", sessionID)
			return errors.New("rtmp: duplicate publish for session")
		}
	}

	h.mu.Lock()
	h.sink = sink
	h.sessionID = sessionID
	h.streamKey = streamKey
	h.mu.Unlock()
	log.Printf("rtmp: session_open session=%s key_prefix=%s remote=%s",
		sessionID, redactKey(streamKey), h.remote)
	return nil
}

func (h *connHandler) OnAudio(_ uint32, payload io.Reader) error {
	return h.pipe(payload)
}

func (h *connHandler) OnVideo(_ uint32, payload io.Reader) error {
	return h.pipe(payload)
}

func (h *connHandler) pipe(payload io.Reader) error {
	h.mu.Lock()
	sink := h.sink
	closed := h.closed
	h.mu.Unlock()
	if sink == nil || closed {
		return nil
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := payload.Read(buf)
		if n > 0 {
			if _, werr := sink.WriteFLV(buf[:n]); werr != nil {
				return werr
			}
			sink.Touch(time.Now())
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (h *connHandler) OnClose() {
	h.evict()
}

// evict closes the per-session sink and untracks the publisher. Safe
// to call multiple times.
func (h *connHandler) evict() {
	h.mu.Lock()
	sink := h.sink
	sessionID := h.sessionID
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.sink = nil
	h.mu.Unlock()
	if sink != nil {
		_ = sink.Close()
	}
	if sessionID != "" {
		h.listener.untrackPublisher(sessionID, h)
		log.Printf("rtmp: session_closed session=%s remote=%s", sessionID, h.remote)
	}
}

// splitPublishingName parses the path-based RTMP publishing name.
// Returns (session_id, stream_key, true) on a `<id>/<key>` shape;
// (raw, "", false) otherwise.
func splitPublishingName(name string) (string, string, bool) {
	name = strings.TrimPrefix(name, "/")
	idx := strings.IndexByte(name, '/')
	if idx <= 0 || idx == len(name)-1 {
		return name, "", false
	}
	return name[:idx], name[idx+1:], true
}

// redactKey returns a short safe-to-log prefix of a stream key.
func redactKey(k string) string {
	if len(k) <= 6 {
		return "[short-key]"
	}
	return k[:6] + "..."
}
