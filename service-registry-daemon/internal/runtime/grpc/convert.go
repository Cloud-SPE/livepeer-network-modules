package grpc

import (
	"strings"
	"time"

	registryv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/registry/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/selection"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Conversion between domain types (internal/types) and proto messages
// (proto/gen/...). Pure functions; no I/O. Tested via round-trip and
// known-value cases in convert_test.go.

// resolveModeToProto maps a domain ResolveMode to its wire enum.
func resolveModeToProto(m types.ResolveMode) registryv1.ResolveMode {
	switch m {
	case types.ModeWellKnown:
		return registryv1.ResolveMode_RESOLVE_MODE_WELL_KNOWN
	case types.ModeCSV:
		return registryv1.ResolveMode_RESOLVE_MODE_CSV
	case types.ModeLegacy:
		return registryv1.ResolveMode_RESOLVE_MODE_LEGACY
	default:
		return registryv1.ResolveMode_RESOLVE_MODE_UNSPECIFIED
	}
}

// signatureStatusToProto maps a domain SignatureStatus.
func signatureStatusToProto(s types.SignatureStatus) registryv1.SignatureStatus {
	switch s {
	case types.SigVerified:
		return registryv1.SignatureStatus_SIGNATURE_STATUS_VERIFIED
	case types.SigUnsigned:
		return registryv1.SignatureStatus_SIGNATURE_STATUS_UNSIGNED
	case types.SigLegacy:
		return registryv1.SignatureStatus_SIGNATURE_STATUS_LEGACY
	default:
		return registryv1.SignatureStatus_SIGNATURE_STATUS_UNSPECIFIED
	}
}

// freshnessToProto maps a domain FreshnessStatus.
func freshnessToProto(f types.FreshnessStatus) registryv1.FreshnessStatus {
	switch f {
	case types.Fresh:
		return registryv1.FreshnessStatus_FRESHNESS_STATUS_FRESH
	case types.StaleRecoverable:
		return registryv1.FreshnessStatus_FRESHNESS_STATUS_STALE_RECOVERABLE
	case types.StaleFailing:
		return registryv1.FreshnessStatus_FRESHNESS_STATUS_STALE_FAILING
	default:
		return registryv1.FreshnessStatus_FRESHNESS_STATUS_UNSPECIFIED
	}
}

// sourceToProto maps a domain Source.
func sourceToProto(s types.Source) registryv1.Source {
	switch s {
	case types.SourceManifest:
		return registryv1.Source_SOURCE_MANIFEST
	case types.SourceLegacy:
		return registryv1.Source_SOURCE_LEGACY
	case types.SourceStaticOverlay:
		return registryv1.Source_SOURCE_STATIC_OVERLAY
	case types.SourceCSVFallback:
		return registryv1.Source_SOURCE_CSV_FALLBACK
	default:
		return registryv1.Source_SOURCE_UNSPECIFIED
	}
}

// sourceFromProto maps the wire enum back. Used by the publisher path
// where a consumer authored a Node in proto form.
func sourceFromProto(s registryv1.Source) types.Source {
	switch s {
	case registryv1.Source_SOURCE_MANIFEST:
		return types.SourceManifest
	case registryv1.Source_SOURCE_LEGACY:
		return types.SourceLegacy
	case registryv1.Source_SOURCE_STATIC_OVERLAY:
		return types.SourceStaticOverlay
	case registryv1.Source_SOURCE_CSV_FALLBACK:
		return types.SourceCSVFallback
	default:
		return types.SourceUnknown
	}
}

// capabilityToProto converts a domain Capability.
func capabilityToProto(c types.Capability) *registryv1.Capability {
	out := &registryv1.Capability{
		Name:     c.Name,
		WorkUnit: c.WorkUnit,
	}
	if len(c.Extra) > 0 {
		out.ExtraJson = append([]byte(nil), c.Extra...)
	}
	for _, o := range c.Offerings {
		out.Offerings = append(out.Offerings, offeringToProto(o))
	}
	return out
}

// capabilityFromProto reverses the conversion. Nil-safe.
func capabilityFromProto(p *registryv1.Capability) types.Capability {
	if p == nil {
		return types.Capability{}
	}
	c := types.Capability{Name: p.GetName(), WorkUnit: p.GetWorkUnit()}
	if x := p.GetExtraJson(); len(x) > 0 {
		c.Extra = append([]byte(nil), x...)
	}
	for _, op := range p.GetOfferings() {
		c.Offerings = append(c.Offerings, offeringFromProto(op))
	}
	return c
}

// offeringToProto converts a domain Offering.
func offeringToProto(o types.Offering) *registryv1.Offering {
	out := &registryv1.Offering{
		Id:                  o.ID,
		PricePerWorkUnitWei: o.PricePerWorkUnitWei,
	}
	if len(o.Constraints) > 0 {
		out.ConstraintsJson = append([]byte(nil), o.Constraints...)
	}
	return out
}

func offeringFromProto(p *registryv1.Offering) types.Offering {
	if p == nil {
		return types.Offering{}
	}
	out := types.Offering{
		ID:                  p.GetId(),
		PricePerWorkUnitWei: p.GetPricePerWorkUnitWei(),
	}
	if c := p.GetConstraintsJson(); len(c) > 0 {
		out.Constraints = append([]byte(nil), c...)
	}
	return out
}

// resolvedNodeToProto converts a single resolver-attached node to the
func resolvedNodeToProto(n types.ResolvedNode) *registryv1.Node {
	out := &registryv1.Node{
		Id:               n.ID,
		Url:              n.URL,
		WorkerEthAddress: n.WorkerEthAddress,
		Source:           sourceToProto(n.Source),
		SignatureStatus:  signatureStatusToProto(n.SignatureStatus),
		OperatorAddress:  string(n.OperatorAddr),
		Enabled:          n.Enabled,
		TierAllowed:      append([]string(nil), n.TierAllowed...),
		Weight:           int32(n.Weight),
	}
	if len(n.Extra) > 0 {
		out.ExtraJson = append([]byte(nil), n.Extra...)
	}
	for _, c := range n.Capabilities {
		out.Capabilities = append(out.Capabilities, capabilityToProto(c))
	}
	return out
}

// resolvedNodeFromProto reverses.
func resolvedNodeFromProto(p *registryv1.Node) types.ResolvedNode {
	if p == nil {
		return types.ResolvedNode{}
	}
	out := types.ResolvedNode{
		ID:               p.GetId(),
		URL:              p.GetUrl(),
		WorkerEthAddress: p.GetWorkerEthAddress(),
		Source:           sourceFromProto(p.GetSource()),
		Enabled:          p.GetEnabled(),
		Weight:           int(p.GetWeight()),
		TierAllowed:      append([]string(nil), p.GetTierAllowed()...),
		OperatorAddr:     types.EthAddress(p.GetOperatorAddress()),
		SignatureStatus:  signatureStatusFromProto(p.GetSignatureStatus()),
	}
	if x := p.GetExtraJson(); len(x) > 0 {
		out.Extra = append([]byte(nil), x...)
	}
	for _, c := range p.GetCapabilities() {
		out.Capabilities = append(out.Capabilities, capabilityFromProto(c))
	}
	return out
}

func selectedRouteToProto(r *SelectedRoute) *registryv1.SelectedRoute {
	if r == nil {
		return &registryv1.SelectedRoute{}
	}
	out := &registryv1.SelectedRoute{
		WorkerUrl:           r.WorkerURL,
		EthAddress:          r.EthAddress,
		Capability:          r.Capability,
		Offering:            r.Offering,
		PricePerWorkUnitWei: r.PricePerWorkUnitWei,
		WorkUnit:            r.WorkUnit,
	}
	if len(r.Extra) > 0 {
		out.ExtraJson = append([]byte(nil), r.Extra...)
	}
	if len(r.Constraints) > 0 {
		out.ConstraintsJson = append([]byte(nil), r.Constraints...)
	}
	return out
}

func selectedRouteFromResolvedNode(n types.ResolvedNode, f selection.Filter) (*SelectedRoute, error) {
	capability, offering, err := matchedCapabilityAndOffering(n, f)
	if err != nil {
		return nil, err
	}
	out := &SelectedRoute{
		WorkerURL:           n.URL,
		EthAddress:          string(n.OperatorAddr),
		Capability:          capability.Name,
		Offering:            offering.ID,
		PricePerWorkUnitWei: offering.PricePerWorkUnitWei,
		WorkUnit:            capability.WorkUnit,
	}
	if len(capability.Extra) > 0 {
		out.Extra = append([]byte(nil), capability.Extra...)
	}
	if len(offering.Constraints) > 0 {
		out.Constraints = append([]byte(nil), offering.Constraints...)
	}
	return out, nil
}

func matchedCapabilityAndOffering(n types.ResolvedNode, f selection.Filter) (types.Capability, types.Offering, error) {
	for _, capability := range n.Capabilities {
		if !strings.EqualFold(capability.Name, f.Capability) {
			continue
		}
		for _, offering := range capability.Offerings {
			if strings.EqualFold(offering.ID, f.Offering) {
				return capability, offering, nil
			}
		}
	}
	return types.Capability{}, types.Offering{}, types.NewValidation(types.ErrParse, "select", "matched node is missing requested capability/offering")
}

func signatureStatusFromProto(s registryv1.SignatureStatus) types.SignatureStatus {
	switch s {
	case registryv1.SignatureStatus_SIGNATURE_STATUS_VERIFIED:
		return types.SigVerified
	case registryv1.SignatureStatus_SIGNATURE_STATUS_UNSIGNED:
		return types.SigUnsigned
	case registryv1.SignatureStatus_SIGNATURE_STATUS_LEGACY:
		return types.SigLegacy
	default:
		return types.SigUnknown
	}
}

// resolveResultToProto packs ResolveByAddress's domain output into
// the wire response.
func resolveResultToProto(r *types.ResolveResult) *registryv1.ResolveResult {
	if r == nil {
		return &registryv1.ResolveResult{}
	}
	out := &registryv1.ResolveResult{
		EthAddress:      string(r.EthAddress),
		ResolvedUri:     r.ResolvedURI,
		Mode:            resolveModeToProto(r.Mode),
		FreshnessStatus: freshnessToProto(r.FreshnessStatus),
		SchemaVersion:   r.SchemaVersion,
		CachedAt:        timeToProto(r.CachedAt),
		FetchedAt:       timeToProto(r.FetchedAt),
	}
	for _, n := range r.Nodes {
		out.Nodes = append(out.Nodes, resolvedNodeToProto(n))
	}
	return out
}

// auditEventToProto converts a domain AuditEvent.
func auditEventToProto(e types.AuditEvent) *registryv1.AuditEvent {
	return &registryv1.AuditEvent{
		At:         timeToProto(e.At),
		EthAddress: string(e.EthAddress),
		Kind:       e.Kind.String(),
		Mode:       resolveModeToProto(e.Mode),
		Detail:     e.Detail,
	}
}

// timeToProto returns a *timestamppb.Timestamp for a time.Time, or nil
// if the time is the zero value (avoids encoding "epoch" in absent
// fields).
func timeToProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t.UTC())
}

// timeFromProto returns the time for a (possibly nil) Timestamp.
func timeFromProto(p *timestamppb.Timestamp) time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.AsTime().UTC()
}

// nodesFromProto converts a list of proto Nodes to types.Node (NOT
// ResolvedNode — the publisher BuildManifest takes the manifest-shape
// type, which has a thinner field set).
func nodesFromProto(ps []*registryv1.Node) []types.Node {
	out := make([]types.Node, 0, len(ps))
	for _, p := range ps {
		if p == nil {
			continue
		}
		n := types.Node{
			ID:               p.GetId(),
			URL:              p.GetUrl(),
			WorkerEthAddress: p.GetWorkerEthAddress(),
		}
		if x := p.GetExtraJson(); len(x) > 0 {
			n.Extra = append([]byte(nil), x...)
		}
		for _, c := range p.GetCapabilities() {
			n.Capabilities = append(n.Capabilities, capabilityFromProto(c))
		}
		out = append(out, n)
	}
	return out
}
