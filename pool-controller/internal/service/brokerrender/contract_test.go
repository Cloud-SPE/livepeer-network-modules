package brokerrender

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"gopkg.in/yaml.v3"
)

// This file locks the rendered host-config against the capability-broker's
// loader contract.
//
// The broker's grammar lives in
// capability-broker/internal/config (config.go + validate.go). That package
// sits under another module's `internal/` tree, so Go forbids importing it
// from pool-controller — the import path may only be used by packages rooted
// at github.com/Cloud-SPE/livepeer-network-modules/capability-broker/. The
// next best thing, done here, is a faithful transcription of that grammar:
//
//   - brokerGrammarConfig mirrors config.Config field-for-field (yaml tags
//     copied verbatim) and is decoded with KnownFields(true), exactly as
//     config.Load does. Any key the renderer emits that the broker does not
//     know — a leftover v0 interaction-mode key, say — fails the decode.
//   - validateLikeBroker ports the capability-level rules from
//     (*config.Config).Validate that the renderer can violate.
//
// When capability-broker's grammar changes, this mirror must be updated with
// it.

type brokerGrammarConfig struct {
	Identity        brokerGrammarIdentity     `yaml:"identity"`
	ExternalBaseURL string                    `yaml:"external_base_url,omitempty"`
	Listen          brokerGrammarListen       `yaml:"listen,omitempty"`
	AdminAuth       brokerGrammarAuth         `yaml:"admin_auth,omitempty"`
	PaymentDaemon   brokerGrammarPayment      `yaml:"payment_daemon,omitempty"`
	SessionStore    brokerGrammarSessionStore `yaml:"session_store,omitempty"`
	PoolSnapshot    map[string]any            `yaml:"pool_snapshot,omitempty"`
	ReceiptSink     map[string]any            `yaml:"receipt_sink,omitempty"`
	Capabilities    []brokerGrammarCapability `yaml:"capabilities"`
}

type brokerGrammarIdentity struct {
	OrchEthAddress string `yaml:"orch_eth_address"`
	Label          string `yaml:"label,omitempty"`
}

type brokerGrammarListen struct {
	Paid       string `yaml:"paid,omitempty"`
	Metrics    string `yaml:"metrics,omitempty"`
	WorkerQUIC string `yaml:"worker_quic,omitempty"`
}

type brokerGrammarAuth struct {
	Method    string `yaml:"method,omitempty"`
	SecretRef string `yaml:"secret_ref,omitempty"`
}

type brokerGrammarPayment struct {
	Socket string `yaml:"socket,omitempty"`
	Mock   bool   `yaml:"mock,omitempty"`
}

type brokerGrammarSessionStore struct {
	Path           string `yaml:"path,omitempty"`
	SealingKeyFile string `yaml:"sealing_key_file,omitempty"`
}

type brokerGrammarCapability struct {
	ID          string                `yaml:"id"`
	OfferingID  string                `yaml:"offering_id"`
	Protocol    string                `yaml:"protocol"`
	Job         *brokerGrammarJob     `yaml:"job,omitempty"`
	Session     *brokerGrammarSession `yaml:"session,omitempty"`
	WorkUnit    brokerGrammarWorkUnit `yaml:"work_unit"`
	Health      map[string]any        `yaml:"health,omitempty"`
	Price       brokerGrammarPrice    `yaml:"price"`
	Backend     brokerGrammarBackend  `yaml:"backend"`
	Extra       map[string]any        `yaml:"extra,omitempty"`
	Constraints map[string]any        `yaml:"constraints,omitempty"`
}

type brokerGrammarJob struct {
	Transports []string `yaml:"transports"`
}

type brokerGrammarSession struct {
	DescriptorSchema string         `yaml:"descriptor_schema"`
	Heartbeat        map[string]any `yaml:"heartbeat,omitempty"`
	LeaseMaxSeconds  int            `yaml:"lease_max_seconds,omitempty"`
	BurnRatePerSec   float64        `yaml:"burn_rate_per_second,omitempty"`
	MinRunwayUnits   int64          `yaml:"min_runway_units,omitempty"`
	Runner           map[string]any `yaml:"runner"`
}

type brokerGrammarWorkUnit struct {
	Name      string         `yaml:"name"`
	Extractor map[string]any `yaml:"extractor"`
}

type brokerGrammarPrice struct {
	AmountWei string `yaml:"amount_wei"`
	PerUnits  uint64 `yaml:"per_units"`
}

type brokerGrammarBackend struct {
	ID                      string            `yaml:"id,omitempty"`
	Transport               string            `yaml:"transport"`
	URL                     string            `yaml:"url,omitempty"`
	Auth                    brokerGrammarAuth `yaml:"auth,omitempty"`
	HostEnrollmentID        string            `yaml:"host_enrollment_id,omitempty"`
	HardwareUnitID          string            `yaml:"hardware_unit_id,omitempty"`
	GPUUUID                 string            `yaml:"gpu_uuid,omitempty"`
	TemplateID              string            `yaml:"template_id,omitempty"`
	WorkerSessionCredential string            `yaml:"worker_session_credential,omitempty"`
	MaxInFlight             int               `yaml:"max_in_flight,omitempty"`
	QueueLimit              int               `yaml:"queue_limit,omitempty"`
	Profile                 string            `yaml:"profile,omitempty"`
	SessionRunner           map[string]any    `yaml:"session_runner,omitempty"`
}

var (
	brokerProtocolRE   = regexp.MustCompile(`^[a-z][a-z0-9-]*/v[0-9]+$`)
	brokerEthAddressRE = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	brokerPriceWeiRE   = regexp.MustCompile(`^[0-9]+$`)
)

// assertBrokerLoads decodes rendered YAML the way capability-broker's
// config.Load does (KnownFields(true)) and then runs the ported subset of
// its validation rules.
func assertBrokerLoads(t *testing.T, raw []byte) brokerGrammarConfig {
	t.Helper()
	var cfg brokerGrammarConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("broker would reject this config at parse time: %v\n%s", err, raw)
	}
	if err := validateLikeBroker(cfg); err != nil {
		t.Fatalf("broker would reject this config at validate time: %v\n%s", err, raw)
	}
	return cfg
}

func validateLikeBroker(cfg brokerGrammarConfig) error {
	if !brokerEthAddressRE.MatchString(cfg.Identity.OrchEthAddress) {
		return fmt.Errorf("identity.orch_eth_address: must be 0x-prefixed 40-hex (got %q)", cfg.Identity.OrchEthAddress)
	}
	if (cfg.SessionStore.Path == "") != (cfg.SessionStore.SealingKeyFile == "") {
		return fmt.Errorf("session_store: path and sealing_key_file must be set together")
	}
	if len(cfg.Capabilities) == 0 {
		return fmt.Errorf("capabilities: must declare at least one")
	}
	for i, capability := range cfg.Capabilities {
		ctx := fmt.Sprintf("capabilities[%d] (%s/%s)", i, capability.ID, capability.OfferingID)
		if capability.ID == "" {
			return fmt.Errorf("%s: id is required", ctx)
		}
		if capability.OfferingID == "" {
			return fmt.Errorf("%s: offering_id is required", ctx)
		}
		if !brokerProtocolRE.MatchString(capability.Protocol) {
			return fmt.Errorf("%s: protocol must match <name>/v<major> (got %q)", ctx, capability.Protocol)
		}
		switch {
		case strings.HasPrefix(capability.Protocol, "paid-job/"):
			if capability.Session != nil {
				return fmt.Errorf("%s: session axes are invalid on a paid-job offering", ctx)
			}
			if capability.Job == nil || len(capability.Job.Transports) == 0 {
				return fmt.Errorf("%s: job.transports is required for paid-job offerings", ctx)
			}
			seen := map[string]bool{}
			for _, transport := range capability.Job.Transports {
				switch transport {
				case "unary", "stream", "multipart":
				default:
					return fmt.Errorf("%s: job.transports entry %q must be unary|stream|multipart", ctx, transport)
				}
				if seen[transport] {
					return fmt.Errorf("%s: job.transports entry %q duplicated", ctx, transport)
				}
				seen[transport] = true
			}
		case strings.HasPrefix(capability.Protocol, "paid-session/"):
			if capability.Job != nil {
				return fmt.Errorf("%s: job axes are invalid on a paid-session offering", ctx)
			}
			if capability.Session == nil {
				return fmt.Errorf("%s: session block is required for paid-session offerings", ctx)
			}
			if !brokerProtocolRE.MatchString(capability.Session.DescriptorSchema) {
				return fmt.Errorf("%s: session.descriptor_schema must match <name>/v<major>", ctx)
			}
			if cfg.SessionStore.Path == "" {
				return fmt.Errorf("%s: session_store must be configured when a paid-session capability is declared", ctx)
			}
			if cfg.ExternalBaseURL == "" {
				return fmt.Errorf("%s: external_base_url must be configured when a paid-session capability is declared", ctx)
			}
		}
		if capability.WorkUnit.Name == "" {
			return fmt.Errorf("%s: work_unit.name is required", ctx)
		}
		if len(capability.WorkUnit.Extractor) == 0 {
			return fmt.Errorf("%s: work_unit.extractor is required", ctx)
		}
		if _, ok := capability.WorkUnit.Extractor["type"].(string); !ok {
			return fmt.Errorf("%s: work_unit.extractor.type must be a string", ctx)
		}
		if !brokerPriceWeiRE.MatchString(capability.Price.AmountWei) {
			return fmt.Errorf("%s: price.amount_wei must be a non-negative decimal string (got %q)", ctx, capability.Price.AmountWei)
		}
		if capability.Price.PerUnits == 0 {
			return fmt.Errorf("%s: price.per_units must be > 0", ctx)
		}
		if capability.Backend.Transport == "" {
			return fmt.Errorf("%s: backend.transport is required", ctx)
		}
	}
	return nil
}

// contractRenderInput builds a pool state that exercises both render paths
// (member backend + connected-worker template assignment).
func contractRenderInput(offers []types.Offer) RenderInput {
	return RenderInput{
		Bootstrap: BootstrapBrokerSettings{
			Identity:      config.Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678", Label: "pool-broker"},
			Listen:        config.Listen{Paid: ":8080", Metrics: ":9090"},
			PaymentDaemon: config.PaymentDaemon{Socket: "/var/run/livepeer/payment-daemon.sock"},
		},
		Offers: offers,
		Members: []types.MemberRecord{{
			ID:         "member-1",
			EthAddress: "0xmember",
			PayoutMode: "onchain",
			Status:     types.MemberStatusActive,
		}},
		Backends: []types.MemberBackend{{
			ID:          "backend-1",
			MemberID:    "member-1",
			Transport:   "http",
			URL:         "http://backend:8080/v1/rerank",
			Auth:        config.AuthConfig{Method: "none"},
			Status:      types.BackendStatusActive,
			HealthProbe: config.HealthProbe{Type: "http-status"},
		}},
		Assignments: []types.Assignment{{
			ID:              "assignment-1",
			OfferID:         offers[0].ID,
			MemberBackendID: "backend-1",
			Status:          types.AssignmentStatusActive,
		}},
	}
}

func contractOffer() types.Offer {
	return types.Offer{
		ID:           "offer-1",
		CapabilityID: "rerank",
		OfferingID:   "zerank-2-default",
		Protocol:     types.ProtocolPaidJobV1,
		WorkUnit:     config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula", "expression": "1"}},
		Price:        config.Price{AmountWei: "372000000000", PerUnits: 1},
		Status:       types.OfferStatusActive,
	}
}

func TestRenderedConfigLoadsUnderBrokerGrammar(t *testing.T) {
	offer := contractOffer()
	offer.Job = &types.OfferJobAxes{Transports: []string{"unary", "stream"}}

	got, err := Render(contractRenderInput([]types.Offer{offer}))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	cfg := assertBrokerLoads(t, got.ConfigYAML)
	if len(cfg.Capabilities) != 1 {
		t.Fatalf("len(capabilities) = %d, want 1", len(cfg.Capabilities))
	}
	capability := cfg.Capabilities[0]
	if capability.Protocol != types.ProtocolPaidJobV1 {
		t.Fatalf("protocol = %q, want %q", capability.Protocol, types.ProtocolPaidJobV1)
	}
	if capability.Job == nil || strings.Join(capability.Job.Transports, ",") != "unary,stream" {
		t.Fatalf("job = %#v, want transports [unary stream]", capability.Job)
	}
	if capability.Session != nil {
		t.Fatalf("session = %#v, want nil for a paid-job capability", capability.Session)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none when the offer declares transports", got.Warnings)
	}
}

func TestRenderDefaultsJobTransportsAndWarns(t *testing.T) {
	got, err := Render(contractRenderInput([]types.Offer{contractOffer()}))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	cfg := assertBrokerLoads(t, got.ConfigYAML)
	if cfg.Capabilities[0].Job == nil || strings.Join(cfg.Capabilities[0].Job.Transports, ",") != "unary" {
		t.Fatalf("job = %#v, want the documented default [unary]", cfg.Capabilities[0].Job)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "defaulted to [unary]") {
		t.Fatalf("warnings = %v, want one defaulted-transports warning", got.Warnings)
	}
	if !strings.Contains(got.Warnings[0], "offer-1") {
		t.Fatalf("warning %q does not name the offer", got.Warnings[0])
	}
}

func TestRenderDerivesJobTransportsFromExtra(t *testing.T) {
	offer := contractOffer()
	offer.Extra = map[string]any{"job": map[string]any{"transports": []any{"multipart"}}}

	got, err := Render(contractRenderInput([]types.Offer{offer}))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	cfg := assertBrokerLoads(t, got.ConfigYAML)
	if cfg.Capabilities[0].Job == nil || strings.Join(cfg.Capabilities[0].Job.Transports, ",") != "multipart" {
		t.Fatalf("job = %#v, want transports [multipart] derived from extra", cfg.Capabilities[0].Job)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", got.Warnings)
	}
}

// TestRenderRefusesPaidSessionOffers pins the deliberate choice: pool data
// carries no descriptor_schema and no runner create/status/terminate paths,
// and pool-controller configures neither external_base_url nor
// session_store, so a session capability cannot be rendered validly. The
// renderer fails loudly instead of emitting a config the broker refuses to
// load (which would take every other pool capability down with it).
func TestRenderRefusesPaidSessionOffers(t *testing.T) {
	offer := contractOffer()
	offer.CapabilityID = "livepeer:vtuber-session"
	offer.Protocol = types.ProtocolPaidSessionV1

	_, err := Render(contractRenderInput([]types.Offer{offer}))
	if err == nil {
		t.Fatalf("Render() = nil error, want refusal for a paid-session offer")
	}
	for _, want := range []string{"offer-1", types.ProtocolPaidSessionV1, "paid-session"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Render() error = %q, missing %q", err.Error(), want)
		}
	}
}

func TestRenderRejectsUnknownProtocolAndBadTransports(t *testing.T) {
	unknown := contractOffer()
	unknown.Protocol = "http-reqresp@v0"
	if _, err := Render(contractRenderInput([]types.Offer{unknown})); err == nil {
		t.Fatalf("Render() = nil error, want refusal for a v0 interaction-mode string")
	}

	empty := contractOffer()
	empty.Protocol = ""
	if _, err := Render(contractRenderInput([]types.Offer{empty})); err == nil {
		t.Fatalf("Render() = nil error, want refusal for an offer with no protocol")
	}

	bad := contractOffer()
	bad.Job = &types.OfferJobAxes{Transports: []string{"webrtc"}}
	if _, err := Render(contractRenderInput([]types.Offer{bad})); err == nil {
		t.Fatalf("Render() = nil error, want refusal for an undeclarable transport")
	}
}

func TestRenderedConnectedWorkerConfigLoadsUnderBrokerGrammar(t *testing.T) {
	offer := types.Offer{
		ID:           "offer-chat",
		CapabilityID: "openai:chat-completions",
		OfferingID:   "llama-3-70b-shared",
		Protocol:     types.ProtocolPaidJobV1,
		Job:          &types.OfferJobAxes{Transports: []string{"unary", "stream"}},
		WorkUnit:     config.WorkUnit{Name: "tokens", Extractor: map[string]any{"type": "openai-usage", "field": "total_tokens"}},
		Price:        config.Price{AmountWei: "210000000", PerUnits: 1},
		Extra:        map[string]any{"openai": map[string]any{"model": "llama-3-70b"}, "provider": "vllm"},
		Status:       types.OfferStatusActive,
	}
	input := RenderInput{
		Bootstrap: BootstrapBrokerSettings{
			Identity: config.Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
			Listen:   config.Listen{Paid: ":8080", Metrics: ":9090", WorkerQUIC: ":8443"},
		},
		Offers: []types.Offer{offer},
		PoolMembers: []types.PoolMember{{
			ID:         "0xmember",
			EthAddress: "0xmember",
			PayoutMode: "eth",
			Status:     types.MemberStatusActive,
		}},
		HostEnrollments: []types.HostEnrollment{{
			ID:                      "host-1",
			MemberEthAddress:        "0xmember",
			BrokerSessionCredential: "worker-secret",
			Status:                  types.HostEnrollmentActive,
		}},
		HardwareUnits: []types.HardwareUnit{{
			ID:               "gpu-1",
			EnrollmentID:     "host-1",
			MemberEthAddress: "0xmember",
			GPUUUID:          "GPU-1",
			State:            types.HardwareUnitActive,
		}},
		Templates: []types.TemplateCatalogEntry{{
			ID:           "chat-4090",
			CapabilityID: "openai:chat-completions",
			OfferingID:   "llama-3-70b-shared",
			Protocol:     types.ProtocolPaidJobV1,
			Status:       types.TemplateStatusActive,
		}},
		TemplateAssignments: []types.TemplateAssignment{{
			ID:               "assign-chat-1",
			HardwareUnitID:   "gpu-1",
			HostEnrollmentID: "host-1",
			MemberEthAddress: "0xmember",
			TemplateID:       "chat-4090",
			State:            types.TemplateAssignmentActive,
		}},
	}

	got, err := Render(input)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	cfg := assertBrokerLoads(t, got.ConfigYAML)
	if len(cfg.Capabilities) != 1 || cfg.Capabilities[0].Job == nil {
		t.Fatalf("capabilities = %#v, want one paid-job capability with job axes", cfg.Capabilities)
	}
	if cfg.Capabilities[0].Backend.WorkerSessionCredential != "worker-secret" {
		t.Fatalf("backend = %#v, want the worker session credential preserved", cfg.Capabilities[0].Backend)
	}
}
