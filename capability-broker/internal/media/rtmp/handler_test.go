package rtmp

import "testing"

func TestSplitPublishingName(t *testing.T) {
	cases := []struct {
		in       string
		wantSess string
		wantKey  string
		wantOk   bool
	}{
		{"sess_abc/streamkeyXYZ", "sess_abc", "streamkeyXYZ", true},
		{"/sess_abc/streamkeyXYZ", "sess_abc", "streamkeyXYZ", true},
		{"sess_abc", "sess_abc", "", false},
		{"", "", "", false},
		{"/", "", "", false},
		{"sess/", "sess/", "", false},
		{"/sess/", "sess/", "", false},
		{"sess_a/key/extra", "sess_a", "key/extra", true},
	}
	for _, c := range cases {
		gotSess, gotKey, ok := splitPublishingName(c.in)
		if ok != c.wantOk || gotSess != c.wantSess || gotKey != c.wantKey {
			t.Errorf("splitPublishingName(%q) = (%q, %q, %v); want (%q, %q, %v)",
				c.in, gotSess, gotKey, ok, c.wantSess, c.wantKey, c.wantOk)
		}
	}
}

func TestRedactKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"abcdef", "[short-key]"},
		{"", "[short-key]"},
		{"short", "[short-key]"},
		{"abcdefghi", "abcdef..."},
	}
	for _, c := range cases {
		if got := redactKey(c.in); got != c.want {
			t.Errorf("redactKey(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}
