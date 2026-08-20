package grpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/publisher"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/selection"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

// Server is the runtime entrypoint for the gRPC surface. It hosts
// optional Resolver and Publisher handlers; presence is determined by
// daemon mode at construction.
type Server struct {
	resolverSvc  *resolver.Service
	publisherSvc *publisher.Service
	cache        manifestcache.Repo
	audit        audit.Repo
	log          logger.Logger
}

// Config wires the server.
type Config struct {
	Resolver  *resolver.Service  // nil in publisher mode
	Publisher *publisher.Service // nil in resolver mode
	Cache     manifestcache.Repo
	Audit     audit.Repo
	Logger    logger.Logger
}

// NewServer constructs a Server. Returns an error if neither service
// is provided (the daemon must mount at least one).
func NewServer(c Config) (*Server, error) {
	if c.Resolver == nil && c.Publisher == nil {
		return nil, errors.New("grpc: at least one of Resolver or Publisher must be provided")
	}
	if c.Logger == nil {
		c.Logger = logger.Discard()
	}
	return &Server{
		resolverSvc:  c.Resolver,
		publisherSvc: c.Publisher,
		cache:        c.Cache,
		audit:        c.Audit,
		log:          c.Logger,
	}, nil
}

// HasResolver reports whether resolver RPCs are available.
func (s *Server) HasResolver() bool { return s.resolverSvc != nil }

// HasPublisher reports whether publisher RPCs are available.
func (s *Server) HasPublisher() bool { return s.publisherSvc != nil }

// ----- Resolver-side RPCs -----

// ResolveByAddress dispatches to the resolver service.
func (s *Server) ResolveByAddress(ctx context.Context, req ResolveByAddressRequest) (*types.ResolveResult, error) {
	if s.resolverSvc == nil {
		return nil, errors.New("grpc: resolver not mounted")
	}
	addr, err := types.ParseEthAddress(req.EthAddress)
	if err != nil {
		return nil, err
	}
	return s.resolverSvc.ResolveByAddress(ctx, resolver.Request{
		Address:             addr,
		AllowLegacyFallback: req.AllowLegacyFallback,
		AllowUnsigned:       req.AllowUnsigned,
		ForceRefresh:        req.ForceRefresh,
	})
}

// Select runs the selection filter across all currently-known
// orchestrators in the cache, ranks the matches, and returns the
// single top route the gateway should dispatch to.
func (s *Server) Select(ctx context.Context, req SelectRequest) (*SelectedRoute, error) {
	routes, err := s.SelectMany(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("%w: no route for capability=%q offering=%q", types.ErrNotFound, req.Capability, req.Offering)
	}
	return routes[0], nil
}

// SelectMany runs the selection filter across all currently-known
// orchestrators in the cache, ranks the matches, and returns all
// payment-ready routes in resolver order for gateway-side failover.
func (s *Server) SelectMany(ctx context.Context, req SelectRequest) ([]*SelectedRoute, error) {
	if s.resolverSvc == nil {
		return nil, errors.New("grpc: resolver not mounted")
	}
	if req.Capability == "" {
		return nil, types.NewValidation(types.ErrParse, "select.capability", "required")
	}
	if req.Offering == "" {
		return nil, types.NewValidation(types.ErrParse, "select.offering", "required")
	}
	addrs, err := s.cache.List()
	if err != nil {
		return nil, err
	}
	s.log.Info("resolver select_many start",
		"capability", req.Capability,
		"offering", req.Offering,
		"tier", req.Tier,
		"min_weight", req.MinWeight,
		"known_address_count", len(addrs),
	)
	all := make([]types.ResolvedNode, 0, len(addrs)*2)
	resolvedAddressCount := 0
	zeroNodeAddressCount := 0
	skippedAddressCount := 0
	for _, addr := range addrs {
		res, err := s.resolverSvc.ResolveByAddress(ctx, resolver.Request{
			Capability:          req.Capability,
			Offering:            req.Offering,
			Address:             addr,
			AllowLegacyFallback: true,
			AllowUnsigned:       true, // Select trusts caller; signature filtering done server-side via overlay
		})
		if err != nil {
			skippedAddressCount++
			s.log.Warn("resolver select_many address skipped before filtering",
				"addr", addr,
				"capability", req.Capability,
				"offering", req.Offering,
				"err", err,
			)
			continue
		}
		resolvedAddressCount++
		if len(res.Nodes) == 0 {
			zeroNodeAddressCount++
			s.log.Warn("resolver select_many address resolved zero nodes",
				"addr", addr,
				"capability", req.Capability,
				"offering", req.Offering,
				"mode", res.Mode,
				"freshness_status", res.FreshnessStatus,
			)
			continue
		}
		s.log.Debug("resolver select_many address resolved nodes",
			"addr", addr,
			"capability", req.Capability,
			"offering", req.Offering,
			"mode", res.Mode,
			"freshness_status", res.FreshnessStatus,
			"node_count", len(res.Nodes),
			"node_examples", summarizeResolvedNodes(res.Nodes, 3),
		)
		all = append(all, res.Nodes...)
	}
	filter := selection.Filter{
		Capability: req.Capability,
		Offering:   req.Offering,
		Tier:       req.Tier,
		MinWeight:  req.MinWeight,
	}
	matches := selection.Apply(all, filter)
	s.log.Info("resolver select_many after selection",
		"capability", req.Capability,
		"offering", req.Offering,
		"known_address_count", len(addrs),
		"resolved_address_count", resolvedAddressCount,
		"zero_node_address_count", zeroNodeAddressCount,
		"skipped_address_count", skippedAddressCount,
		"candidate_node_count", len(all),
		"matched_node_count", len(matches),
		"matched_node_examples", summarizeResolvedNodes(matches, 5),
	)
	if len(matches) == 0 {
		s.log.Warn("resolver select_many no selectable routes after filtering",
			"capability", req.Capability,
			"offering", req.Offering,
			"tier", req.Tier,
			"min_weight", req.MinWeight,
			"known_address_count", len(addrs),
			"resolved_address_count", resolvedAddressCount,
			"zero_node_address_count", zeroNodeAddressCount,
			"skipped_address_count", skippedAddressCount,
			"candidate_node_count", len(all),
			"candidate_node_examples", summarizeResolvedNodes(all, 5),
		)
		return nil, fmt.Errorf("%w: no route for capability=%q offering=%q", types.ErrNotFound, req.Capability, req.Offering)
	}
	routes := make([]*SelectedRoute, 0, len(matches))
	for _, match := range matches {
		route, err := selectedRouteFromResolvedNode(match, filter)
		if err != nil {
			s.log.Warn("resolver select_many matched node failed payment-ready route construction",
				"capability", req.Capability,
				"offering", req.Offering,
				"node_id", match.ID,
				"worker_url", match.URL,
				"node_examples", summarizeResolvedNodes([]types.ResolvedNode{match}, 1),
				"err", err,
			)
			return nil, err
		}
		routes = append(routes, route)
	}
	s.log.Info("resolver select_many success",
		"capability", req.Capability,
		"offering", req.Offering,
		"route_count", len(routes),
		"route_examples", summarizeSelectedRoutes(routes, 5),
	)
	return routes, nil
}

func summarizeResolvedNodes(nodes []types.ResolvedNode, max int) []string {
	if len(nodes) == 0 || max <= 0 {
		return nil
	}
	if len(nodes) < max {
		max = len(nodes)
	}
	out := make([]string, 0, max)
	for _, n := range nodes[:max] {
		tuples := make([]string, 0, 3)
		for _, c := range n.Capabilities {
			if len(tuples) >= 3 {
				break
			}
			if len(c.Offerings) == 0 {
				tuples = append(tuples, c.Name)
				continue
			}
			for _, o := range c.Offerings {
				tuples = append(tuples, c.Name+"|"+o.ID)
				if len(tuples) >= 3 {
					break
				}
			}
		}
		out = append(out, fmt.Sprintf("%s|%s|%s", n.ID, n.URL, strings.Join(tuples, ",")))
	}
	return out
}

func summarizeSelectedRoutes(routes []*SelectedRoute, max int) []string {
	if len(routes) == 0 || max <= 0 {
		return nil
	}
	if len(routes) < max {
		max = len(routes)
	}
	out := make([]string, 0, max)
	for _, r := range routes[:max] {
		out = append(out, fmt.Sprintf("%s|%s|%s|%s", r.WorkerURL, r.Capability, r.Offering, r.PricePerWorkUnitWei))
	}
	return out
}

// ListKnown returns all eth addresses currently in the cache, with
// freshness status.
func (s *Server) ListKnown(_ context.Context) ([]KnownEntry, error) {
	addrs, err := s.cache.List()
	if err != nil {
		return nil, err
	}
	out := make([]KnownEntry, 0, len(addrs))
	for _, addr := range addrs {
		e, ok, err := s.cache.Get(addr)
		if err != nil || !ok {
			continue
		}
		out = append(out, KnownEntry{
			EthAddress: addr,
			Mode:       e.Mode,
			CachedAt:   e.FetchedAt,
		})
	}
	return out, nil
}

// Refresh forces a re-fetch.
func (s *Server) Refresh(ctx context.Context, req RefreshRequest) error {
	if s.resolverSvc == nil {
		return errors.New("grpc: resolver not mounted")
	}
	if req.EthAddress == "*" {
		addrs, err := s.cache.List()
		if err != nil {
			return err
		}
		for _, addr := range addrs {
			_, _ = s.resolverSvc.ResolveByAddress(ctx, resolver.Request{Address: addr, ForceRefresh: req.Force, AllowLegacyFallback: true})
		}
		return nil
	}
	addr, err := types.ParseEthAddress(req.EthAddress)
	if err != nil {
		return err
	}
	_, err = s.resolverSvc.ResolveByAddress(ctx, resolver.Request{Address: addr, ForceRefresh: req.Force, AllowLegacyFallback: true})
	return err
}

// GetAuditLog returns recent audit events for an address.
func (s *Server) GetAuditLog(_ context.Context, req GetAuditLogRequest) ([]types.AuditEvent, error) {
	if s.audit == nil {
		return nil, nil
	}
	addr, err := types.ParseEthAddress(req.EthAddress)
	if err != nil {
		return nil, err
	}
	return s.audit.Query(addr, req.Since, int(req.Limit))
}

// ----- Publisher-side RPCs -----

// BuildManifest constructs an unsigned manifest from spec.
func (s *Server) BuildManifest(_ context.Context, spec publisher.BuildSpec) (*types.Manifest, error) {
	if s.publisherSvc == nil {
		return nil, errors.New("grpc: publisher not mounted")
	}
	return s.publisherSvc.BuildManifest(spec)
}

// GetIdentity returns the loaded publisher cold-key identity.
func (s *Server) GetIdentity(_ context.Context) (types.EthAddress, error) {
	if s.publisherSvc == nil {
		return "", errors.New("grpc: publisher not mounted")
	}
	return s.publisherSvc.Identity()
}

// SignManifest signs an in-memory manifest produced by BuildManifest.
//
// Note: we accept the typed *types.Manifest struct here rather than
// a JSON-bytes wire form because DecodeManifest requires a full
// signature for validation, and BuildManifest output is intentionally
// unsigned. The gRPC adapter (added under `make proto`) is responsible
// for translating the wire form to/from this struct. See
// docs/exec-plans/active/0001-repo-scaffold.md for the wiring plan.
func (s *Server) SignManifest(_ context.Context, m *types.Manifest) (*types.Manifest, error) {
	if s.publisherSvc == nil {
		return nil, errors.New("grpc: publisher not mounted")
	}
	if m == nil {
		return nil, errors.New("grpc: nil manifest")
	}
	return s.publisherSvc.SignManifest(m)
}

// BuildAndSign is the one-shot Build+Sign path used by
// livepeer-registry-refresh. Output is byte-identical to BuildManifest
// followed by SignManifest.
func (s *Server) BuildAndSign(_ context.Context, spec publisher.BuildSpec) (*types.Manifest, error) {
	if s.publisherSvc == nil {
		return nil, errors.New("grpc: publisher not mounted")
	}
	return s.publisherSvc.BuildAndSign(spec)
}

// Health returns a coarse aliveness status.
func (s *Server) Health(_ context.Context) HealthResult {
	mode := "resolver"
	if s.HasPublisher() {
		mode = "publisher"
	}
	cacheSize := 0
	if s.cache != nil {
		if list, err := s.cache.List(); err == nil {
			cacheSize = len(list)
		}
	}
	return HealthResult{
		Mode:              mode,
		ChainOK:           true, // placeholder; v1 doesn't actively probe
		ManifestFetcherOK: true,
		CacheSize:         cacheSize,
		LastChainSuccess:  time.Now().UTC(),
	}
}

// ----- Local request/response shapes (Go-native, decoupled from proto) -----

// ResolveByAddressRequest mirrors the proto message.
type ResolveByAddressRequest struct {
	EthAddress          string
	AllowLegacyFallback bool
	AllowUnsigned       bool
	ForceRefresh        bool
}

// SelectRequest mirrors the proto message.
type SelectRequest struct {
	Capability string
	Offering   string
	Tier       string
	MinWeight  int
}

// SelectedRoute is the gateway-facing result of Resolver.Select. It is
// narrower than ResolvedNode on purpose: gateways get only the route
// fields they need for dispatch, pricing, and payment.
type SelectedRoute struct {
	WorkerURL  string
	EthAddress string
	Capability string
	// Protocol is the signed tuple's protocol tag. Typed, not a key in
	// Extra, because consumers gate their open path on it.
	Protocol              string
	Offering              string
	PricePerWorkUnitWei   string
	WorkUnit              string
	Extra                 []byte
	Constraints           []byte
	QuoteID               string
	QuoteVersion          uint64
	ConstraintFingerprint []byte
	RouteFingerprint      []byte
	UnitsPerPrice         uint64
}

// KnownEntry mirrors the proto message.
type KnownEntry struct {
	EthAddress types.EthAddress
	Mode       types.ResolveMode
	CachedAt   time.Time
}

// RefreshRequest mirrors the proto message.
type RefreshRequest struct {
	EthAddress string
	Force      bool
}

// GetAuditLogRequest mirrors the proto message.
type GetAuditLogRequest struct {
	EthAddress string
	Since      time.Time
	Limit      int32
}

// HealthResult mirrors the proto message.
type HealthResult struct {
	Mode              string
	ChainOK           bool
	ManifestFetcherOK bool
	CacheSize         int
	LastChainSuccess  time.Time
}

// String for HealthResult — small affordance for human-readable logs.
func (h HealthResult) String() string {
	return fmt.Sprintf("mode=%s chain_ok=%v fetcher_ok=%v cache=%d", h.Mode, h.ChainOK, h.ManifestFetcherOK, h.CacheSize)
}
