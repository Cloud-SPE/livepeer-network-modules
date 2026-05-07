package rtmpingresshlsegress

import (
	"strings"
	"testing"
	"time"
)

func TestStore_AddLookupRemove(t *testing.T) {
	s := NewStore()
	rec := &SessionRecord{
		SessionID: "sess_a",
		StreamKey: "key_a",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := s.Add(rec); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(rec); err == nil {
		t.Fatalf("Add duplicate: want error")
	}

	got, ok := s.Lookup("sess_a", "key_a")
	if !ok || got.SessionID != "sess_a" {
		t.Fatalf("Lookup match: ok=%v rec=%+v", ok, got)
	}

	if _, ok := s.Lookup("sess_a", "wrong"); ok {
		t.Fatalf("Lookup wrong key: want miss")
	}
	if _, ok := s.Lookup("missing", "key_a"); ok {
		t.Fatalf("Lookup missing session: want miss")
	}

	s.Remove("sess_a")
	if _, ok := s.Lookup("sess_a", "key_a"); ok {
		t.Fatalf("Lookup after Remove: want miss")
	}
}

func TestStore_MarkPublishingTouch(t *testing.T) {
	s := NewStore()
	rec := &SessionRecord{SessionID: "sess_b", StreamKey: "k"}
	_ = s.Add(rec)

	now := time.Unix(100, 0)
	prior, ok := s.MarkPublishing("sess_b", now)
	if !ok || prior {
		t.Fatalf("first MarkPublishing: prior=%v ok=%v", prior, ok)
	}
	prior, ok = s.MarkPublishing("sess_b", now)
	if !ok || !prior {
		t.Fatalf("second MarkPublishing: prior=%v ok=%v", prior, ok)
	}

	tStamp := time.Unix(200, 0)
	s.Touch("sess_b", tStamp)
	got := s.Get("sess_b")
	if !got.LastPacketAt.Equal(tStamp) {
		t.Fatalf("Touch: LastPacketAt=%v want=%v", got.LastPacketAt, tStamp)
	}

	if _, ok := s.MarkPublishing("missing", now); ok {
		t.Fatalf("MarkPublishing missing: want ok=false")
	}
}

func TestGenerateStreamKey(t *testing.T) {
	k1, err := generateStreamKey()
	if err != nil {
		t.Fatalf("generateStreamKey: %v", err)
	}
	if len(k1) < 32 {
		t.Fatalf("stream key length=%d want>=32", len(k1))
	}
	k2, _ := generateStreamKey()
	if k1 == k2 {
		t.Fatalf("stream key collision: %s", k1)
	}
	if strings.ContainsAny(k1, "+/=") {
		t.Fatalf("stream key contains non-url-safe chars: %s", k1)
	}
}
