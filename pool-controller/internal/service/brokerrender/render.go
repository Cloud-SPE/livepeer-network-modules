package brokerrender

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"gopkg.in/yaml.v3"
)

type BootstrapBrokerSettings struct {
	Identity      config.Identity
	Listen        config.Listen
	PaymentDaemon config.PaymentDaemon
	ReceiptSink   config.ReceiptSink
	AdminAuth     config.AuthConfig
}

type RenderInput struct {
	Bootstrap           BootstrapBrokerSettings
	Offers              []types.Offer
	Members             []types.MemberRecord
	Backends            []types.MemberBackend
	Assignments         []types.Assignment
	PoolMembers         []types.PoolMember
	HostEnrollments     []types.HostEnrollment
	HardwareUnits       []types.HardwareUnit
	Templates           []types.TemplateCatalogEntry
	TemplateAssignments []types.TemplateAssignment
}

type RenderResult struct {
	ConfigYAML []byte
	Revision   string
	Model      BrokerConfig
	// Warnings carries operator-visible notes about choices the renderer
	// had to make on incomplete offer data — today only the
	// job.transports fallback (see DefaultJobTransports).
	Warnings []string
}

// BrokerConfig mirrors capability-broker/internal/config.Config. The broker
// parses host-config.yaml with KnownFields(true), so every key rendered here
// must exist in that grammar and every required key must be present.
//
// external_base_url and session_store are deliberately absent: they are
// required only when a paid-session capability is declared, and
// pool-controller refuses to render paid-session offerings (see
// jobAxesForOffer).
type BrokerConfig struct {
	Identity      config.Identity      `yaml:"identity"`
	AdminAuth     config.AuthConfig    `yaml:"admin_auth,omitempty"`
	Listen        BrokerListen         `yaml:"listen,omitempty"`
	PaymentDaemon config.PaymentDaemon `yaml:"payment_daemon,omitempty"`
	ReceiptSink   config.ReceiptSink   `yaml:"receipt_sink,omitempty"`
	Capabilities  []BrokerCapability   `yaml:"capabilities"`
}

type BrokerListen struct {
	Paid       string `yaml:"paid,omitempty"`
	Metrics    string `yaml:"metrics,omitempty"`
	WorkerQUIC string `yaml:"worker_quic,omitempty"`
}

type BrokerCapability struct {
	ID         string `yaml:"id"`
	OfferingID string `yaml:"offering_id"`
	// Protocol is the protocol tag ("paid-job/v1"). The broker rejects a
	// capability whose protocol does not match <name>/v<major>, and
	// requires exactly the matching axes block.
	Protocol string `yaml:"protocol"`
	// Job carries the paid-job axes. Always non-nil for the capabilities
	// this renderer emits, since paid-session offerings are refused.
	Job      *BrokerJobAxes  `yaml:"job,omitempty"`
	WorkUnit config.WorkUnit `yaml:"work_unit"`
	Health   config.Health   `yaml:"health,omitempty"`
	Price    config.Price    `yaml:"price"`
	Backend  BrokerBackend   `yaml:"backend"`
	Extra    map[string]any  `yaml:"extra,omitempty"`
	// Constraints is always rendered (no omitempty) so that downstream
	// resolvers can fingerprint a stable shape even when the operator
	// declared no constraints. An empty map renders as `constraints: {}`.
	Constraints map[string]any `yaml:"constraints"`
}

// BrokerJobAxes mirrors capability-broker/internal/config.JobCapability.
type BrokerJobAxes struct {
	Transports []string `yaml:"transports"`
}

type BrokerBackend struct {
	ID                      string            `yaml:"id,omitempty"`
	Transport               string            `yaml:"transport"`
	URL                     string            `yaml:"url,omitempty"`
	Auth                    config.AuthConfig `yaml:"auth,omitempty"`
	HostEnrollmentID        string            `yaml:"host_enrollment_id,omitempty"`
	HardwareUnitID          string            `yaml:"hardware_unit_id,omitempty"`
	GPUUUID                 string            `yaml:"gpu_uuid,omitempty"`
	TemplateID              string            `yaml:"template_id,omitempty"`
	WorkerSessionCredential string            `yaml:"worker_session_credential,omitempty"`
	MaxInFlight             int               `yaml:"max_in_flight,omitempty"`
	QueueLimit              int               `yaml:"queue_limit,omitempty"`
}

func Render(input RenderInput) (RenderResult, error) {
	offersByID := make(map[string]types.Offer, len(input.Offers))
	for _, offer := range input.Offers {
		offersByID[offer.ID] = offer
	}
	membersByID := make(map[string]types.MemberRecord, len(input.Members))
	for _, member := range input.Members {
		membersByID[member.ID] = member
	}
	backendsByID := make(map[string]types.MemberBackend, len(input.Backends))
	for _, backend := range input.Backends {
		backendsByID[backend.ID] = backend
	}
	poolMembersByID := make(map[string]types.PoolMember, len(input.PoolMembers))
	for _, member := range input.PoolMembers {
		poolMembersByID[member.ID] = member
		poolMembersByID[member.EthAddress] = member
	}
	enrollmentsByID := make(map[string]types.HostEnrollment, len(input.HostEnrollments))
	for _, enrollment := range input.HostEnrollments {
		enrollmentsByID[enrollment.ID] = enrollment
	}
	hardwareByID := make(map[string]types.HardwareUnit, len(input.HardwareUnits))
	for _, hardware := range input.HardwareUnits {
		hardwareByID[hardware.ID] = hardware
	}
	templatesByID := make(map[string]types.TemplateCatalogEntry, len(input.Templates))
	for _, template := range input.Templates {
		templatesByID[template.ID] = template
	}

	capabilities := make([]BrokerCapability, 0)
	warnings := make([]string, 0)
	for _, assignment := range input.Assignments {
		if assignment.Status != types.AssignmentStatusActive {
			continue
		}
		offer, ok := offersByID[assignment.OfferID]
		if !ok || offer.Status != types.OfferStatusActive {
			continue
		}
		job, warning, err := jobAxesForOffer(offer)
		if err != nil {
			return RenderResult{}, err
		}
		if warning != "" {
			warnings = appendWarning(warnings, warning)
		}
		backend, ok := backendsByID[assignment.MemberBackendID]
		if !ok || backend.Status != types.BackendStatusActive {
			continue
		}
		member, ok := membersByID[backend.MemberID]
		if !ok || member.Status != types.MemberStatusActive {
			continue
		}
		extra := cloneMap(offer.Extra)
		if extra == nil {
			extra = map[string]any{}
		}
		extra["pool"] = map[string]any{
			"member_eth_address":  member.EthAddress,
			"member_display_name": member.DisplayName,
			"member_backend_id":   backend.ID,
			"payout_mode":         member.PayoutMode,
		}
		constraints := cloneMap(offer.Constraints)
		if constraints == nil {
			constraints = map[string]any{}
		}
		capabilities = append(capabilities, BrokerCapability{
			ID:         offer.CapabilityID,
			OfferingID: offer.OfferingID,
			Protocol:   offer.Protocol,
			Job:        job,
			WorkUnit:   config.NormalizeWorkUnit(offer.WorkUnit),
			Health:     config.Health{Probe: backend.HealthProbe},
			Price:      offer.Price,
			Backend: BrokerBackend{
				ID:        backend.ID,
				Transport: backend.Transport,
				URL:       backend.URL,
				Auth:      backend.Auth,
			},
			Extra:       extra,
			Constraints: constraints,
		})
	}
	for _, assignment := range input.TemplateAssignments {
		if assignment.State != types.TemplateAssignmentActive && assignment.State != types.TemplateAssignmentProbationary {
			continue
		}
		template, ok := templatesByID[assignment.TemplateID]
		if !ok || template.Status != types.TemplateStatusActive {
			continue
		}
		maxInFlight := assignment.MaxInFlight
		if maxInFlight == 0 {
			maxInFlight = template.MaxInFlightDefault
		}
		queueLimit := assignment.QueueLimit
		if queueLimit == 0 {
			queueLimit = template.QueueLimitDefault
		}
		offer, ok := activeOfferForTemplate(offersByID, template)
		if !ok {
			continue
		}
		job, warning, err := jobAxesForOffer(offer)
		if err != nil {
			return RenderResult{}, err
		}
		if warning != "" {
			warnings = appendWarning(warnings, warning)
		}
		hardware, ok := hardwareByID[assignment.HardwareUnitID]
		if !ok || (hardware.State != types.HardwareUnitActive && hardware.State != types.HardwareUnitProbationary) {
			continue
		}
		enrollment, ok := enrollmentsByID[assignment.HostEnrollmentID]
		if !ok || enrollment.Status == types.HostEnrollmentRevoked || enrollment.Status == types.HostEnrollmentRetired {
			continue
		}
		member, ok := poolMembersByID[assignment.MemberEthAddress]
		if !ok || member.Status != types.MemberStatusActive {
			continue
		}
		extra := cloneMap(offer.Extra)
		if extra == nil {
			extra = map[string]any{}
		}
		extra["pool"] = map[string]any{
			"member_eth_address":  member.EthAddress,
			"host_enrollment_id":  enrollment.ID,
			"hardware_unit_id":    hardware.ID,
			"gpu_uuid":            hardware.GPUUUID,
			"template_id":         template.ID,
			"template_assignment": assignment.ID,
			"payout_mode":         member.PayoutMode,
		}
		constraints := cloneMap(offer.Constraints)
		if constraints == nil {
			constraints = map[string]any{}
		}
		capabilities = append(capabilities, BrokerCapability{
			ID:         offer.CapabilityID,
			OfferingID: offer.OfferingID,
			Protocol:   offer.Protocol,
			Job:        job,
			WorkUnit:   config.NormalizeWorkUnit(offer.WorkUnit),
			Health:     agentBackendHealth(assignment.ID, offer),
			Price:      offer.Price,
			Backend: BrokerBackend{
				ID:                      assignment.ID,
				Transport:               "http",
				URL:                     agentBackendURL(assignment.ID, offer),
				Auth:                    config.AuthConfig{Method: "none"},
				HostEnrollmentID:        enrollment.ID,
				HardwareUnitID:          hardware.ID,
				GPUUUID:                 hardware.GPUUUID,
				TemplateID:              template.ID,
				WorkerSessionCredential: enrollment.BrokerSessionCredential,
				MaxInFlight:             maxInFlight,
				QueueLimit:              queueLimit,
			},
			Extra:       extra,
			Constraints: constraints,
		})
	}

	sort.Slice(capabilities, func(i, j int) bool {
		left := capabilities[i]
		right := capabilities[j]
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		if left.OfferingID != right.OfferingID {
			return left.OfferingID < right.OfferingID
		}
		leftPool, _ := left.Extra["pool"].(map[string]any)
		rightPool, _ := right.Extra["pool"].(map[string]any)
		leftMember, _ := leftPool["member_eth_address"].(string)
		rightMember, _ := rightPool["member_eth_address"].(string)
		if leftMember != rightMember {
			return leftMember < rightMember
		}
		leftBackend, _ := leftPool["member_backend_id"].(string)
		rightBackend, _ := rightPool["member_backend_id"].(string)
		return leftBackend < rightBackend
	})

	model := BrokerConfig{
		Identity:  input.Bootstrap.Identity,
		AdminAuth: input.Bootstrap.AdminAuth,
		Listen: BrokerListen{
			Paid:       input.Bootstrap.Listen.Paid,
			Metrics:    input.Bootstrap.Listen.Metrics,
			WorkerQUIC: input.Bootstrap.Listen.WorkerQUIC,
		},
		PaymentDaemon: input.Bootstrap.PaymentDaemon,
		ReceiptSink:   input.Bootstrap.ReceiptSink,
		Capabilities:  capabilities,
	}
	raw, err := yaml.Marshal(model)
	if err != nil {
		return RenderResult{}, fmt.Errorf("marshal broker config: %w", err)
	}
	sum := sha256.Sum256(raw)
	sort.Strings(warnings)
	return RenderResult{
		ConfigYAML: raw,
		Revision:   hex.EncodeToString(sum[:]),
		Model:      model,
		Warnings:   warnings,
	}, nil
}

// DefaultJobTransports is the transport set assumed for a paid-job offer
// that carries no transport information at all. Pool offers were authored
// against the removed v0 interaction-mode field, where the overwhelmingly
// common value was the plain request/response mode -- which
// is exactly `unary` in the v1 vocabulary. Rendering the wider set would
// advertise transports the member backend may not serve, so the renderer
// takes the narrowest safe option and reports the substitution through
// RenderResult.Warnings.
var DefaultJobTransports = []string{"unary"}

var validJobTransports = map[string]bool{
	"unary":     true,
	"stream":    true,
	"multipart": true,
}

// jobAxesForOffer derives the broker `job` block for an offer and reports a
// warning when it had to fall back to DefaultJobTransports.
//
// paid-session offerings are refused outright: pool-controller has no
// session-runner contract with its members (no descriptor_schema, no runner
// create/status/terminate paths) and no way to configure the broker-side
// session_store / sealing key / external_base_url those capabilities
// require. Emitting a session capability anyway would produce a host-config
// the broker refuses to load, taking every other pool capability down with
// it, so the render fails loudly on the offending offer instead.
func jobAxesForOffer(offer types.Offer) (*BrokerJobAxes, string, error) {
	protocol := strings.TrimSpace(offer.Protocol)
	switch {
	case protocol == "":
		return nil, "", fmt.Errorf("offer %q (%s/%s): protocol is required; expected a %s* tag",
			offer.ID, offer.CapabilityID, offer.OfferingID, types.ProtocolPaidJobPrefix)
	case offer.IsPaidSession():
		return nil, "", fmt.Errorf("offer %q (%s/%s) declares protocol %q: pool-controller cannot render paid-session capabilities "+
			"(the pool member contract carries no descriptor_schema or runner create/status/terminate paths, and pool-controller "+
			"configures neither external_base_url nor session_store); disable the offer or host it from a standalone broker config",
			offer.ID, offer.CapabilityID, offer.OfferingID, protocol)
	case !offer.IsPaidJob():
		return nil, "", fmt.Errorf("offer %q (%s/%s) declares unsupported protocol %q; pool backends serve %s* offerings only",
			offer.ID, offer.CapabilityID, offer.OfferingID, protocol, types.ProtocolPaidJobPrefix)
	}

	declared := offer.JobTransports()
	if len(declared) == 0 {
		return &BrokerJobAxes{Transports: append([]string(nil), DefaultJobTransports...)},
			fmt.Sprintf("offer %q (%s/%s) declares no job.transports; defaulted to %v",
				offer.ID, offer.CapabilityID, offer.OfferingID, DefaultJobTransports),
			nil
	}
	transports := make([]string, 0, len(declared))
	seen := make(map[string]bool, len(declared))
	for _, transport := range declared {
		if !validJobTransports[transport] {
			return nil, "", fmt.Errorf("offer %q (%s/%s): job transport %q must be unary|stream|multipart",
				offer.ID, offer.CapabilityID, offer.OfferingID, transport)
		}
		if seen[transport] {
			continue
		}
		seen[transport] = true
		transports = append(transports, transport)
	}
	return &BrokerJobAxes{Transports: transports}, "", nil
}

func appendWarning(warnings []string, warning string) []string {
	for _, existing := range warnings {
		if existing == warning {
			return warnings
		}
	}
	return append(warnings, warning)
}

// agentBackendHealth builds the broker health probe for a worker://-tunneled
// (connected-worker) backend. Without an explicit probe the broker defaults to
// probing the backend root (worker://<assignment-id>), which OpenAI-style
// runners answer with 404 -- leaving the backend permanently unreachable. Probe
// a real path (default /v1/models) that is forwarded over the worker session to
// the runner. Operators can override the path per offer via
// extra.health_probe_path (e.g. "/healthz" for the openai-chat-runner).
// agentBackendURL builds the worker://-tunneled backend URL the broker forwards
// paid requests to. http-reqresp forwards backend.URL verbatim (it does not
// append the inbound path), so the URL must carry the runner's endpoint path or
// the request lands on the backend root and 404s. The host stays the assignment
// id (the worker-session routing key); the path defaults to /v1/chat/completions
// and is overridable per offer via extra.backend_path.
func agentBackendURL(assignmentID string, offer types.Offer) string {
	path := "/v1/chat/completions"
	if raw, ok := offer.Extra["backend_path"].(string); ok && strings.TrimSpace(raw) != "" {
		path = strings.TrimSpace(raw)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	return "worker://" + assignmentID + path
}

func agentBackendHealth(assignmentID string, offer types.Offer) config.Health {
	path := "/v1/models"
	if raw, ok := offer.Extra["health_probe_path"].(string); ok && strings.TrimSpace(raw) != "" {
		path = strings.TrimSpace(raw)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	return config.Health{
		Probe: config.HealthProbe{
			Type:   "http-status",
			Config: map[string]any{"url": "worker://" + assignmentID + path},
		},
	}
}

func activeOfferForTemplate(offersByID map[string]types.Offer, template types.TemplateCatalogEntry) (types.Offer, bool) {
	for _, offer := range offersByID {
		if offer.Status != types.OfferStatusActive {
			continue
		}
		if offer.CapabilityID == template.CapabilityID && offer.OfferingID == template.OfferingID && offer.Protocol == template.Protocol {
			return offer, true
		}
	}
	return types.Offer{}, false
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
