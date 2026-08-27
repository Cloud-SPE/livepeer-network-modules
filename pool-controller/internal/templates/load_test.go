package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// validTemplate is the smallest template the loader accepts. Tests bend
// one field at a time from here so a failure names the rule it broke.
func validTemplate(id, capability, offeringID string) string {
	return "id: " + id + "\n" +
		"capability: " + capability + "\n" +
		"offering_id: " + offeringID + "\n" +
		"protocol: paid-job/v1\n" +
		"price_default:\n  amount_wei: \"1\"\n  per_units: 1\n" +
		"stacking:\n  primary: true\n"
}

func writeTemplates(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// A pool that has not adopted a catalog yet must still boot: the loader
// is not allowed to be the thing that stops the controller starting.
func TestLoadMissingDirectoryIsEmptyNotAnError(t *testing.T) {
	catalog, err := Load(filepath.Join(t.TempDir(), "no-such-dir"))
	if err != nil {
		t.Fatalf("Load(missing) error = %v, want nil", err)
	}
	if catalog == nil || catalog.Len() != 0 || len(catalog.All()) != 0 {
		t.Fatalf("Load(missing) = %#v, want an empty catalog", catalog)
	}
	if _, ok := catalog.Get("anything"); ok {
		t.Fatal("empty catalog claims to hold a template")
	}
}

// The order is the loader's, not the filesystem's: callers publish the
// catalog and compare it against a broker, so it has to be stable
// whatever the directory listing happens to say.
func TestLoadOrdersByID(t *testing.T) {
	dir := writeTemplates(t, map[string]string{
		"z-first.yaml":  validTemplate("chat-b", "openai:chat-completions", "b"),
		"a-second.yaml": validTemplate("chat-a", "openai:chat-completions", "a"),
		"m-third.yml":   validTemplate("chat-c", "openai:chat-completions", "c"),
		"notes.txt":     "not a template",
	})
	catalog, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if catalog.Len() != 3 {
		t.Fatalf("Len() = %d, want 3 (only .yaml/.yml files are templates)", catalog.Len())
	}
	var got []string
	for _, tmpl := range catalog.All() {
		got = append(got, tmpl.ID)
	}
	if strings.Join(got, ",") != "chat-a,chat-b,chat-c" {
		t.Fatalf("All() ids = %v, want them sorted by id", got)
	}
	if tmpl, ok := catalog.Get("chat-b"); !ok || tmpl.Capability != "openai:chat-completions" {
		t.Fatalf("Get(chat-b) = %+v, %v", tmpl, ok)
	}
}

// KnownFields(true) is what turns a misspelled key into a boot failure.
// Without it the template would load with that field silently unset —
// a runner quietly missing its image is far worse than a refusal.
func TestLoadRejectsUnknownKey(t *testing.T) {
	dir := writeTemplates(t, map[string]string{
		"typo.yaml": validTemplate("chat-a", "openai:chat-completions", "a") + "prioirty: 5\n",
	})
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() accepted an unknown key; a typo must fail the boot")
	}
	if !strings.Contains(err.Error(), "prioirty") {
		t.Fatalf("Load() error = %v, want it to name the unknown field", err)
	}
}

func TestLoadRejectsDuplicateID(t *testing.T) {
	dir := writeTemplates(t, map[string]string{
		"one.yaml": validTemplate("chat-a", "openai:chat-completions", "a"),
		"two.yaml": validTemplate("chat-a", "openai:chat-completions", "b"),
	})
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "already declared") {
		t.Fatalf("Load() error = %v, want a duplicate-id rejection", err)
	}
}

// Two templates selling the same (capability, offering) would race to
// define one offer, and the broker admits exactly one — so the conflict
// has to surface here rather than as an arbitrary winner at push time.
func TestLoadRejectsDuplicateOffering(t *testing.T) {
	dir := writeTemplates(t, map[string]string{
		"one.yaml": validTemplate("chat-a", "openai:chat-completions", "default"),
		"two.yaml": validTemplate("chat-b", "openai:chat-completions", "default"),
	})
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "already sold by") {
		t.Fatalf("Load() error = %v, want a duplicate-offering rejection", err)
	}
}

func TestValidateRejects(t *testing.T) {
	base := func() Template {
		return Template{
			ID:           "chat-a",
			Capability:   "openai:chat-completions",
			OfferingID:   "default",
			Protocol:     "paid-job/v1",
			PriceDefault: Price{AmountWei: "1", PerUnits: 1},
			Stacking:     Stacking{Primary: true},
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("the baseline template must be valid, got %v", err)
	}

	for _, tc := range []struct {
		name    string
		mutate  func(*Template)
		wantErr string
	}{
		{
			// Ids name files, URL path segments and DB keys, so the
			// shape is a contract rather than a style preference.
			name:    "bad id",
			mutate:  func(tm *Template) { tm.ID = "Chat A!" },
			wantErr: "must match",
		},
		{
			// The protocol decides how the broker meters and pays for
			// the work; an unrecognised one has no settlement path.
			name:    "unknown protocol",
			mutate:  func(tm *Template) { tm.Protocol = "paid-stream/v1" },
			wantErr: "protocol must be",
		},
		{
			// Wei is arbitrary precision, so it travels as a decimal
			// string; a float here would silently lose the low digits.
			name:    "non-decimal price",
			mutate:  func(tm *Template) { tm.PriceDefault.AmountWei = "1.5" },
			wantErr: "non-negative decimal string",
		},
		{
			// The declaration owns these keys; a template that set one
			// would be overwriting what the protocol already says.
			name:    "reserved extra key",
			mutate:  func(tm *Template) { tm.Extra = map[string]any{"protocol": "paid-job/v1"} },
			wantErr: "reserved",
		},
		{
			// Only x-* keys are runner extensions, so only they can be
			// promoted from an attach document.
			name:    "promoted key is not an extension",
			mutate:  func(tm *Template) { tm.ExtraFromRunner = []string{"provider"} },
			wantErr: "must be an x-* key",
		},
		{
			// A step type the broker cannot run would fail at push
			// time, which is a slow way to learn about a typo.
			name: "unknown certification step type",
			mutate: func(tm *Template) {
				tm.Certification = []types.CertificationStep{{Name: "health", Type: "smoke"}}
			},
			wantErr: "unsupported type",
		},
		{
			// Results are reported by step name, so two steps sharing
			// one name would make the verdict unreadable.
			name: "duplicate step name",
			mutate: func(tm *Template) {
				tm.Certification = []types.CertificationStep{
					{Name: "health", Type: "readiness"},
					{Name: "health", Type: "request"},
				}
			},
			wantErr: "repeats step name",
		},
		{
			// A selector is matched against the identity a runner
			// declared at attach, so a key outside identity.* can never
			// match anything and would leave the offer unserved.
			name:    "match key outside identity",
			mutate:  func(tm *Template) { tm.Match = map[string]string{"openai.model": "gpt-oss-20b"} },
			wantErr: "identity.<dotted key>",
		},
		{
			// An empty value would match every runner of the
			// capability, which is what omitting match already means —
			// so it is far likelier a truncated config than an intent.
			name:    "match value is empty",
			mutate:  func(tm *Template) { tm.Match = map[string]string{"identity.openai.model": "  "} },
			wantErr: "has no value",
		},
		{
			// Neither primary nor stackable anywhere means no GPU can
			// ever run it: a config error, not a template that simply
			// never matches.
			name:    "unplaceable stacking",
			mutate:  func(tm *Template) { tm.Stacking = Stacking{} },
			wantErr: "never be placed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := base()
			tc.mutate(&tmpl)
			err := tmpl.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// The selector has to survive the YAML round trip: a wrong tag would
// drop it silently, and an offer with no match takes any runner of its
// capability — so two models of one capability would both be served by
// whichever runner attached first.
func TestLoadReadsMatchFromYAML(t *testing.T) {
	dir := writeTemplates(t, map[string]string{
		"chat.yaml": validTemplate("chat-20b", "openai:chat-completions", "gpt-oss-20b") +
			"match:\n  identity.openai.model: gpt-oss-20b\n",
	})
	catalog, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	tmpl, ok := catalog.Get("chat-20b")
	if !ok {
		t.Fatal("template did not load")
	}
	if tmpl.Match["identity.openai.model"] != "gpt-oss-20b" {
		t.Fatalf("match = %v", tmpl.Match)
	}
}
