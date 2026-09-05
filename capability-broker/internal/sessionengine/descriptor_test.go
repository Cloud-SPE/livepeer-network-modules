package sessionengine

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseDescriptorHappyPath(t *testing.T) {
	d, err := ParseDescriptor(validRuntime(), "sfu-room/v1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Schema != "sfu-room/v1" || len(d.Grants) != 1 || d.Grants[0].ID != "g1" {
		t.Fatalf("parsed wrong: %+v", d)
	}
}

func TestParseDescriptorRejections(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"unknown top-level key", `{"schema":"sfu-room/v1","public":{},"extra_key":{}}`, "unknown top-level key"},
		{"missing public", `{"schema":"sfu-room/v1"}`, "public part must be a JSON object"},
		{"public not object", `{"schema":"sfu-room/v1","public":[1]}`, "public part must be a JSON object"},
		{"private not object", `{"schema":"sfu-room/v1","public":{},"private":"x"}`, "private part must be a JSON object"},
		{"bad schema tag", `{"schema":"SFU@v1","public":{}}`, "does not match"},
		{"schema mismatch", `{"schema":"rtmp-hls/v1","public":{}}`, "declared descriptor_schema"},
		{"grant missing fields", `{"schema":"sfu-room/v1","public":{},"grants":[{"id":"g1"}]}`, "missing required field"},
		{"duplicate grant ids", `{"schema":"sfu-room/v1","public":{},"grants":[
			{"id":"g1","operations":["a"],"secret":"s","expires_at":"2030-01-01T00:00:00Z"},
			{"id":"g1","operations":["b"],"secret":"s2","expires_at":"2030-01-01T00:00:00Z"}]}`, "duplicated"},
		{"not an object", `[1,2]`, "not a JSON object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDescriptor(json.RawMessage(tc.raw), "sfu-room/v1", 0)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestParseDescriptorSizeCap(t *testing.T) {
	big := `{"schema":"sfu-room/v1","public":{"pad":"` + strings.Repeat("x", DefaultDescriptorMaxBytes) + `"}}`
	if _, err := ParseDescriptor(json.RawMessage(big), "sfu-room/v1", 0); err == nil {
		t.Fatal("oversize descriptor accepted")
	}
	if _, err := ParseDescriptor(validRuntime(), "sfu-room/v1", 64); err == nil {
		t.Fatal("offering-lowered cap not enforced")
	}
}

func TestParseDescriptorGrantCap(t *testing.T) {
	grants := make([]string, 0, maxGrants+1)
	for i := 0; i < maxGrants+1; i++ {
		grants = append(grants, `{"id":"g`+string(rune('a'+i))+`","operations":["op"],"secret":"s","expires_at":"2030-01-01T00:00:00Z"}`)
	}
	raw := `{"schema":"sfu-room/v1","public":{},"grants":[` + strings.Join(grants, ",") + `]}`
	if _, err := ParseDescriptor(json.RawMessage(raw), "sfu-room/v1", 0); err == nil {
		t.Fatal("grant cap not enforced")
	}
}
