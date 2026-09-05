package brokerpush

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type fakePusher struct {
	offers      []brokeradmin.OfferPush
	creds       []brokeradmin.CredentialPush
	offerRev    string
	credRev     string
	offerResult *brokeradmin.PushResult
	credResult  *brokeradmin.PushResult
	err         error
}

func (f *fakePusher) PutOffers(_ context.Context, rev string, o []brokeradmin.OfferPush) (*brokeradmin.PushResult, error) {
	f.offers, f.offerRev = o, rev
	return f.offerResult, f.err
}

func (f *fakePusher) PutCredentials(_ context.Context, rev string, c []brokeradmin.CredentialPush) (*brokeradmin.PushResult, error) {
	f.creds, f.credRev = c, rev
	return f.credResult, nil
}

// chatTemplate is the catalog half of a fixture: what the workload IS.
// Tests bend one field at a time from here so a failure names the rule
// it broke.
func chatTemplate() templates.Template {
	return templates.Template{
		ID:           "chat-20b",
		Capability:   "openai:chat-completions",
		OfferingID:   "llama-shared",
		Protocol:     "paid-job/v1",
		PriceDefault: templates.Price{AmountWei: "210000000", PerUnits: 1},
		Extra: map[string]any{
			"openai":    map[string]any{"model": "llama-3-70b"},
			"region":    "us-west-2",
			"gpu_class": "h100",
		},
	}
}

// adopted is the pool half: an override is what says "this pool sells
// this", so every test that expects a push needs one.
func adopted(templateID string) types.TemplateOverride {
	return types.TemplateOverride{TemplateID: templateID, Enabled: true}
}

func TestBuildOffersFromCatalog(t *testing.T) {
	for _, tc := range []struct {
		name      string
		catalog   []templates.Template
		overrides []types.TemplateOverride
		check     func(t *testing.T, got []brokeradmin.OfferPush)
	}{
		{
			// A template this build knows how to run is not a template
			// this pool sells. Only an override says it was adopted.
			name:      "template with no override is not pushed",
			catalog:   []templates.Template{chatTemplate()},
			overrides: nil,
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if len(got) != 0 {
					t.Fatalf("an unadopted template was pushed: %+v", got)
				}
			},
		},
		{
			// The distinction the whole TemplateOverride record exists
			// for. Dropping a disabled template from the set would
			// DELETE the broker's offer and with it the record of which
			// runner shape was certified, so re-enabling would silently
			// start a fresh freeze. Disabled keeps the offer and stops
			// the advertising (broker-admin §4.4).
			name:      "disabled override is pushed disabled, not omitted",
			catalog:   []templates.Template{chatTemplate()},
			overrides: []types.TemplateOverride{{TemplateID: "chat-20b", Enabled: false}},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if len(got) != 1 {
					t.Fatalf("offers = %d, want the disabled offer kept in the set", len(got))
				}
				if !got[0].Disabled {
					t.Fatalf("a disabled template was pushed as live: %+v", got[0])
				}
				if got[0].OfferingID != "llama-shared" || got[0].Price.AmountWei != "210000000" {
					t.Fatalf("a disabled offer must keep its shape: %+v", got[0])
				}
			},
		},
		{
			name:      "enabled override is pushed live",
			catalog:   []templates.Template{chatTemplate()},
			overrides: []types.TemplateOverride{adopted("chat-20b")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if len(got) != 1 || got[0].Disabled {
					t.Fatalf("offers = %+v", got)
				}
				if got[0].Capability != "openai:chat-completions" || got[0].Protocol != "paid-job/v1" {
					t.Fatalf("offer = %+v", got[0])
				}
			},
		},
		{
			name:      "price falls back to the catalog's suggestion",
			catalog:   []templates.Template{chatTemplate()},
			overrides: []types.TemplateOverride{adopted("chat-20b")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if got[0].Price != (brokeradmin.OfferPushPrice{AmountWei: "210000000", PerUnits: 1}) {
					t.Fatalf("price = %+v, want the template's price_default", got[0].Price)
				}
			},
		},
		{
			name:    "override price wins over the catalog's",
			catalog: []templates.Template{chatTemplate()},
			overrides: []types.TemplateOverride{{
				TemplateID: "chat-20b", Enabled: true,
				Price: &config.Price{AmountWei: "42", PerUnits: 1000},
			}},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if got[0].Price != (brokeradmin.OfferPushPrice{AmountWei: "42", PerUnits: 1000}) {
					t.Fatalf("price = %+v, want the pool's override", got[0].Price)
				}
			},
		},
		{
			// per_units is a divisor on the broker side, so zero is not
			// a value it can be given.
			name: "per_units 0 is normalised to 1",
			catalog: []templates.Template{func() templates.Template {
				tmpl := chatTemplate()
				tmpl.PriceDefault.PerUnits = 0
				return tmpl
			}()},
			overrides: []types.TemplateOverride{adopted("chat-20b")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if got[0].Price.PerUnits != 1 {
					t.Fatalf("per_units = %d, want the documented default", got[0].Price.PerUnits)
				}
			},
		},
		{
			name:    "override per_units 0 is normalised too",
			catalog: []templates.Template{chatTemplate()},
			overrides: []types.TemplateOverride{{
				TemplateID: "chat-20b", Enabled: true,
				Price: &config.Price{AmountWei: "7", PerUnits: 0},
			}},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if got[0].Price.PerUnits != 1 {
					t.Fatalf("per_units = %d, want the documented default", got[0].Price.PerUnits)
				}
			},
		},
		{
			// An override exists precisely to disagree with the
			// catalog, so on a collision the pool's value is the answer.
			name:    "extra merges with the override winning collisions",
			catalog: []templates.Template{chatTemplate()},
			overrides: []types.TemplateOverride{{
				TemplateID: "chat-20b", Enabled: true,
				Extra: map[string]any{"region": "eu-central-1", "tier": "gold"},
			}},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if got[0].Extra["region"] != "eu-central-1" {
					t.Fatalf("the catalog's value survived a collision: %v", got[0].Extra)
				}
				if got[0].Extra["gpu_class"] != "h100" {
					t.Fatalf("catalog extra dropped by the merge: %v", got[0].Extra)
				}
				if got[0].Extra["tier"] != "gold" {
					t.Fatalf("override-only extra dropped: %v", got[0].Extra)
				}
			},
		},
		{
			name: "the template's match wins",
			catalog: []templates.Template{func() templates.Template {
				tmpl := chatTemplate()
				tmpl.Match = map[string]string{"identity.openai.model": "gpt-oss-20b"}
				return tmpl
			}()},
			overrides: []types.TemplateOverride{adopted("chat-20b")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				want := map[string]string{"identity.openai.model": "gpt-oss-20b"}
				if !reflect.DeepEqual(got[0].Match, want) {
					t.Fatalf("match = %v, want the template's own selector", got[0].Match)
				}
			},
		},
		{
			// A single-model template should not have to state the
			// model twice.
			name:      "match is derived from extra.openai.model",
			catalog:   []templates.Template{chatTemplate()},
			overrides: []types.TemplateOverride{adopted("chat-20b")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if got[0].Match["identity.openai.model"] != "llama-3-70b" {
					t.Fatalf("match = %v", got[0].Match)
				}
			},
		},
		{
			// The derivation reads the MERGED extra, so a pool that
			// repoints the template at another model is matched on the
			// model it actually serves.
			name:    "derived match follows the override's model",
			catalog: []templates.Template{chatTemplate()},
			overrides: []types.TemplateOverride{{
				TemplateID: "chat-20b", Enabled: true,
				Extra: map[string]any{"openai": map[string]any{"model": "mixtral-8x7b"}},
			}},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if got[0].Match["identity.openai.model"] != "mixtral-8x7b" {
					t.Fatalf("match = %v", got[0].Match)
				}
			},
		},
		{
			name: "no match and no identity takes any runner of the capability",
			catalog: []templates.Template{func() templates.Template {
				tmpl := chatTemplate()
				tmpl.Extra = nil
				return tmpl
			}()},
			overrides: []types.TemplateOverride{adopted("chat-20b")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if got[0].Match != nil {
					t.Fatalf("match = %v, want nil so every runner of the capability qualifies", got[0].Match)
				}
			},
		},
		{
			// The broker defaults an omitted required to true, so an
			// intentionally-optional step that travelled without the
			// field would come out required — the inverse of what the
			// template said. The pointer must always be set.
			name: "certification always states required, in both directions",
			catalog: []templates.Template{func() templates.Template {
				tmpl := chatTemplate()
				tmpl.Certification = []types.CertificationStep{
					{Name: "ready", Type: "readiness", Required: true},
					{Name: "latency", Type: "latency", Required: false, TimeoutMS: 4000,
						Config: map[string]any{"samples": 3}},
				}
				return tmpl
			}()},
			overrides: []types.TemplateOverride{adopted("chat-20b")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				steps := got[0].Certification
				if len(steps) != 2 {
					t.Fatalf("certification = %+v", steps)
				}
				for _, step := range steps {
					if step.Required == nil {
						t.Fatalf("step %q left required unset; the broker would default it to true", step.Name)
					}
				}
				if !*steps[0].Required {
					t.Fatalf("required step %q pushed as optional", steps[0].Name)
				}
				if *steps[1].Required {
					t.Fatalf("optional step %q pushed as required", steps[1].Name)
				}
				if steps[1].TimeoutMS != 4000 || steps[1].Config["samples"] != 3 {
					t.Fatalf("step config did not survive the conversion: %+v", steps[1])
				}
				// Each step needs its own pointer; a shared one would
				// make every step agree with the last.
				if steps[0].Required == steps[1].Required {
					t.Fatal("all steps share one required pointer")
				}
			},
		},
		{
			name:      "no certification pushes none",
			catalog:   []templates.Template{chatTemplate()},
			overrides: []types.TemplateOverride{adopted("chat-20b")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if got[0].Certification != nil {
					t.Fatalf("certification = %+v, want nil", got[0].Certification)
				}
			},
		},
		{
			// Capacity omitted entirely means "the broker's default",
			// which is not the same as a zero ceiling.
			name:      "capacity is omitted when the template sets none",
			catalog:   []templates.Template{chatTemplate()},
			overrides: []types.TemplateOverride{adopted("chat-20b")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if got[0].Capacity != nil {
					t.Fatalf("capacity = %+v, want nil", got[0].Capacity)
				}
			},
		},
		{
			name: "capacity travels when either half is set",
			catalog: []templates.Template{func() templates.Template {
				tmpl := chatTemplate()
				tmpl.Capacity = templates.Capacity{QueueLimit: 4}
				return tmpl
			}()},
			overrides: []types.TemplateOverride{adopted("chat-20b")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if got[0].Capacity == nil || got[0].Capacity.QueueLimit != 4 {
					t.Fatalf("capacity = %+v", got[0].Capacity)
				}
			},
		},
		{
			// The push is content-hashed into a revision, so the order
			// has to come from the data rather than from the catalog's
			// file listing.
			name: "output is sorted by offering id",
			catalog: []templates.Template{
				func() templates.Template {
					tmpl := chatTemplate()
					tmpl.ID, tmpl.OfferingID = "z-template", "zeta"
					return tmpl
				}(),
				func() templates.Template {
					tmpl := chatTemplate()
					tmpl.ID, tmpl.OfferingID = "a-template", "alpha"
					return tmpl
				}(),
			},
			overrides: []types.TemplateOverride{adopted("z-template"), adopted("a-template")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if len(got) != 2 || got[0].OfferingID != "alpha" || got[1].OfferingID != "zeta" {
					t.Fatalf("offers = %+v", got)
				}
			},
		},
		{
			// An override naming a template this build does not carry
			// is stale config, not an offer to invent.
			name:      "an override with no template pushes nothing",
			catalog:   nil,
			overrides: []types.TemplateOverride{adopted("chat-20b")},
			check: func(t *testing.T, got []brokeradmin.OfferPush) {
				if len(got) != 0 {
					t.Fatalf("offers = %+v", got)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, BuildOffersFromCatalog(tc.catalog, tc.overrides))
		})
	}
}

// The push carries what the operator owns and nothing the runner
// declares — that separation is the whole epic.
func TestBuildOffersCarriesNoRunnerFacts(t *testing.T) {
	got := BuildOffersFromCatalog([]templates.Template{chatTemplate()}, []types.TemplateOverride{adopted("chat-20b")})
	if len(got) != 1 {
		t.Fatalf("offers = %d", len(got))
	}
	raw, _ := json.Marshal(got[0])
	// Check the offer's own keys, not a substring of the whole payload:
	// "readiness" legitimately appears as a certification STEP TYPE (the
	// offer says run a readiness step; the runner declares the recipe).
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"offering_id": true, "capability": true, "protocol": true, "match": true, "price": true,
		"capacity": true, "extra": true, "constraints": true, "extra_from_runner": true,
		"certification": true, "disabled": true,
	}
	for k := range fields {
		if !allowed[k] {
			t.Fatalf("push carries %q, which is not part of the offer grammar: %s", k, raw)
		}
	}
	for _, runnerFact := range []string{"backend", "transports", "work_unit", "worker_url", "paths"} {
		if _, present := fields[runnerFact]; present {
			t.Fatalf("push carries the runner fact %q: %s", runnerFact, raw)
		}
	}
	if got[0].Price.AmountWei != "210000000" || got[0].Capability != "openai:chat-completions" {
		t.Fatalf("push = %s", raw)
	}
	// The selector is derived from the identity the operator declared.
	if got[0].Match["identity.openai.model"] != "llama-3-70b" {
		t.Fatalf("match = %v", got[0].Match)
	}
	if got[0].Extra["region"] != "us-west-2" {
		t.Fatalf("operator extra dropped: %v", got[0].Extra)
	}
}

// Only the hash travels; a revoked or dropped enrollment is a revoke.
func TestBuildCredentialsPushesHashesOnly(t *testing.T) {
	now := time.Now().UTC()
	creds := BuildCredentials([]types.HostEnrollment{
		{ID: "host-1", MemberEthAddress: "0xabc", HostLabel: "rig", BrokerSessionCredential: "plaintext-secret",
			Status: types.HostEnrollmentActive, CreatedAt: now},
		{ID: "host-2", BrokerSessionCredential: "another", Status: types.HostEnrollmentRevoked, CreatedAt: now},
		{ID: "host-3", Status: types.HostEnrollmentActive, CreatedAt: now}, // no credential yet
	})
	if len(creds) != 2 {
		t.Fatalf("credentials = %d (a host with no credential must be skipped, not pushed empty)", len(creds))
	}
	raw, _ := json.Marshal(creds)
	if strings.Contains(string(raw), "plaintext-secret") || strings.Contains(string(raw), "another") {
		t.Fatalf("plaintext credential left the controller: %s", raw)
	}
	if creds[0].TokenSHA256 == "" || len(creds[0].TokenSHA256) != 64 {
		t.Fatalf("hash = %q", creds[0].TokenSHA256)
	}
	if creds[0].HostID != "host-1" || creds[0].MemberEthAddress != "0xabc" {
		t.Fatalf("credential = %+v", creds[0])
	}
	if creds[1].State != "revoked" {
		t.Fatalf("revoked enrollment pushed as %q", creds[1].State)
	}
}

// Offers go first, so a host whose credential was just accepted attaches
// into a broker that already knows what it might serve.
func TestSyncPushesOffersBeforeCredentials(t *testing.T) {
	f := &fakePusher{
		offerResult: &brokeradmin.PushResult{Changed: []string{"llama-shared"}},
		credResult:  &brokeradmin.PushResult{RevokedHosts: []string{"host-9"}},
	}
	offers := BuildOffersFromCatalog([]templates.Template{chatTemplate()}, []types.TemplateOverride{adopted("chat-20b")})
	res, err := Sync(context.Background(), f, State{
		Offers:      offers,
		Enrollments: []types.HostEnrollment{{ID: "host-1", BrokerSessionCredential: "s", Status: types.HostEnrollmentActive}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Offers != 1 || res.Credentials != 1 {
		t.Fatalf("result = %+v", res)
	}
	if len(res.OffersChanged) != 1 || len(res.RevokedHosts) != 1 {
		t.Fatalf("result did not report what changed: %+v", res)
	}
	if f.offerRev == "" || f.offerRev == f.credRev {
		t.Fatalf("revisions = %q / %q; each set gets its own", f.offerRev, f.credRev)
	}
	// The derived set is pushed as-is: nothing between the derivation
	// and the wire may reshape it.
	if !reflect.DeepEqual(f.offers, offers) {
		t.Fatalf("pushed offers = %+v, want the derived set", f.offers)
	}
	// Identical state produces an identical revision, which is what
	// makes "nothing changed" observable.
	again := Revision(BuildOffersFromCatalog([]templates.Template{chatTemplate()}, []types.TemplateOverride{adopted("chat-20b")}))
	if again != f.offerRev {
		t.Fatalf("revision is not stable: %q vs %q", again, f.offerRev)
	}
}

// A disabled template and an absent one produce different revisions, so
// disabling something is a push the broker actually notices.
func TestRevisionDistinguishesDisabledFromAbsent(t *testing.T) {
	catalog := []templates.Template{chatTemplate()}
	disabled := Revision(BuildOffersFromCatalog(catalog, []types.TemplateOverride{{TemplateID: "chat-20b"}}))
	absent := Revision(BuildOffersFromCatalog(catalog, nil))
	enabled := Revision(BuildOffersFromCatalog(catalog, []types.TemplateOverride{adopted("chat-20b")}))
	if disabled == absent || disabled == enabled {
		t.Fatalf("revisions collide: disabled=%q absent=%q enabled=%q", disabled, absent, enabled)
	}
}
