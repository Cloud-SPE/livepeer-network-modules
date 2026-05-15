package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
)

type Status string

const (
	StatusReady       Status = "ready"
	StatusDraining    Status = "draining"
	StatusDegraded    Status = "degraded"
	StatusUnreachable Status = "unreachable"
	StatusStale       Status = "stale"
)

type Snapshot struct {
	ID                   string    `json:"id"`
	OfferingID           string    `json:"offering_id"`
	Status               Status    `json:"status"`
	Reason               string    `json:"reason,omitempty"`
	ProbeType            string    `json:"probe_type,omitempty"`
	ProbedAt             time.Time `json:"probed_at,omitempty"`
	StaleAfter           time.Time `json:"stale_after,omitempty"`
	ConsecutiveSuccesses int       `json:"consecutive_successes,omitempty"`
	ConsecutiveFailures  int       `json:"consecutive_failures,omitempty"`
}

type Response struct {
	BrokerStatus string     `json:"broker_status"`
	GeneratedAt  time.Time  `json:"generated_at"`
	Capabilities []Snapshot `json:"capabilities"`
}

type probeResult struct {
	status Status
	reason string
}

type state struct {
	cap config.Capability

	mu                   sync.RWMutex
	status               Status
	reason               string
	probedAt             time.Time
	staleAfter           time.Time
	consecutiveSuccesses int
	consecutiveFailures  int
}

type Manager struct {
	states []*state
	client *http.Client
}

func New(cfg *config.Config) *Manager {
	states := make([]*state, 0, len(cfg.Capabilities))
	for _, cap := range cfg.Capabilities {
		initial := Status(cap.Health.InitialStatus)
		if initial == "" {
			initial = StatusStale
		}
		if cap.Health.Drain.Enabled || cap.Health.Probe.Type == "manual-drain" {
			initial = StatusDraining
		}
		states = append(states, &state{
			cap:    cap,
			status: initial,
			reason: initialReason(cap, initial),
		})
	}
	return &Manager{
		states: states,
		client: &http.Client{},
	}
}

func initialReason(cap config.Capability, status Status) string {
	switch status {
	case StatusDraining:
		return "operator_marked_drain"
	case StatusStale:
		if cap.Health.Probe.Type == "" {
			return "probe_not_configured"
		}
		return "probe_not_yet_run"
	default:
		return "initial_status"
	}
}

func (m *Manager) Run(ctx context.Context) {
	for _, st := range m.states {
		go m.runState(ctx, st)
	}
}

func (m *Manager) runState(ctx context.Context, st *state) {
	probeType := st.cap.Health.Probe.Type
	if st.cap.Health.Drain.Enabled || probeType == "manual-drain" {
		now := time.Now().UTC()
		st.mu.Lock()
		st.status = StatusDraining
		st.reason = "operator_marked_drain"
		st.probedAt = now
		st.staleAfter = now.Add(24 * time.Hour)
		st.mu.Unlock()
		<-ctx.Done()
		return
	}
	if probeType == "" {
		<-ctx.Done()
		return
	}

	interval := time.Duration(st.cap.Health.Probe.IntervalMS) * time.Millisecond
	if interval <= 0 {
		interval = 5 * time.Second
	}
	m.tick(st)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(st)
		}
	}
}

func (m *Manager) tick(st *state) {
	timeout := time.Duration(st.cap.Health.Probe.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	res, err := m.runProbe(ctx, st.cap)
	now := time.Now().UTC()

	st.mu.Lock()
	defer st.mu.Unlock()
	st.probedAt = now
	st.staleAfter = now.Add(freshnessTTL(st.cap.Health.Probe.IntervalMS))
	if err != nil {
		st.consecutiveFailures++
		st.consecutiveSuccesses = 0
		if st.consecutiveFailures >= max(1, st.cap.Health.Probe.UnhealthyAfter) {
			st.status = StatusUnreachable
		} else if st.status == "" {
			st.status = StatusStale
		}
		st.reason = boundedReason(err.Error(), "probe_failed")
		return
	}

	switch res.status {
	case StatusReady:
		st.consecutiveSuccesses++
		st.consecutiveFailures = 0
		if st.consecutiveSuccesses >= max(1, st.cap.Health.Probe.HealthyAfter) {
			st.status = StatusReady
		}
		if st.status == "" {
			st.status = StatusReady
		}
		st.reason = res.reason
	case StatusDegraded, StatusUnreachable:
		st.consecutiveFailures++
		st.consecutiveSuccesses = 0
		if st.consecutiveFailures >= max(1, st.cap.Health.Probe.UnhealthyAfter) {
			st.status = res.status
		}
		if st.status == "" {
			st.status = res.status
		}
		st.reason = res.reason
	default:
		st.status = StatusStale
		st.reason = "probe_unknown_status"
	}
}

func freshnessTTL(intervalMS int) time.Duration {
	interval := time.Duration(intervalMS) * time.Millisecond
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return interval * 3
}

func (m *Manager) runProbe(ctx context.Context, cap config.Capability) (probeResult, error) {
	switch cap.Health.Probe.Type {
	case "http-status":
		return m.probeHTTPStatus(ctx, cap)
	case "http-jsonpath":
		return m.probeHTTPJSONPath(ctx, cap)
	case "http-openai-model-ready":
		return m.probeHTTPOpenAIModelReady(ctx, cap)
	case "tcp-connect":
		return m.probeTCPConnect(ctx, cap)
	case "command-exit-0":
		return m.probeCommandExit0(ctx, cap)
	default:
		return probeResult{}, fmt.Errorf("unsupported_probe")
	}
}

func (m *Manager) probeHTTPStatus(ctx context.Context, cap config.Capability) (probeResult, error) {
	target, _ := cap.Health.Probe.Config["url"].(string)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return probeResult{}, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return probeResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return probeResult{status: StatusReady, reason: "probe_ok"}, nil
	}
	if resp.StatusCode >= 500 {
		return probeResult{status: StatusUnreachable, reason: "http_5xx"}, nil
	}
	return probeResult{status: StatusDegraded, reason: "http_non_2xx"}, nil
}

func (m *Manager) probeHTTPJSONPath(ctx context.Context, cap config.Capability) (probeResult, error) {
	target, _ := cap.Health.Probe.Config["url"].(string)
	path, _ := cap.Health.Probe.Config["path"].(string)
	expect, _ := cap.Health.Probe.Config["equals"]
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return probeResult{}, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return probeResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return probeResult{status: StatusUnreachable, reason: "http_non_2xx"}, nil
	}
	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return probeResult{}, err
	}
	val, ok := jsonPathLookup(payload, path)
	if !ok {
		return probeResult{status: StatusDegraded, reason: "jsonpath_missing"}, nil
	}
	if expect != nil && fmt.Sprint(val) != fmt.Sprint(expect) {
		return probeResult{status: StatusDegraded, reason: "jsonpath_mismatch"}, nil
	}
	return probeResult{status: StatusReady, reason: "probe_ok"}, nil
}

func (m *Manager) probeHTTPOpenAIModelReady(ctx context.Context, cap config.Capability) (probeResult, error) {
	target, _ := cap.Health.Probe.Config["url"].(string)
	expectModel, _ := cap.Health.Probe.Config["expect_model"].(string)
	if !strings.Contains(target, "/") {
		target = "http://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return probeResult{}, err
	}
	if u.Path == "" || u.Path == "/" || strings.Contains(u.Path, "/v1/chat/completions") {
		u.Path = "/v1/models"
		target = u.String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return probeResult{}, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return probeResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return probeResult{status: StatusUnreachable, reason: "http_non_2xx"}, nil
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return probeResult{}, err
	}
	for _, item := range payload.Data {
		if item.ID == expectModel {
			return probeResult{status: StatusReady, reason: "probe_ok"}, nil
		}
	}
	return probeResult{status: StatusDegraded, reason: "model_not_ready"}, nil
}

func (m *Manager) probeTCPConnect(ctx context.Context, cap config.Capability) (probeResult, error) {
	address, _ := cap.Health.Probe.Config["address"].(string)
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return probeResult{}, err
	}
	_ = conn.Close()
	return probeResult{status: StatusReady, reason: "probe_ok"}, nil
}

func (m *Manager) probeCommandExit0(ctx context.Context, cap config.Capability) (probeResult, error) {
	raw, _ := cap.Health.Probe.Config["command"].([]any)
	if len(raw) == 0 {
		return probeResult{}, errors.New("command_missing")
	}
	args := make([]string, 0, len(raw))
	for _, item := range raw {
		args = append(args, fmt.Sprint(item))
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if err := cmd.Run(); err != nil {
		return probeResult{status: StatusDegraded, reason: "command_non_zero"}, nil
	}
	return probeResult{status: StatusReady, reason: "probe_ok"}, nil
}

func (m *Manager) Snapshot() Response {
	out := Response{
		BrokerStatus: "ready",
		GeneratedAt:  time.Now().UTC(),
		Capabilities: make([]Snapshot, 0, len(m.states)),
	}
	brokerStatus := StatusReady
	for _, st := range m.states {
		snap := st.snapshot()
		out.Capabilities = append(out.Capabilities, snap)
		switch snap.Status {
		case StatusUnreachable:
			brokerStatus = StatusDegraded
		case StatusDegraded:
			if brokerStatus == StatusReady {
				brokerStatus = StatusDegraded
			}
		}
	}
	out.BrokerStatus = string(brokerStatus)
	return out
}

func (st *state) snapshot() Snapshot {
	st.mu.RLock()
	defer st.mu.RUnlock()
	status := st.status
	now := time.Now().UTC()
	if !st.staleAfter.IsZero() && now.After(st.staleAfter) && status != StatusDraining {
		status = StatusStale
	}
	return Snapshot{
		ID:                   st.cap.ID,
		OfferingID:           st.cap.OfferingID,
		Status:               status,
		Reason:               st.reason,
		ProbeType:            st.cap.Health.Probe.Type,
		ProbedAt:             st.probedAt,
		StaleAfter:           st.staleAfter,
		ConsecutiveSuccesses: st.consecutiveSuccesses,
		ConsecutiveFailures:  st.consecutiveFailures,
	}
}

func boundedReason(reason, fallback string) string {
	if reason == "" {
		return fallback
	}
	reason = strings.ToLower(reason)
	reason = strings.ReplaceAll(reason, " ", "_")
	if len(reason) > 64 {
		return fallback
	}
	return reason
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func jsonPathLookup(v any, path string) (any, bool) {
	if path == "" || path == "$" {
		return v, true
	}
	if !strings.HasPrefix(path, "$.") {
		return nil, false
	}
	cur := v
	for _, part := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
