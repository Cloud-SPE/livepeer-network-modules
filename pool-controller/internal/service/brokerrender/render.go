package brokerrender

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

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
}

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
	ID              string          `yaml:"id"`
	OfferingID      string          `yaml:"offering_id"`
	InteractionMode string          `yaml:"interaction_mode"`
	WorkUnit        config.WorkUnit `yaml:"work_unit"`
	Health          config.Health   `yaml:"health,omitempty"`
	Price           config.Price    `yaml:"price"`
	Backend         BrokerBackend   `yaml:"backend"`
	Extra           map[string]any  `yaml:"extra,omitempty"`
	// Constraints is always rendered (no omitempty) so that downstream
	// resolvers can fingerprint a stable shape even when the operator
	// declared no constraints. An empty map renders as `constraints: {}`.
	Constraints map[string]any `yaml:"constraints"`
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
	for _, assignment := range input.Assignments {
		if assignment.Status != types.AssignmentStatusActive {
			continue
		}
		offer, ok := offersByID[assignment.OfferID]
		if !ok || offer.Status != types.OfferStatusActive {
			continue
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
			ID:              offer.CapabilityID,
			OfferingID:      offer.OfferingID,
			InteractionMode: offer.InteractionMode,
			WorkUnit:        config.NormalizeWorkUnit(offer.WorkUnit),
			Health:          config.Health{Probe: backend.HealthProbe},
			Price:           offer.Price,
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
			ID:              offer.CapabilityID,
			OfferingID:      offer.OfferingID,
			InteractionMode: offer.InteractionMode,
			WorkUnit:        config.NormalizeWorkUnit(offer.WorkUnit),
			Health:          config.Health{},
			Price:           offer.Price,
			Backend: BrokerBackend{
				ID:                      assignment.ID,
				Transport:               "http",
				URL:                     "worker://" + assignment.ID,
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
	return RenderResult{
		ConfigYAML: raw,
		Revision:   hex.EncodeToString(sum[:]),
		Model:      model,
	}, nil
}

func activeOfferForTemplate(offersByID map[string]types.Offer, template types.TemplateCatalogEntry) (types.Offer, bool) {
	for _, offer := range offersByID {
		if offer.Status != types.OfferStatusActive {
			continue
		}
		if offer.CapabilityID == template.CapabilityID && offer.OfferingID == template.OfferingID && offer.InteractionMode == template.InteractionMode {
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
