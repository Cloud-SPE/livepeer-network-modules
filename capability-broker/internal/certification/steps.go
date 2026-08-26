package certification

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/responsejsonpath"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
)

// runExec executes one run's steps in order (certification-steps §5).
type runExec struct {
	engine      *Engine
	ctx         context.Context
	conn        runners.Conn
	cap         *runnerattach.Capability
	offer       config.Offer
	runID       string
	fixturesDir string

	// lastRequest is the nearest preceding request step's outcome, for
	// usage/latency source: previous_request.
	lastRequest *exchange
}

// exchange is a completed request-step exchange.
type exchange struct {
	cfg       map[string]any // the resolved (substituted) request config
	status    int
	body      []byte
	header    http.Header
	duration  time.Duration
	sessionID string // session form: the runner session still open (hold_ms)
	units     func() (uint64, string, error)
}

const (
	defaultRequestTimeout = 30 * time.Second
	defaultReadinessTO    = 5 * time.Second
	defaultUsageTimeout   = 15 * time.Second
	maxEvidenceBytes      = 8 * 1024
	defaultMaxRespBytes   = 1 << 20
)

func (x *runExec) run() ([]StepResult, string, *runnerattach.Reason) {
	var out []StepResult
	skipping := false
	failed := false
	var firstFail *runnerattach.Reason
	for _, step := range x.offer.Certification {
		sr := StepResult{Name: step.Name, Type: step.Type, Required: step.IsRequired()}
		if skipping {
			sr.Status = StepSkipped
			out = append(out, sr)
			continue
		}
		start := time.Now()
		x.executeStep(step, &sr)
		sr.DurationMS = time.Since(start).Milliseconds()
		boundEvidence(&sr)
		out = append(out, sr)
		if sr.Status != StepPassed && sr.Required {
			skipping = true
			failed = true
			if firstFail == nil {
				firstFail = &runnerattach.Reason{Code: "certification_failed",
					Field: "/" + step.Name, Message: sr.Message}
			}
		}
	}
	state := RunPassed
	if failed {
		state = RunFailed
	}
	if x.ctx.Err() != nil {
		state = RunError
	}
	return out, state, firstFail
}

func (x *runExec) executeStep(step config.CertificationStep, sr *StepResult) {
	timeout := time.Duration(step.TimeoutMS) * time.Millisecond
	switch step.Type {
	case "readiness":
		x.stepReadiness(step, sr, timeout)
	case "request":
		x.stepRequest(step, sr, timeout)
	case "usage":
		x.stepUsage(step, sr, timeout)
	case "latency":
		x.stepLatency(step, sr, timeout)
	default:
		sr.Status = StepError
		sr.Message = "unknown step type " + step.Type
	}
}

// --- readiness (certification-steps §3.1) ----------------------------------

func (x *runExec) stepReadiness(step config.CertificationStep, sr *StepResult, timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultReadinessTO
	}
	attempts := intOr(step.Config, "attempts", 3)
	interval := time.Duration(intOr(step.Config, "interval_ms", 2000)) * time.Millisecond
	consecutive := intOr(step.Config, "consecutive", 1)
	path := x.cap.Readiness.Path
	if path == "" {
		path = "/"
	}
	streak := 0
	for i := 1; i <= attempts; i++ {
		ok := x.probeOnce(path, timeout)
		if ok {
			streak++
			if streak >= consecutive {
				sr.Status = StepPassed
				sr.Evidence = map[string]any{"attempts": i, "ready_at_attempt": i, "probe_type": x.cap.Readiness.Type}
				return
			}
		} else {
			streak = 0
		}
		if i < attempts {
			select {
			case <-x.ctx.Done():
				sr.Status = StepError
				sr.Message = "aborted"
				return
			case <-time.After(interval):
			}
		}
	}
	sr.Status = StepFailed
	sr.Evidence = map[string]any{"attempts": attempts, "probe_type": x.cap.Readiness.Type}
	sr.Message = "runner not ready"
}

// probeOnce evaluates the runner-declared probe over the tunnel. The
// broker-local probe families never run here (runner-attach §3.2), so
// every remote kind reduces to an HTTP exchange the agent routes.
func (x *runExec) probeOnce(path string, timeout time.Duration) bool {
	resp, err := x.forward(http.MethodGet, path, nil, nil, timeout, defaultMaxRespBytes)
	if err != nil {
		return false
	}
	switch x.cap.Readiness.Type {
	case "http-status", "tcp-connect":
		return resp.status >= 200 && resp.status < 400
	case "http-jsonpath":
		pathExpr, _ := x.cap.Readiness.Config["path"].(string)
		want := x.cap.Readiness.Config["equals"]
		var data any
		if json.Unmarshal(resp.body, &data) != nil {
			return false
		}
		vals, err := responsejsonpath.Eval(pathExpr, data)
		if err != nil || len(vals) == 0 {
			return false
		}
		if want == nil {
			return true
		}
		return fmt.Sprint(vals[0]) == fmt.Sprint(want)
	case "http-openai-model-ready":
		model, _ := x.cap.Readiness.Config["model"].(string)
		if model == "" {
			model = x.cap.Identity["openai.model"]
		}
		return resp.status == 200 && model != "" && bytes.Contains(resp.body, []byte(model))
	default:
		return false
	}
}

// --- request (certification-steps §3.2) ------------------------------------

func (x *runExec) stepRequest(step config.CertificationStep, sr *StepResult, timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	cfg, err := x.substituteConfig(step.Config)
	if err != nil {
		sr.Status = StepError
		sr.Message = err.Error()
		return
	}
	if x.cap.IsSession() {
		x.sessionRequest(cfg, sr, timeout)
		return
	}
	ex, failMsg, err := x.jobExchange(cfg, timeout)
	if err != nil {
		sr.Status = StepError
		sr.Message = err.Error()
		return
	}
	if failMsg != "" {
		sr.Status = StepFailed
		sr.Message = failMsg
		sr.Evidence = map[string]any{"status": ex.status, "bytes": len(ex.body), "duration_ms": ex.duration.Milliseconds()}
		return
	}
	x.lastRequest = ex
	sr.Status = StepPassed
	sr.Evidence = map[string]any{
		"transport": transportOf(cfg, x.cap), "status": ex.status,
		"content_type": ex.header.Get("Content-Type"), "bytes": len(ex.body),
		"duration_ms": ex.duration.Milliseconds(), "asserted": assertedPaths(cfg),
	}
}

// jobExchange performs one exchange and applies expectations. failMsg
// non-empty means the runner failed the step; err means the broker
// could not run it.
func (x *runExec) jobExchange(cfg map[string]any, timeout time.Duration) (*exchange, string, error) {
	transport := transportOf(cfg, x.cap)
	if !declaredTransport(x.cap, transport) {
		return nil, "", fmt.Errorf("transport_not_declared: %s", transport)
	}
	path, _ := cfg["path"].(string)
	if path == "" {
		path = x.cap.Paths["invoke"]
	}
	method, _ := cfg["method"].(string)
	if method == "" {
		method = http.MethodPost
	}
	headers := http.Header{}
	if hs, ok := cfg["headers"].(map[string]any); ok {
		for k, v := range hs {
			headers.Set(k, fmt.Sprint(v))
		}
	}
	var body io.Reader
	switch transport {
	case "multipart":
		buf, contentType, err := x.multipartBody(cfg)
		if err != nil {
			return nil, "", err
		}
		headers.Set("Content-Type", contentType)
		body = buf
	default:
		if raw, ok := cfg["body"]; ok {
			b, err := json.Marshal(raw)
			if err != nil {
				return nil, "", err
			}
			headers.Set("Content-Type", "application/json")
			body = bytes.NewReader(b)
		}
	}
	maxBytes := int64(intOr(cfg, "max_response_bytes", defaultMaxRespBytes))
	ex, err := x.forward(method, path, headers, body, timeout, maxBytes)
	if err != nil {
		return nil, "", err
	}
	ex.cfg = cfg
	if msg := checkExpectations(cfg, ex); msg != "" {
		return ex, msg, nil
	}
	return ex, "", nil
}

func checkExpectations(cfg map[string]any, ex *exchange) string {
	if !statusExpected(cfg, ex.status) {
		return fmt.Sprintf("status %d not in expect_status", ex.status)
	}
	if want, ok := cfg["expect_content_type"].(string); ok && want != "" {
		if !strings.HasPrefix(ex.header.Get("Content-Type"), want) {
			return fmt.Sprintf("content type %q does not match %q", ex.header.Get("Content-Type"), want)
		}
	}
	if asserts, ok := cfg["assert"].([]any); ok && len(asserts) > 0 {
		var data any
		if err := json.Unmarshal(assertableBody(ex), &data); err != nil {
			return "response is not JSON for assert[]"
		}
		for _, a := range asserts {
			if msg := checkAssert(a, data); msg != "" {
				return msg
			}
		}
	}
	return ""
}

// assertableBody: for SSE streams, concatenate data: payloads into a
// JSON array (certification-steps §3.2); otherwise the raw body.
func assertableBody(ex *exchange) []byte {
	ct := ex.header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		return ex.body
	}
	var items []json.RawMessage
	for _, line := range strings.Split(string(ex.body), "\n") {
		line = strings.TrimSpace(line)
		if payload, ok := strings.CutPrefix(line, "data:"); ok {
			payload = strings.TrimSpace(payload)
			if payload == "" || payload == "[DONE]" {
				continue
			}
			if json.Valid([]byte(payload)) {
				items = append(items, json.RawMessage(payload))
			}
		}
	}
	out, _ := json.Marshal(items)
	return out
}

func checkAssert(a any, data any) string {
	switch spec := a.(type) {
	case string:
		vals, err := responsejsonpath.Eval(spec, data)
		if err != nil || len(vals) == 0 || vals[0] == nil {
			return "assert " + spec + ": no non-null match"
		}
		return ""
	case map[string]any:
		pathExpr, _ := spec["path"].(string)
		vals, err := responsejsonpath.Eval(pathExpr, data)
		if err != nil || len(vals) == 0 {
			return "assert " + pathExpr + ": no match"
		}
		got := vals[0]
		if want, ok := spec["equals"]; ok && fmt.Sprint(got) != fmt.Sprint(want) {
			return fmt.Sprintf("assert %s: got %v want %v", pathExpr, got, want)
		}
		if min, ok := spec["min"]; ok {
			g, gok := toFloat(got)
			m, _ := toFloat(min)
			if !gok || g < m {
				return fmt.Sprintf("assert %s: %v below min %v", pathExpr, got, min)
			}
		}
		return ""
	default:
		return "assert entry has unknown shape"
	}
}

// sessionRequest: open → descriptor check → (hold) → terminate → status
// (certification-steps §3.2 session form).
func (x *runExec) sessionRequest(cfg map[string]any, sr *StepResult, timeout time.Duration) {
	params, _ := json.Marshal(valueOr(cfg, "session_params", map[string]any{}))
	createBody, _ := json.Marshal(map[string]any{
		"session_id": "cert-" + x.runID, "work_id": "cert-" + x.runID,
		"capability": x.cap.CapabilityID, "offering": x.offer.OfferingID,
		"session_params": json.RawMessage(params),
	})
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	start := time.Now()
	ex, err := x.forward(http.MethodPost, x.cap.Paths["create"], headers, bytes.NewReader(createBody), timeout, defaultMaxRespBytes)
	if err != nil {
		sr.Status = StepError
		sr.Message = err.Error()
		return
	}
	if ex.status < 200 || ex.status >= 300 {
		sr.Status = StepFailed
		sr.Message = fmt.Sprintf("create returned %d", ex.status)
		sr.Evidence = map[string]any{"status": ex.status}
		return
	}
	var created struct {
		RunnerSessionID string          `json:"runner_session_id"`
		Runtime         json.RawMessage `json:"runtime"`
	}
	if err := json.Unmarshal(ex.body, &created); err != nil || len(created.Runtime) == 0 {
		sr.Status = StepFailed
		sr.Message = "create response carries no runtime descriptor"
		return
	}
	expectSchema, _ := cfg["expect_descriptor_schema"].(string)
	if expectSchema == "" && len(x.cap.DescriptorSchemas) > 0 {
		schemas := append([]string(nil), x.cap.DescriptorSchemas...)
		sort.Strings(schemas)
		expectSchema = schemas[0]
	}
	desc, err := sessionengine.ParseDescriptor(created.Runtime, expectSchema, 64*1024)
	if err != nil {
		sr.Status = StepFailed
		sr.Message = "descriptor: " + err.Error()
		return
	}
	if asserts, ok := cfg["assert"].([]any); ok && len(asserts) > 0 {
		var pub any
		_ = json.Unmarshal(desc.Public, &pub)
		for _, a := range asserts {
			if msg := checkAssert(a, pub); msg != "" {
				sr.Status = StepFailed
				sr.Message = msg
				return
			}
		}
	}
	x.lastRequest = &exchange{cfg: cfg, status: ex.status, body: ex.body, header: ex.header,
		duration: time.Since(start), sessionID: created.RunnerSessionID}
	if hold := intOr(cfg, "hold_ms", 0); hold > 0 {
		select {
		case <-x.ctx.Done():
		case <-time.After(time.Duration(hold) * time.Millisecond):
		}
	}
	termPath := strings.ReplaceAll(x.cap.Paths["terminate"], "{id}", created.RunnerSessionID)
	tex, err := x.forward(http.MethodDelete, termPath, nil, nil, timeout, defaultMaxRespBytes)
	if err != nil || tex.status >= 300 {
		sr.Status = StepFailed
		sr.Message = "terminate failed"
		return
	}
	sr.Status = StepPassed
	sr.Evidence = map[string]any{"descriptor_schema": desc.Schema, "duration_ms": time.Since(start).Milliseconds(),
		"asserted": assertedPaths(cfg), "terminated": true}
}

// --- usage (certification-steps §3.3) --------------------------------------

func (x *runExec) stepUsage(step config.CertificationStep, sr *StepResult, timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultUsageTimeout
	}
	minUnits := uint64(intOr(step.Config, "min_units", 1))
	src := x.lastRequest
	if inline, ok := step.Config["source"].(map[string]any); ok {
		cfg, err := x.substituteConfig(inline)
		if err != nil {
			sr.Status = StepError
			sr.Message = err.Error()
			return
		}
		ex, failMsg, err := x.jobExchange(cfg, timeout)
		if err != nil || failMsg != "" {
			sr.Status = StepError
			sr.Message = firstNonEmpty(failMsg, errString(err))
			return
		}
		src = ex
	}
	if src == nil {
		sr.Status = StepError
		sr.Message = "no preceding request step"
		return
	}
	if x.cap.IsSession() {
		// Session usage needs the runner's usage events, which arrive on
		// the paid callback surface the certification path deliberately
		// does not mint. Runner-reported usage is proven by the paid
		// path's own machinery; here it is recorded as not-run.
		sr.Status = StepError
		sr.Message = "session usage verification requires the item-9 event tap; not implemented"
		return
	}
	units, extractorType, err := x.runExtractor(src)
	if err != nil {
		sr.Status = StepError
		sr.Message = err.Error()
		return
	}
	sr.Evidence = map[string]any{"extractor": extractorType, "units": units}
	if units >= minUnits {
		sr.Status = StepPassed
	} else {
		sr.Status = StepFailed
		sr.Message = fmt.Sprintf("units %d below min_units %d", units, minUnits)
	}
}

func (x *runExec) runExtractor(src *exchange) (uint64, string, error) {
	typ, _ := x.cap.WorkUnit.Extractor["type"].(string)
	ext, err := x.engine.buildExtractor(x.cap.WorkUnit.Extractor)
	if err != nil {
		return 0, typ, err
	}
	var reqBody []byte
	if raw, ok := src.cfg["body"]; ok {
		reqBody, _ = json.Marshal(raw)
	}
	units, err := ext.Extract(x.ctx,
		&extractors.Request{Method: http.MethodPost, Body: reqBody},
		&extractors.Response{Status: src.status, Body: src.body, Headers: src.header})
	return units, typ, err
}

// --- latency (certification-steps §3.4) ------------------------------------

func (x *runExec) stepLatency(step config.CertificationStep, sr *StepResult, timeout time.Duration) {
	samples := intOr(step.Config, "samples", 3)
	warmup := intOr(step.Config, "warmup", 0)
	measure, _ := step.Config["measure"].(string)
	cfg := map[string]any(nil)
	if inline, ok := step.Config["request"].(map[string]any); ok {
		var err error
		cfg, err = x.substituteConfig(inline)
		if err != nil {
			sr.Status = StepError
			sr.Message = err.Error()
			return
		}
	} else if x.lastRequest != nil {
		cfg = x.lastRequest.cfg
	} else {
		sr.Status = StepError
		sr.Message = "no preceding request step"
		return
	}
	if timeout <= 0 {
		timeout = time.Duration(samples) * defaultRequestTimeout
	}
	perSample := timeout / time.Duration(samples+warmup)
	var durations []time.Duration
	for i := 0; i < warmup+samples; i++ {
		ex, failMsg, err := x.jobExchange(cfg, perSample)
		if err != nil || failMsg != "" {
			sr.Status = StepFailed
			sr.Message = "sample failed: " + firstNonEmpty(failMsg, errString(err))
			sr.Evidence = map[string]any{"samples": len(durations)}
			return
		}
		_ = measure // first_byte needs streaming plumbing; total measured for both. Recorded in evidence.
		if i >= warmup {
			durations = append(durations, ex.duration)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p50 := durations[len(durations)/2]
	p95 := durations[(len(durations)*95)/100]
	sr.Evidence = map[string]any{
		"samples": samples, "p50_ms": p50.Milliseconds(), "p95_ms": p95.Milliseconds(),
		"min_ms": durations[0].Milliseconds(), "max_ms": durations[len(durations)-1].Milliseconds(),
		"measure": firstNonEmpty(measure, "total"),
	}
	bound := map[string]any{}
	ok := true
	if v, has := step.Config["p50_max_ms"]; has {
		bound["p50_max_ms"] = v
		if m, _ := toFloat(v); float64(p50.Milliseconds()) > m {
			ok = false
		}
	}
	if v, has := step.Config["p95_max_ms"]; has {
		bound["p95_max_ms"] = v
		if m, _ := toFloat(v); float64(p95.Milliseconds()) > m {
			ok = false
		}
	}
	sr.Evidence["bound"] = bound
	if ok {
		sr.Status = StepPassed
	} else {
		sr.Status = StepFailed
		sr.Message = "percentile above bound"
	}
}

// --- transport & helpers ----------------------------------------------------

// forward sends one request over the attach connection with the routing
// header (runner-attach §7). No payment headers, ever.
func (x *runExec) forward(method, path string, headers http.Header, body io.Reader, timeout time.Duration, maxBytes int64) (*exchange, error) {
	if headers == nil {
		headers = http.Header{}
	}
	headers.Set(LocalIDHeader, x.cap.LocalID)
	headers.Set("Livepeer-Capability", x.cap.CapabilityID)
	headers.Set("Livepeer-Offering", x.offer.OfferingID)
	ctx, cancel := context.WithTimeout(x.ctx, timeout)
	defer cancel()
	start := time.Now()
	resp, err := x.conn.Forward(ctx, backend.ForwardRequest{
		URL: "http://worker.local" + path, Method: method, Headers: headers, Body: body,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("response_too_large: > %d bytes", maxBytes)
	}
	return &exchange{status: resp.StatusCode, body: raw, header: resp.Header, duration: time.Since(start)}, nil
}

func (x *runExec) multipartBody(cfg map[string]any) (*bytes.Buffer, string, error) {
	parts, _ := cfg["parts"].([]any)
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	for i, raw := range parts {
		part, _ := raw.(map[string]any)
		name, _ := part["name"].(string)
		if v, ok := part["value"].(string); ok {
			if err := w.WriteField(name, v); err != nil {
				return nil, "", err
			}
			continue
		}
		fx, _ := part["fixture"].(map[string]any)
		data, contentType, err := x.resolveFixture(fx)
		if err != nil {
			return nil, "", fmt.Errorf("parts[%d]: %w", i, err)
		}
		filename, _ := part["filename"].(string)
		if filename == "" {
			filename = "fixture.bin"
		}
		if ct, ok := part["content_type"].(string); ok && ct != "" {
			contentType = ct
		}
		hdr := make(map[string][]string)
		hdr["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name=%q; filename=%q`, name, filename)}
		hdr["Content-Type"] = []string{contentType}
		fw, err := w.CreatePart(hdr)
		if err != nil {
			return nil, "", err
		}
		if _, err := fw.Write(data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf, w.FormDataContentType(), nil
}

func (x *runExec) resolveFixture(fx map[string]any) ([]byte, string, error) {
	if inline, ok := fx["inline_base64"].(string); ok {
		data, err := base64.StdEncoding.DecodeString(inline)
		if err != nil {
			return nil, "", fmt.Errorf("fixture_invalid: %v", err)
		}
		ct, _ := fx["content_type"].(string)
		return data, ct, nil
	}
	ref, _ := fx["ref"].(string)
	if x.fixturesDir == "" {
		return nil, "", fmt.Errorf("fixture_missing: no certification fixtures_dir configured for ref %q", ref)
	}
	// ref is <dir>/<name-without-extension>; find the file by glob.
	matches, _ := filepath.Glob(filepath.Join(x.fixturesDir, filepath.Clean(ref)+".*"))
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(x.fixturesDir, filepath.Clean(ref)))
	}
	if len(matches) == 0 {
		return nil, "", fmt.Errorf("fixture_missing: %q not under %s", ref, x.fixturesDir)
	}
	sort.Strings(matches)
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return nil, "", err
	}
	return data, "application/octet-stream", nil
}

// substituteConfig applies {{identity.*}} / {{offer.*}} / {{run.id}}
// (certification-steps §4). A token with no value is a template bug:
// step error, nothing sent.
func (x *runExec) substituteConfig(cfg map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	out := string(raw)
	for start := strings.Index(out, "{{"); start >= 0; start = strings.Index(out, "{{") {
		end := strings.Index(out[start:], "}}")
		if end < 0 {
			break
		}
		token := out[start+2 : start+end]
		value, ok := x.tokenValue(strings.TrimSpace(token))
		if !ok {
			return nil, fmt.Errorf("substitution_missing: {{%s}}", strings.TrimSpace(token))
		}
		escaped, _ := json.Marshal(value)
		out = out[:start] + strings.Trim(string(escaped), `"`) + out[start+end+2:]
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return nil, fmt.Errorf("substitution produced invalid JSON: %v", err)
	}
	return result, nil
}

func (x *runExec) tokenValue(token string) (string, bool) {
	switch {
	case token == "run.id":
		return x.runID, true
	case token == "offer.offering_id":
		return x.offer.OfferingID, true
	case token == "offer.capability_id":
		return x.offer.Capability, true
	case strings.HasPrefix(token, "identity."):
		v, ok := x.cap.Identity[strings.TrimPrefix(token, "identity.")]
		return v, ok
	case strings.HasPrefix(token, "offer.extra."):
		v, ok := lookupDotted(x.offer.Extra, strings.TrimPrefix(token, "offer.extra."))
		return v, ok
	default:
		return "", false
	}
}

func lookupDotted(m map[string]any, key string) (string, bool) {
	cur := any(m)
	for _, part := range strings.Split(key, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = obj[part]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	if !ok && cur != nil {
		return fmt.Sprint(cur), true
	}
	return s, ok
}

func transportOf(cfg map[string]any, c *runnerattach.Capability) string {
	if t, ok := cfg["transport"].(string); ok && t != "" {
		return t
	}
	if len(c.Transports) > 0 {
		ts := append([]string(nil), c.Transports...)
		sort.Strings(ts)
		return ts[0]
	}
	return "unary"
}

func declaredTransport(c *runnerattach.Capability, t string) bool {
	for _, d := range c.Transports {
		if d == t {
			return true
		}
	}
	return false
}

func statusExpected(cfg map[string]any, status int) bool {
	switch want := cfg["expect_status"].(type) {
	case nil:
		return status == 200
	case float64:
		return status == int(want)
	case int:
		return status == want
	case []any:
		for _, w := range want {
			if f, ok := toFloat(w); ok && status == int(f) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func assertedPaths(cfg map[string]any) []string {
	var out []string
	if asserts, ok := cfg["assert"].([]any); ok {
		for _, a := range asserts {
			switch spec := a.(type) {
			case string:
				out = append(out, spec)
			case map[string]any:
				if p, ok := spec["path"].(string); ok {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

func boundEvidence(sr *StepResult) {
	if sr.Evidence == nil {
		return
	}
	raw, err := json.Marshal(sr.Evidence)
	if err != nil || len(raw) <= maxEvidenceBytes {
		return
	}
	sr.Evidence = map[string]any{"truncated": true}
}

func intOr(cfg map[string]any, key string, def int) int {
	if v, ok := cfg[key]; ok {
		if f, ok := toFloat(v); ok {
			return int(f)
		}
	}
	return def
}

func valueOr(cfg map[string]any, key string, def any) any {
	if v, ok := cfg[key]; ok {
		return v
	}
	return def
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
