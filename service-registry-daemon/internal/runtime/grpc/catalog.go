package grpc

import (
	"context"
	"strings"

	registryv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/registry/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/selection"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

func discoveryStatusToProto(s types.DiscoveryStatus) *registryv1.DiscoveryStatus {
	compatibility := registryv1.ManifestCompatibility(registryv1.ManifestCompatibility_value["MANIFEST_COMPATIBILITY_"+strings.ToUpper(string(s.Compatibility))])
	if compatibility == registryv1.ManifestCompatibility_MANIFEST_COMPATIBILITY_UNSPECIFIED {
		compatibility = registryv1.ManifestCompatibility_MANIFEST_COMPATIBILITY_UNKNOWN
	}
	availability := registryv1.ManifestAvailability(registryv1.ManifestAvailability_value["MANIFEST_AVAILABILITY_"+strings.ToUpper(s.Availability)])
	if availability == registryv1.ManifestAvailability_MANIFEST_AVAILABILITY_UNSPECIFIED {
		availability = registryv1.ManifestAvailability_MANIFEST_AVAILABILITY_UNKNOWN
	}
	return &registryv1.DiscoveryStatus{
		SourceUri: s.SourceURI, Compatibility: compatibility, Availability: availability,
		FailureReason:       registryv1.DiscoveryFailureReason(registryv1.DiscoveryFailureReason_value["DISCOVERY_FAILURE_REASON_"+strings.ToUpper(string(s.FailureReason))]),
		ConsecutiveFailures: s.ConsecutiveFailures, PolicyStep: s.PolicyStep, RetryClass: s.RetryClass,
		LastVerifiedAt: timeToProto(s.LastVerifiedAt), NextRetryAt: timeToProto(s.NextRetryAt), LastAttemptAt: timeToProto(s.LastAttemptAt), CompatibilityCheckedAt: timeToProto(s.CompatibilityCheckedAt), CompatibilityValidUntil: timeToProto(s.CompatibilityValidUntil), SourceCheckedAt: timeToProto(s.SourceCheckedAt),
	}
}
func (a *resolverAdapter) ListOfferings(ctx context.Context, req *registryv1.ListOfferingsRequest) (*registryv1.ListOfferingsResult, error) {
	filter := selection.Filter{Capability: req.GetCapability(), Offering: req.GetOffering(), Tier: req.GetTier(), MinWeight: int(req.GetMinWeight())}
	catalog, err := a.srv.resolverSvc.ListOfferings(ctx, filter, req.GetIncludeExpired())
	if err != nil {
		return nil, errorToStatus(err)
	}
	out := &registryv1.ListOfferingsResult{
		Completeness: registryv1.CatalogCompleteness(registryv1.CatalogCompleteness_value["CATALOG_COMPLETENESS_"+strings.ToUpper(catalog.Completeness)]),
		Coverage:     &registryv1.CatalogCoverage{KnownAddresses: catalog.Known, VerifiedCompatibleAddresses: catalog.Verified, ConfirmedIncompatibleAddresses: catalog.Incompatible, UnknownAddresses: catalog.Unknown, ExpiredAddresses: catalog.Expired, DeferredAddresses: catalog.Deferred, UnavailableAddresses: catalog.Unavailable},
		SnapshotAt:   timeToProto(catalog.SnapshotAt), EvaluatedAt: timeToProto(catalog.EvaluatedAt), DiscoveryScope: catalog.DiscoveryScope, DiscoveryScopeAuthoritative: catalog.ScopeAuthoritative, DiscoveryObservedAt: timeToProto(catalog.DiscoveryObservedAt), DiscoveryValidUntil: timeToProto(catalog.DiscoveryValidUntil), CoverageValidUntil: timeToProto(catalog.CoverageValidUntil),
	}
	for _, entry := range catalog.Entries {
		route, err := selectedRouteFromResolvedNode(entry.Node, selection.Filter{Capability: entry.Node.Capabilities[0].Name, Offering: entry.Node.Capabilities[0].Offerings[0].ID})
		if err != nil {
			return nil, errorToStatus(err)
		}
		out.Entries = append(out.Entries, &registryv1.CatalogOffering{Offering: selectedRouteToProto(route), WorkerId: entry.Node.ID, Selectable: entry.Selectable, ExclusionReason: entry.ExclusionReason, VerifiedAt: timeToProto(entry.VerifiedAt), ValidUntil: timeToProto(entry.ValidUntil), HealthValidUntil: timeToProto(entry.HealthValidUntil), Expired: entry.Expired})
	}
	return out, nil
}
