package config

import (
	"testing"
	"time"
)

func TestRetryPolicyValidation(t *testing.T) {
	p := DefaultRetryPolicy()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "0s", "-1s", "2m,1m", "200h", "garbage"} {
		var d Durations
		if err := d.Set(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	var d Durations
	if err := d.Set("5s,15s,1m"); err != nil {
		t.Fatal(err)
	}
	if d.String() != "5s,15s,1m0s" {
		t.Fatal(d.String())
	}
	p.Jitter = 1
	if p.Validate() == nil {
		t.Fatal("jitter accepted")
	}
	p = DefaultRetryPolicy()
	p.SourcePollInterval = -time.Second
	if p.Validate() == nil {
		t.Fatal("poll accepted")
	}
}
