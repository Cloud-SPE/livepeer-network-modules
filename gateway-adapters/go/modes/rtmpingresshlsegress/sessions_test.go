package rtmpingresshlsegress

import (
	"testing"
	"time"
)

func TestSessions_RegisterLookup(t *testing.T) {
	s := NewSessions()

	if got := s.Active(); got != 0 {
		t.Fatalf("Active=%d want 0", got)
	}

	if err := s.Register(&Session{
		SessionID:     "sess_xyz",
		StreamKey:     "key_abc",
		RTMPIngestURL: "rtmp://broker.example.com:1935/live/sess_xyz",
		ExpiresAt:     time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if got := s.Active(); got != 1 {
		t.Fatalf("Active=%d want 1", got)
	}

	got := s.Lookup("sess_xyz")
	if got == nil {
		t.Fatal("Lookup returned nil for registered session")
	}
	if got.StreamKey != "key_abc" {
		t.Errorf("StreamKey=%q want key_abc", got.StreamKey)
	}

	if other := s.Lookup("nope"); other != nil {
		t.Errorf("Lookup(nope) returned %+v, want nil", other)
	}
}

func TestSessions_Register_Validation(t *testing.T) {
	s := NewSessions()

	cases := []struct {
		name string
		sess *Session
	}{
		{"nil", nil},
		{"missing id", &Session{RTMPIngestURL: "rtmp://b/live/x"}},
		{"missing url", &Session{SessionID: "a"}},
		{"bad scheme", &Session{SessionID: "a", RTMPIngestURL: "http://b/live/x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := s.Register(c.sess); err == nil {
				t.Errorf("Register(%v) returned nil error; want error", c.sess)
			}
		})
	}
}

func TestSessions_DuplicateRegister(t *testing.T) {
	s := NewSessions()
	if err := s.Register(&Session{
		SessionID:     "dup",
		RTMPIngestURL: "rtmp://b/live/dup",
	}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := s.Register(&Session{
		SessionID:     "dup",
		RTMPIngestURL: "rtmp://b/live/dup",
	}); err == nil {
		t.Fatal("duplicate Register returned nil error; want failure")
	}
}

func TestSessions_RemoveAndMarkStarted(t *testing.T) {
	s := NewSessions()
	_ = s.Register(&Session{
		SessionID:     "a",
		RTMPIngestURL: "rtmp://b/live/a",
	})

	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	s.MarkStarted("a", now)
	got := s.Lookup("a")
	if !got.Started.Equal(now) {
		t.Errorf("Started=%v want %v", got.Started, now)
	}

	// Idempotent.
	later := now.Add(time.Hour)
	s.MarkStarted("a", later)
	if !got.Started.Equal(now) {
		t.Errorf("Started=%v after second MarkStarted; want unchanged %v", got.Started, now)
	}

	s.Remove("a")
	if other := s.Lookup("a"); other != nil {
		t.Errorf("Lookup after Remove returned %+v; want nil", other)
	}
	if got := s.Active(); got != 0 {
		t.Errorf("Active after Remove=%d want 0", got)
	}
}

func TestSplitPublishingName(t *testing.T) {
	cases := []struct {
		in       string
		wantID   string
		wantKey  string
		wantOK   bool
	}{
		{"sess_xyz/key_abc", "sess_xyz", "key_abc", true},
		{"/sess_xyz/key_abc", "sess_xyz", "key_abc", true},
		{"sess_xyz", "sess_xyz", "", false},
		{"", "", "", false},
		{"sess/", "sess/", "", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			id, key, ok := splitPublishingName(c.in)
			if id != c.wantID || key != c.wantKey || ok != c.wantOK {
				t.Errorf("splitPublishingName(%q) = (%q, %q, %v); want (%q, %q, %v)",
					c.in, id, key, ok, c.wantID, c.wantKey, c.wantOK)
			}
		})
	}
}

func TestSplitRTMPPath(t *testing.T) {
	cases := []struct {
		in       string
		wantApp  string
		wantName string
	}{
		{"/live/sess_xyz", "live", "sess_xyz"},
		{"live/sess_xyz", "live", "sess_xyz"},
		{"/live", "live", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			app, name := splitRTMPPath(c.in)
			if app != c.wantApp || name != c.wantName {
				t.Errorf("splitRTMPPath(%q) = (%q, %q); want (%q, %q)",
					c.in, app, name, c.wantApp, c.wantName)
			}
		})
	}
}
