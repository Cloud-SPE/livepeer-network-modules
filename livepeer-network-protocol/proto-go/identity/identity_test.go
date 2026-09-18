package identity

import "testing"

func TestBrokerURI(t *testing.T) {
	for raw, want := range map[string]string{"HTTPS://EXAMPLE.COM:443/": "https://example.com", "http://EXAMPLE.COM:80/base/": "http://example.com/base", "https://[::1]:443/": "https://[::1]", "https://xn--bcher-kva.example": "https://xn--bcher-kva.example"} {
		got, err := BrokerURI(raw)
		if err != nil || got != want {
			t.Errorf("%s: %q %v", raw, got, err)
		}
	}
	for _, raw := range []string{"https://a/?x=1", "https://a/?", "https://a/#", "https://u:p@a", "https://a/a/../b", "https://a//b", "https://a/%61", "https://bücher.example", "https://a.", " https://a", "https://a:0"} {
		if _, err := BrokerURI(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if SameBrokerURI("https://a/base", "https://a/BASE") {
		t.Fatal("base path case must matter")
	}
}
