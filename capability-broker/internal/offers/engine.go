// Package offers is the broker's offer engine (plan 0043 §3.4; protocols/
// broker-admin.md §2, §4; protocols/runner-attach.md §5).
//
// It holds the operator's offers, matches attached runners to them,
// freezes the first certified shape, and decides eligibility from then
// on. The invariant it protects: nothing a runner sends changes an
// offer. A runner is matched, certified, and then either equals the
// frozen shape (eligible) or does not (ineligible). Only an explicit
// accept-shape moves a freeze.
//
// Certification execution lives elsewhere (plan 0043 item 9); the engine
// asks a Certifier and records the answer.
package offers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
)

// Offer states (broker-admin §2).
const (
	OfferUnfrozen    = "unfrozen"
	OfferFrozen      = "frozen"
	OfferSuperseding = "superseding"
	OfferDisabled    = "disabled"
)

// Runner × offer states.
const (
	PairAttached   = "attached"
	PairMatched    = "matched"
	PairCertified  = "certified"
	PairEligible   = "eligible"
	PairIneligible = "ineligible"
)

// CertOutcome is what a Certifier reports for a matched pair.
type CertOutcome struct {
	Passed  bool
	Pending bool // running or not yet run; the pair stays matched
	RunID   string
	State   string // passed | failed | running | error
	Reason  *runnerattach.Reason
	At      time.Time
}

// Certifier decides whether a matched runner passes an offer's steps.
// The default certifier passes offers with no steps and leaves the rest
// pending, which is exactly "certify on match" (certification-steps
// §2) until the engine ships.
type Certifier interface {
	Certify(host runners.Snapshot, cap *runnerattach.Capability, offer config.Offer) CertOutcome
}

// DefaultCertifier: empty steps pass; anything else is pending.
type DefaultCertifier struct{}

func (DefaultCertifier) Certify(_ runners.Snapshot, _ *runnerattach.Capability, offer config.Offer) CertOutcome {
	if len(offer.Certification) == 0 {
		return CertOutcome{Passed: true, State: "passed", At: time.Now().UTC()}
	}
	return CertOutcome{Pending: true, State: "running"}
}

// Frozen is a persisted frozen shape.
type Frozen struct {
	ShapeHash  string                  `json:"shape_hash"`
	Projection runnerattach.Projection `json:"projection"`
	FrozenAt   time.Time               `json:"frozen_at"`
	FrozenBy   FrozenBy                `json:"frozen_by"`
	// SessionParamsSchema is carried from the freezing runner so the
	// advertised tuple can relay it (paid-session offers).
	SessionParamsSchema json.RawMessage `json:"session_params_schema,omitempty"`
	// Heartbeat is the freezing runner's advisory cadence, advertised
	// when the operator's policy leaves it unset.
	HeartbeatIntervalSeconds int `json:"heartbeat_interval_seconds,omitempty"`
}

// FrozenBy names the runner and run that froze the shape.
type FrozenBy struct {
	HostID  string `json:"host_id"`
	LocalID string `json:"local_id"`
	RunID   string `json:"run_id,omitempty"`
}

// OfferState is what persists per offer.
type OfferState struct {
	Frozen *Frozen `json:"frozen,omitempty"`
	// Pending is an accepted-but-unpublished shape (state superseding).
	Pending  *Frozen `json:"pending,omitempty"`
	Disabled bool    `json:"disabled,omitempty"`
}

// persisted is the on-disk file.
type persisted struct {
	Revision string                 `json:"offers_revision,omitempty"`
	Offers   []config.Offer         `json:"offers,omitempty"` // only when source is admin
	States   map[string]*OfferState `json:"states"`
}

// PairKey identifies runner capability × offer.
type PairKey struct {
	HostID, LocalID, OfferingID string
}

// Pair is the live state of one runner capability against one offer.
type Pair struct {
	Key       PairKey
	State     string
	Since     time.Time
	Reason    *runnerattach.Reason
	Cert      *CertOutcome
	ShapeHash string
}

// Candidate is a certified shape that disagrees with the frozen one.
type Candidate struct {
	ShapeHash  string
	Projection runnerattach.Projection
	FirstSeen  time.Time
	Runners    []FrozenBy
	Diff       []runnerattach.Reason
	sample     *runnerattach.Capability
}

// Engine is the offer engine.
type Engine struct {
	mu        sync.RWMutex
	source    string
	offers    map[string]config.Offer // by offering_id
	states    map[string]*OfferState
	pairs     map[PairKey]*Pair
	revision  string
	statePath string
	certifier Certifier
	registry  *runners.Registry
	now       func() time.Time
	// OnAdvertisedChange fires when /registry/offerings content changed.
	OnAdvertisedChange func()
}

// New constructs an engine over the config's offers. statePath may be
// empty (state is then in-memory; a warning is the caller's business).
func New(cfg *config.Config, reg *runners.Registry, statePath string, certifier Certifier) (*Engine, error) {
	if certifier == nil {
		certifier = DefaultCertifier{}
	}
	e := &Engine{
		source: cfg.OffersSource, offers: map[string]config.Offer{}, states: map[string]*OfferState{},
		pairs: map[PairKey]*Pair{}, statePath: statePath, certifier: certifier, registry: reg,
		now: func() time.Time { return time.Now().UTC() },
	}
	if err := e.load(); err != nil {
		return nil, err
	}
	if cfg.OffersSource != config.OffersSourceAdmin {
		e.setOffersLocked(cfg.Offers, "")
	}
	for id := range e.offers {
		if e.states[id] == nil {
			e.states[id] = &OfferState{}
		}
	}
	return e, nil
}

// --- persistence ------------------------------------------------------------

func (e *Engine) load() error {
	if e.statePath == "" {
		return nil
	}
	raw, err := os.ReadFile(e.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("offers: read state %s: %w", e.statePath, err)
	}
	var p persisted
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("offers: decode state %s: %w", e.statePath, err)
	}
	e.states = p.States
	if e.states == nil {
		e.states = map[string]*OfferState{}
	}
	e.revision = p.Revision
	if e.source == config.OffersSourceAdmin {
		e.setOffersLocked(p.Offers, p.Revision)
	}
	return nil
}

func (e *Engine) persistLocked() error {
	if e.statePath == "" {
		return nil
	}
	p := persisted{Revision: e.revision, States: e.states}
	if e.source == config.OffersSourceAdmin {
		for _, o := range e.offers {
			p.Offers = append(p.Offers, o)
		}
		sort.Slice(p.Offers, func(i, j int) bool { return p.Offers[i].OfferingID < p.Offers[j].OfferingID })
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := e.statePath + ".tmp"
	if err := os.MkdirAll(filepath.Dir(e.statePath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.statePath)
}

func (e *Engine) setOffersLocked(list []config.Offer, revision string) {
	next := make(map[string]config.Offer, len(list))
	for _, o := range list {
		next[o.OfferingID] = o
	}
	for id := range e.offers {
		if _, keep := next[id]; !keep {
			delete(e.states, id)
			for k := range e.pairs {
				if k.OfferingID == id {
					delete(e.pairs, k)
				}
			}
		}
	}
	e.offers = next
	for id := range next {
		if e.states[id] == nil {
			e.states[id] = &OfferState{}
		}
	}
	if revision != "" {
		e.revision = revision
	}
}

// --- operator surfaces -------------------------------------------------------

// Reload replaces file-sourced offers after a config reload.
func (e *Engine) Reload(cfg *config.Config) error {
	if e.source == config.OffersSourceAdmin {
		return nil
	}
	e.mu.Lock()
	e.setOffersLocked(cfg.Offers, "")
	err := e.persistLocked()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.Rematch("")
	return nil
}

// ErrSourceIsFile is PUT /offers on a file-sourced broker.
var ErrSourceIsFile = errors.New("offers come from host-config (offers_source: file)")

// Push replaces the admin-sourced offer set (broker-admin §4.2).
// Returns the ids whose operator fields changed.
func (e *Engine) Push(revision string, list []config.Offer) ([]string, error) {
	if e.source != config.OffersSourceAdmin {
		return nil, ErrSourceIsFile
	}
	e.mu.Lock()
	var changed []string
	next := map[string]bool{}
	for _, o := range list {
		next[o.OfferingID] = true
		prev, had := e.offers[o.OfferingID]
		if !had || !sameJSON(prev, o) {
			changed = append(changed, o.OfferingID)
		}
	}
	for id := range e.offers {
		if !next[id] {
			changed = append(changed, id)
		}
	}
	sort.Strings(changed)
	if len(changed) == 0 && e.revision == revision {
		e.mu.Unlock()
		return []string{}, nil
	}
	e.setOffersLocked(list, revision)
	err := e.persistLocked()
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	e.Rematch("")
	return changed, nil
}

// SourceIsAdmin reports whether offers come from admin pushes.
func (e *Engine) SourceIsAdmin() bool { return e.source == config.OffersSourceAdmin }

// Revision is the applied offers revision.
func (e *Engine) Revision() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.revision
}

// ErrNotFound / ErrNotCandidate for admin routes.
var (
	ErrNotFound     = errors.New("offer not found")
	ErrNotCandidate = errors.New("shape is not a current candidate")
)

// AcceptShape supersedes the frozen shape with a candidate (§4.3).
func (e *Engine) AcceptShape(offeringID, shapeHash string) (eligibleNow, ineligibleNow int, err error) {
	e.mu.Lock()
	st, ok := e.states[offeringID]
	if !ok {
		e.mu.Unlock()
		return 0, 0, ErrNotFound
	}
	cands := e.candidatesLocked(offeringID)
	var chosen *Candidate
	for i := range cands {
		if cands[i].ShapeHash == shapeHash {
			chosen = &cands[i]
		}
	}
	if chosen == nil {
		e.mu.Unlock()
		return 0, 0, ErrNotCandidate
	}
	fz := &Frozen{ShapeHash: chosen.ShapeHash, Projection: chosen.Projection, FrozenAt: e.now(), FrozenBy: chosen.Runners[0]}
	if chosen.sample != nil {
		fz.SessionParamsSchema = chosen.sample.SessionParamsSchema
		if chosen.sample.Heartbeat != nil {
			fz.HeartbeatIntervalSeconds = chosen.sample.Heartbeat.IntervalSeconds
		}
	}
	// The previously frozen (published) shape keeps serving paid work
	// until the sign lands; the accepted one is what /registry/offerings
	// now carries. Both are kept: Pending is advertised, Frozen is served.
	st.Pending = fz
	err = e.persistLocked()
	e.mu.Unlock()
	if err != nil {
		return 0, 0, err
	}
	e.Rematch("")
	e.mu.RLock()
	for k, p := range e.pairs {
		if k.OfferingID != offeringID {
			continue
		}
		switch p.State {
		case PairEligible:
			eligibleNow++
		case PairIneligible:
			ineligibleNow++
		}
	}
	e.mu.RUnlock()
	if e.OnAdvertisedChange != nil {
		e.OnAdvertisedChange()
	}
	return eligibleNow, ineligibleNow, nil
}

// ConfirmPublished reports that the signed manifest now carries
// shapeHash; a pending shape with that hash becomes the frozen one.
func (e *Engine) ConfirmPublished(offeringID, shapeHash string) error {
	e.mu.Lock()
	st, ok := e.states[offeringID]
	if !ok {
		e.mu.Unlock()
		return ErrNotFound
	}
	if st.Pending != nil && st.Pending.ShapeHash == shapeHash {
		st.Frozen, st.Pending = st.Pending, nil
	}
	err := e.persistLocked()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.Rematch("")
	return nil
}

// SetDisabled toggles an offer (§4.4).
func (e *Engine) SetDisabled(offeringID string, disabled bool) error {
	e.mu.Lock()
	st, ok := e.states[offeringID]
	if !ok {
		e.mu.Unlock()
		return ErrNotFound
	}
	st.Disabled = disabled
	err := e.persistLocked()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if e.OnAdvertisedChange != nil {
		e.OnAdvertisedChange()
	}
	return nil
}

// --- matching -----------------------------------------------------------------

// Rematch re-evaluates every pair (hostID empty) or one host's pairs.
// Called by the runner registry on attach/detach and by the certifier
// when a run finishes.
func (e *Engine) Rematch(hostID string) {
	if e.registry == nil {
		return
	}
	var hosts []runners.Snapshot
	if hostID == "" {
		hosts = e.registry.List()
	} else if sn, ok := e.registry.Get(hostID); ok {
		hosts = []runners.Snapshot{sn}
	}
	changed := false
	e.mu.Lock()
	// Drop pairs for hosts that are gone or capabilities no longer declared.
	live := map[PairKey]bool{}
	for _, sn := range hosts {
		if sn.State != "connected" {
			continue
		}
		for _, cv := range sn.Capabilities {
			if cv.Capability == nil {
				continue
			}
			for id, offer := range e.offers {
				if !matches(offer, cv.Capability) {
					continue
				}
				key := PairKey{HostID: sn.HostID, LocalID: cv.Capability.LocalID, OfferingID: id}
				live[key] = true
				if e.evaluatePairLocked(key, sn, cv.Capability, offer) {
					changed = true
				}
			}
		}
	}
	for key := range e.pairs {
		if hostID != "" && key.HostID != hostID {
			continue
		}
		if !live[key] {
			delete(e.pairs, key)
		}
	}
	err := e.persistLocked()
	e.mu.Unlock()
	_ = err
	if changed && e.OnAdvertisedChange != nil {
		e.OnAdvertisedChange()
	}
}

func matches(o config.Offer, c *runnerattach.Capability) bool {
	if o.Capability != c.CapabilityID || o.Protocol != c.Protocol {
		return false
	}
	for k, want := range o.Match {
		idKey := strings.TrimPrefix(k, "identity.")
		if c.Identity[idKey] != want {
			return false
		}
	}
	return true
}

// evaluatePairLocked advances one pair; returns true when the offer's
// advertisement changed (a freeze happened).
func (e *Engine) evaluatePairLocked(key PairKey, sn runners.Snapshot, c *runnerattach.Capability, offer config.Offer) bool {
	now := e.now()
	st := e.states[key.OfferingID]
	proj := runnerattach.Project(c, offer.ExtraFromRunner)
	_, hash, err := proj.Canonical()
	if err != nil {
		return false
	}
	p := e.pairs[key]
	if p == nil {
		p = &Pair{Key: key, State: PairAttached, Since: now}
		e.pairs[key] = p
	}
	// A changed shape is a new runner (runner-attach §5): certification
	// does not carry across the change.
	if p.ShapeHash != "" && p.ShapeHash != hash {
		p.State = PairAttached
		p.Cert = nil
		p.Since = now
	}
	p.ShapeHash = hash
	p.Reason = nil

	if p.State == PairAttached {
		p.State = PairMatched
		p.Since = now
	}
	// Certification. The certifier MUST NOT block: a real engine starts
	// a run and reports back via RecordCertification, returning Pending
	// meanwhile. The default certifier answers empty step lists inline.
	if p.Cert == nil {
		out := e.certifier.Certify(sn, c, offer)
		p.Cert = &out
	}
	if p.Cert.Pending {
		return false // stays matched until the run reports
	}
	if !p.Cert.Passed {
		p.State = PairMatched
		p.Reason = p.Cert.Reason
		return false
	}

	// Certified. Judge against the accepted shape: Pending once an
	// accept-shape happened (its runners must go eligible now), else the
	// frozen one. What is DISPATCHED while superseding is the published
	// (frozen) shape — that is dispatch's lookup, not eligibility's.
	judging := st.judging()
	if judging == nil {
		// First certified runner freezes the offer (§5: freeze once).
		fz := &Frozen{ShapeHash: hash, Projection: proj, FrozenAt: now,
			FrozenBy: FrozenBy{HostID: key.HostID, LocalID: key.LocalID, RunID: p.Cert.RunID}}
		fz.SessionParamsSchema = c.SessionParamsSchema
		if c.Heartbeat != nil {
			fz.HeartbeatIntervalSeconds = c.Heartbeat.IntervalSeconds
		}
		st.Frozen = fz
		p.State = PairEligible
		p.Since = now
		return true
	}
	if judging.ShapeHash == hash {
		if p.State != PairEligible {
			p.State = PairEligible
			p.Since = now
		}
		return false
	}
	if p.State != PairIneligible {
		p.Since = now
	}
	p.State = PairIneligible
	if diff := runnerattach.Diff(judging.Projection, proj); len(diff) > 0 {
		p.Reason = &diff[0]
	} else {
		p.Reason = &runnerattach.Reason{Code: "shape_mismatch", Expected: judging.ShapeHash, Declared: hash}
	}
	return false
}

// judging is the shape eligibility is decided against: the accepted
// pending shape when a supersession is in flight, else the frozen one.
func (st *OfferState) judging() *Frozen {
	if st.Pending != nil {
		return st.Pending
	}
	return st.Frozen
}

// state derives the broker-admin §2 offer state.
func (st *OfferState) state() string {
	switch {
	case st.Disabled:
		return OfferDisabled
	case st.Pending != nil:
		return OfferSuperseding
	case st.Frozen != nil:
		return OfferFrozen
	default:
		return OfferUnfrozen
	}
}

// candidatesLocked groups certified-but-mismatching shapes for an offer
// (broker-admin §4.1 candidates[]). Caller holds e.mu.
func (e *Engine) candidatesLocked(offeringID string) []Candidate {
	st := e.states[offeringID]
	if st == nil {
		return nil
	}
	judging := st.judging()
	byHash := map[string]*Candidate{}
	var order []string
	for key, p := range e.pairs {
		if key.OfferingID != offeringID || p.Cert == nil || !p.Cert.Passed {
			continue
		}
		if judging != nil && p.ShapeHash == judging.ShapeHash {
			continue
		}
		cand := byHash[p.ShapeHash]
		if cand == nil {
			// Recover the projection from the live capability.
			c := e.capabilityFor(key.HostID, key.LocalID)
			if c == nil {
				continue
			}
			proj := runnerattach.Project(c, e.offers[offeringID].ExtraFromRunner)
			cand = &Candidate{ShapeHash: p.ShapeHash, Projection: proj, FirstSeen: p.Since, sample: c}
			if judging != nil {
				cand.Diff = runnerattach.Diff(judging.Projection, proj)
			}
			byHash[p.ShapeHash] = cand
			order = append(order, p.ShapeHash)
		}
		if p.Since.Before(cand.FirstSeen) {
			cand.FirstSeen = p.Since
		}
		cand.Runners = append(cand.Runners, FrozenBy{HostID: key.HostID, LocalID: key.LocalID, RunID: p.Cert.RunID})
	}
	sort.Strings(order)
	out := make([]Candidate, 0, len(order))
	for _, h := range order {
		c := byHash[h]
		sort.Slice(c.Runners, func(i, j int) bool {
			if c.Runners[i].HostID != c.Runners[j].HostID {
				return c.Runners[i].HostID < c.Runners[j].HostID
			}
			return c.Runners[i].LocalID < c.Runners[j].LocalID
		})
		out = append(out, *c)
	}
	return out
}

// capabilityFor finds the live accepted capability for a host+local id.
func (e *Engine) capabilityFor(hostID, localID string) *runnerattach.Capability {
	if e.registry == nil {
		return nil
	}
	sn, ok := e.registry.Get(hostID)
	if !ok {
		return nil
	}
	for _, cv := range sn.Capabilities {
		if cv.Capability != nil && cv.Capability.LocalID == localID {
			return cv.Capability
		}
	}
	return nil
}

// RecordCertification is the callback a real certification engine uses
// when a run reaches a terminal state (plan 0043 item 9).
func (e *Engine) RecordCertification(key PairKey, out CertOutcome) {
	e.mu.Lock()
	if p := e.pairs[key]; p != nil {
		p.Cert = &out
	}
	e.mu.Unlock()
	e.Rematch(key.HostID)
}

func sameJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}

// --- read surfaces ------------------------------------------------------------

// RunnerCounts is the per-state tally broker-admin §4.1 reports.
type RunnerCounts struct {
	Eligible   int `json:"eligible"`
	Ineligible int `json:"ineligible"`
	Matched    int `json:"matched"`
	Attached   int `json:"attached"`
}

// View is one offer as the admin API reports it.
type View struct {
	OfferingID   string       `json:"offering_id"`
	CapabilityID string       `json:"capability_id"`
	Protocol     string       `json:"protocol"`
	State        string       `json:"state"`
	Advertised   bool         `json:"advertised"`
	Source       string       `json:"source"`
	Operator     config.Offer `json:"operator"`
	Frozen       *Frozen      `json:"frozen,omitempty"`
	Pending      *Frozen      `json:"pending,omitempty"`
	Candidates   []Candidate  `json:"-"`
	Runners      RunnerCounts `json:"runners"`
}

// Views lists every offer, sorted by offering id.
func (e *Engine) Views() []View {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ids := make([]string, 0, len(e.offers))
	for id := range e.offers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]View, 0, len(ids))
	for _, id := range ids {
		out = append(out, e.viewLocked(id))
	}
	return out
}

// ViewOf returns one offer.
func (e *Engine) ViewOf(offeringID string) (View, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if _, ok := e.offers[offeringID]; !ok {
		return View{}, false
	}
	return e.viewLocked(offeringID), true
}

func (e *Engine) viewLocked(id string) View {
	o := e.offers[id]
	st := e.states[id]
	v := View{
		OfferingID: id, CapabilityID: o.Capability, Protocol: o.Protocol,
		State: st.state(), Source: e.sourceName(), Operator: o,
		Frozen: st.Frozen, Pending: st.Pending,
		Candidates: e.candidatesLocked(id),
	}
	v.Advertised = e.advertisedLocked(id)
	for key, p := range e.pairs {
		if key.OfferingID != id {
			continue
		}
		switch p.State {
		case PairEligible:
			v.Runners.Eligible++
		case PairIneligible:
			v.Runners.Ineligible++
		case PairCertified:
			v.Runners.Eligible++ // transitional; certified resolves immediately
		case PairMatched:
			v.Runners.Matched++
		default:
			v.Runners.Attached++
		}
	}
	return v
}

func (e *Engine) sourceName() string {
	if e.source == config.OffersSourceAdmin {
		return config.OffersSourceAdmin
	}
	return config.OffersSourceFile
}

func (e *Engine) advertisedLocked(id string) bool {
	st := e.states[id]
	return st != nil && !st.Disabled && st.judging() != nil
}

// Advertised is one offer plus its accepted shape, for the offerings
// payload builder. When a supersession is in flight the ACCEPTED shape
// is what /registry/offerings carries (broker-admin §4.3); dispatch keeps
// the published one until confirm.
type Advertised struct {
	Offer config.Offer
	Shape *Frozen
}

// AdvertisedOffers returns every advertised offer, sorted.
func (e *Engine) AdvertisedOffers() []Advertised {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ids := make([]string, 0, len(e.offers))
	for id := range e.offers {
		if e.advertisedLocked(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]Advertised, 0, len(ids))
	for _, id := range ids {
		out = append(out, Advertised{Offer: e.offers[id], Shape: e.states[id].judging()})
	}
	return out
}

// PairView is one offer's verdict on one runner capability, for the
// runners admin view (broker-admin §3.1 offers[]).
type PairView struct {
	OfferingID    string               `json:"offering_id"`
	State         string               `json:"state"`
	Since         time.Time            `json:"since"`
	Reason        *runnerattach.Reason `json:"reason,omitempty"`
	Certification *CertSummary         `json:"certification,omitempty"`
}

// CertSummary is the §3.1 certification stub on a pair.
type CertSummary struct {
	RunID      string    `json:"run_id,omitempty"`
	State      string    `json:"state"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// PairsFor lists the pairs for one runner capability, sorted by offer.
func (e *Engine) PairsFor(hostID, localID string) []PairView {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []PairView
	for key, p := range e.pairs {
		if key.HostID != hostID || key.LocalID != localID {
			continue
		}
		pv := PairView{OfferingID: key.OfferingID, State: p.State, Since: p.Since, Reason: p.Reason}
		if p.Cert != nil {
			pv.Certification = &CertSummary{RunID: p.Cert.RunID, State: p.Cert.State, FinishedAt: p.Cert.At}
		}
		out = append(out, pv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OfferingID < out[j].OfferingID })
	return out
}

// EligiblePairs lists eligible (host, local) pairs for an offering whose
// shape equals the given hash — dispatch's lookup (plan 0043 item 10).
func (e *Engine) EligiblePairs(offeringID string) []PairKey {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st := e.states[offeringID]
	if st == nil || st.Frozen == nil {
		return nil
	}
	var out []PairKey
	for key, p := range e.pairs {
		if key.OfferingID == offeringID && p.Cert != nil && p.Cert.Passed && p.ShapeHash == st.Frozen.ShapeHash {
			out = append(out, key)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HostID != out[j].HostID {
			return out[i].HostID < out[j].HostID
		}
		return out[i].LocalID < out[j].LocalID
	})
	return out
}
