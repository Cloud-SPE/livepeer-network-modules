// Package certification is the broker's step engine (plan 0043 §3.5;
// protocols/certification-steps.md): it proves a matched runner can
// actually serve an offer before the runner gets work or freezes the
// offer's shape.
//
// The engine implements offers.Certifier. A matched pair triggers an
// async run; while it executes the pair stays `matched` (the engine
// answers Pending); on a terminal outcome the engine reports back to
// the offer engine via the Report callback, which re-evaluates the
// pair — a first pass freezes an unfrozen offer there, never here.
//
// Certification traffic is NOT paid work: no payment envelope, no
// settlement, no receipts, no capacity accounting. It reaches the
// runner over its attach connection with Livepeer-Runner-Local-Id, so
// the agent routes it like any dispatched request (runner-attach §7).
package certification

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
)

// LocalIDHeader routes a certification request to the right container
// on a multi-capability host (runner-attach §7).
const LocalIDHeader = "Livepeer-Runner-Local-Id"

// Step statuses.
const (
	StepPassed  = "passed"
	StepFailed  = "failed"
	StepSkipped = "skipped"
	StepError   = "error"
)

// Run states.
const (
	RunRunning = "running"
	RunPassed  = "passed"
	RunFailed  = "failed"
	RunAborted = "aborted"
	RunError   = "error"
)

// StepResult is one executed step (broker-admin §6.1).
type StepResult struct {
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Required   bool           `json:"required"`
	Status     string         `json:"status"`
	DurationMS int64          `json:"duration_ms"`
	Evidence   map[string]any `json:"evidence,omitempty"`
	Message    string         `json:"message,omitempty"`
}

// Result is one certification run.
type Result struct {
	HostID     string       `json:"host_id"`
	LocalID    string       `json:"local_id"`
	OfferingID string       `json:"offering_id"`
	RunID      string       `json:"run_id"`
	Trigger    string       `json:"trigger"`
	State      string       `json:"state"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt time.Time    `json:"finished_at,omitempty"`
	ShapeHash  string       `json:"shape_hash,omitempty"`
	Steps      []StepResult `json:"steps"`
}

// Options tune the engine.
type Options struct {
	// Extractors builds work_unit extractors for usage steps.
	Extractors *extractors.Registry
	// FixturesDir resolves fixture refs `<dir>/<name>` for multipart
	// steps. Empty means every ref errors at run time.
	FixturesDir string
	// RetryBackoffMax caps the failed-run retry backoff (default 30 m).
	RetryBackoffMax time.Duration
	// Retention bounds kept non-latest results (default 50 per pair).
	KeepPerPair int
	Now         func() time.Time
	// CallbackBaseURL is the broker's externally reachable base URL. A
	// session capability's usage step hands the runner a callback under
	// it (certification-steps §3.3). Empty means no callback can be
	// minted, and a session usage step errors saying so rather than
	// handing the runner a URL it cannot reach.
	CallbackBaseURL string
}

// Engine executes certification runs.
type Engine struct {
	registry *runners.Registry
	opts     Options
	// Report delivers terminal outcomes to the offer engine
	// (offers.Engine.RecordCertification). Set before first use.
	Report func(offers.PairKey, offers.CertOutcome)

	mu      sync.Mutex
	results map[offers.PairKey][]*Result // newest first
	running map[offers.PairKey]context.CancelFunc
	backoff map[offers.PairKey]time.Duration
	seq     int
	ctx     context.Context
	cancel  context.CancelFunc
	// taps collects usage a session runner reports during its own
	// certification run.
	taps *usageTaps
}

// New constructs the engine.
func New(reg *runners.Registry, opts Options) *Engine {
	if opts.RetryBackoffMax <= 0 {
		opts.RetryBackoffMax = 30 * time.Minute
	}
	if opts.KeepPerPair <= 0 {
		opts.KeepPerPair = 50
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		registry: reg, opts: opts, taps: newUsageTaps(),
		results: map[offers.PairKey][]*Result{},
		running: map[offers.PairKey]context.CancelFunc{},
		backoff: map[offers.PairKey]time.Duration{},
		ctx:     ctx, cancel: cancel,
	}
}

// Close aborts every in-flight run.
func (e *Engine) Close() { e.cancel() }

// Certify implements offers.Certifier. It never blocks: empty step
// lists pass inline (certify-on-match); otherwise a run is started (or
// already running) and the pair stays pending.
func (e *Engine) Certify(sn runners.Snapshot, c *runnerattach.Capability, offer config.Offer) offers.CertOutcome {
	if len(offer.Certification) == 0 {
		return offers.CertOutcome{Passed: true, State: RunPassed, At: e.opts.Now()}
	}
	key := offers.PairKey{HostID: sn.HostID, LocalID: c.LocalID, OfferingID: offer.OfferingID}
	e.mu.Lock()
	latest := e.latestLocked(key)
	if latest != nil && latest.State != RunRunning && latest.ShapeHash == shapeHashOf(c, offer) {
		// A terminal result for this exact shape stands; report it.
		out := outcomeOf(latest)
		e.mu.Unlock()
		return out
	}
	_, inFlight := e.running[key]
	e.mu.Unlock()
	if !inFlight {
		e.Start(key, "match", offer, c)
	}
	return offers.CertOutcome{Pending: true, State: RunRunning}
}

// Start launches a run for a pair, aborting any in-flight one. Returns
// the run id.
func (e *Engine) Start(key offers.PairKey, trigger string, offer config.Offer, c *runnerattach.Capability) string {
	now := e.opts.Now()
	e.mu.Lock()
	if cancel, ok := e.running[key]; ok {
		cancel()
		delete(e.running, key)
		if prior := e.latestLocked(key); prior != nil && prior.State == RunRunning {
			prior.State = RunAborted
			prior.FinishedAt = now
		}
	}
	e.seq++
	res := &Result{
		HostID: key.HostID, LocalID: key.LocalID, OfferingID: key.OfferingID,
		RunID: runID(now, e.seq), Trigger: trigger, State: RunRunning,
		StartedAt: now, ShapeHash: shapeHashOf(c, offer), Steps: []StepResult{},
	}
	e.pushLocked(key, res)
	runCtx, cancel := context.WithCancel(e.ctx)
	e.running[key] = cancel
	e.mu.Unlock()

	go e.execute(runCtx, key, offer, c, res)
	return res.RunID
}

// execute runs the steps and reports the terminal outcome.
func (e *Engine) execute(ctx context.Context, key offers.PairKey, offer config.Offer, c *runnerattach.Capability, res *Result) {
	conn, ok := e.registry.ConnFor(key.HostID, key.LocalID)
	var steps []StepResult
	state := RunError
	var failReason *runnerattach.Reason
	if !ok {
		steps = []StepResult{}
		failReason = &runnerattach.Reason{Code: "runner_not_connected", Message: "no attach connection for the pair"}
	} else {
		exec := &runExec{
			engine: e, ctx: ctx, conn: conn, cap: c, offer: offer, runID: res.RunID,
			fixturesDir: e.opts.FixturesDir,
		}
		steps, state, failReason = exec.run()
	}
	now := e.opts.Now()
	e.mu.Lock()
	res.Steps = steps
	res.FinishedAt = now
	if ctx.Err() != nil && state != RunPassed {
		res.State = RunAborted
	} else {
		res.State = state
	}
	if cancel, ok := e.running[key]; ok {
		_ = cancel
		delete(e.running, key)
	}
	// Retry backoff bookkeeping for failed automatic runs.
	if res.State == RunPassed {
		delete(e.backoff, key)
	}
	report := e.Report
	e.mu.Unlock()

	if res.State == RunAborted || report == nil {
		return
	}
	out := outcomeOf(res)
	if failReason != nil {
		out.Reason = failReason
	}
	report(key, out)
}

// Aborted / latest helpers -------------------------------------------------

func (e *Engine) latestLocked(key offers.PairKey) *Result {
	if rs := e.results[key]; len(rs) > 0 {
		return rs[0]
	}
	return nil
}

func (e *Engine) pushLocked(key offers.PairKey, r *Result) {
	rs := append([]*Result{r}, e.results[key]...)
	if len(rs) > e.opts.KeepPerPair {
		rs = rs[:e.opts.KeepPerPair]
	}
	e.results[key] = rs
}

// Drop forgets a pair's results (offer or runner gone).
func (e *Engine) Drop(match func(offers.PairKey) bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key := range e.results {
		if match(key) {
			delete(e.results, key)
			if cancel, ok := e.running[key]; ok {
				cancel()
				delete(e.running, key)
			}
			delete(e.backoff, key)
		}
	}
}

// NextRetryDelay returns and escalates the backoff for a failed pair
// (30 s doubling to RetryBackoffMax; reset on pass or new trigger).
func (e *Engine) NextRetryDelay(key offers.PairKey) time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.backoff[key]
	if d <= 0 {
		d = 30 * time.Second
	} else {
		d *= 2
		if d > e.opts.RetryBackoffMax {
			d = e.opts.RetryBackoffMax
		}
	}
	e.backoff[key] = d
	return d
}

// Results lists results, optionally filtered; latestOnly keeps one per
// pair. Sorted by started_at descending, then pair.
func (e *Engine) Results(hostID, offeringID, state string, latestOnly bool) []Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Result
	for key, rs := range e.results {
		if hostID != "" && key.HostID != hostID || offeringID != "" && key.OfferingID != offeringID {
			continue
		}
		for _, r := range rs {
			if state == "" || r.State == state {
				out = append(out, *r)
			}
			if latestOnly {
				break // rs is newest-first; only rs[0] is "latest"
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		if out[i].HostID != out[j].HostID {
			return out[i].HostID < out[j].HostID
		}
		return out[i].OfferingID < out[j].OfferingID
	})
	return out
}

// PairResults lists every run for one pair, newest first.
func (e *Engine) PairResults(hostID, offeringID string) []Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Result
	for key, rs := range e.results {
		if key.HostID == hostID && key.OfferingID == offeringID {
			for _, r := range rs {
				out = append(out, *r)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// --- small helpers ---------------------------------------------------------

func outcomeOf(r *Result) offers.CertOutcome {
	return offers.CertOutcome{
		Passed: r.State == RunPassed, Pending: r.State == RunRunning,
		RunID: r.RunID, State: r.State, At: r.FinishedAt,
	}
}

func shapeHashOf(c *runnerattach.Capability, offer config.Offer) string {
	_, hash, _ := runnerattach.Project(c, offer.ExtraFromRunner).Canonical()
	return hash
}

func (e *Engine) buildExtractor(cfg map[string]any) (extractors.Extractor, error) {
	if e.opts.Extractors == nil {
		return nil, fmt.Errorf("no extractor registry configured")
	}
	return e.opts.Extractors.Build(cfg)
}

func runID(t time.Time, seq int) string {
	return "run_" + t.Format("20060102T150405") + "-" + itoa(seq)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
