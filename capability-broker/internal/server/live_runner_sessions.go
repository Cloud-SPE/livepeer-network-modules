package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/modes/livesessiongatewayingest"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/modes/sessioncontrolexternalmedia"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
)

const remoteLiveRunnerTransport = "remote-live-runner"

func isLiveRunnerMode(mode string) bool {
	return mode == sessioncontrolexternalmedia.Mode || mode == livesessiongatewayingest.Mode
}

type liveRunnerSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*liveRunnerSession
}

type liveRunnerSession struct {
	mu sync.Mutex

	BrokerSessionID  string
	GatewaySessionID string
	RunnerSessionID  string
	WorkID           string
	CapabilityID     string
	OfferingID       string
	Sender           []byte
	CallbackToken    string
	RequestID        string
	Mode             string

	State              string
	CloseReason        *string
	StartedAt          *time.Time
	LastHeartbeatAt    *time.Time
	EndedAt            *time.Time
	ExpiresAt          time.Time
	CreatedAt          time.Time
	PaymentClosed      bool
	LastSequence       uint64
	DebitSeq           uint64
	ReportedUsageUnit  string
	ReportedUsageTotal uint64
	DebitedUsageTotal  uint64
	EventIDs           map[string]struct{}

	InitialStreamKey string
	IngestRTMPURL    string
	PlaybackHLSURL   string
	PrivateIngestURL string

	Backend         config.Backend
	CapacityRelease func()
}

func newLiveRunnerSessionStore() *liveRunnerSessionStore {
	return &liveRunnerSessionStore{sessions: make(map[string]*liveRunnerSession)}
}

func (s *liveRunnerSessionStore) Add(sess *liveRunnerSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sess.BrokerSessionID]; ok {
		return errors.New("broker session already exists")
	}
	s.sessions[sess.BrokerSessionID] = sess
	return nil
}

func (s *liveRunnerSessionStore) Get(id string) *liveRunnerSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

type liveRunnerBackendClient struct {
	http *http.Client
	auth *backend.AuthApplier
}

type liveRunnerCreateRequest struct {
	BrokerSessionID  string                 `json:"broker_session_id"`
	WorkID           string                 `json:"work_id"`
	CapabilityID     string                 `json:"capability_id"`
	OfferingID       string                 `json:"offering_id"`
	SessionParams    map[string]any         `json:"session_params"`
	OutputCredential *liveOutputCredential  `json:"output_credential,omitempty"`
	IngestAccept     *liveIngestAccept      `json:"ingest_accept,omitempty"`
	BrokerCallbacks  liveRunnerCallbacksReq `json:"broker_callbacks"`
}

type liveRunnerCallbacksReq struct {
	EventURL  string `json:"event_url"`
	AuthToken string `json:"auth_token"`
}

type liveRunnerCreateResponse struct {
	RunnerSessionID  string          `json:"runner_session_id"`
	State            string          `json:"state"`
	Media            liveRunnerMedia `json:"media"`
	PrivateIngestURL string          `json:"private_ingest_url,omitempty"`
	CreatedAt        string          `json:"created_at"`
	ExpiresAt        string          `json:"expires_at,omitempty"`
}

type liveRunnerMedia struct {
	Ingest struct {
		RTMPURL   string `json:"rtmp_url"`
		StreamKey string `json:"stream_key,omitempty"`
	} `json:"ingest"`
	Playback struct {
		HLSURL string `json:"hls_url"`
	} `json:"playback"`
}

type liveRunnerDeleteRequest struct {
	Reason string `json:"reason"`
}

type liveRunnerDeleteResponse struct {
	RunnerSessionID string `json:"runner_session_id"`
	State           string `json:"state"`
	CloseReason     string `json:"close_reason"`
	EndedAt         string `json:"ended_at"`
}

func newLiveRunnerBackendClient() *liveRunnerBackendClient {
	return &liveRunnerBackendClient{
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *liveRunnerBackendClient) CreateSession(ctx context.Context, cap *config.Capability, req liveRunnerCreateRequest, secrets backend.SecretResolver) (*liveRunnerCreateResponse, error) {
	if cap == nil || cap.Backend.LiveRunner == nil {
		return nil, errors.New("live runner backend not configured")
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(req); err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cap.Backend.LiveRunner.BaseURL, "/")+"/v1/video/live/sessions", &buf)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if err := backend.NewAuthApplier(secrets).Apply(httpReq.Header, cap.Backend.Auth); err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("runner create session status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out liveRunnerCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *liveRunnerBackendClient) DeleteSession(ctx context.Context, sess *liveRunnerSession, reason string, secrets backend.SecretResolver) (*liveRunnerDeleteResponse, error) {
	if sess == nil || sess.Backend.LiveRunner == nil {
		return nil, errors.New("live runner backend not configured")
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(liveRunnerDeleteRequest{Reason: reason}); err != nil {
		return nil, err
	}
	target := strings.TrimRight(sess.Backend.LiveRunner.BaseURL, "/") + "/v1/video/live/sessions/" + url.PathEscape(sess.RunnerSessionID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, &buf)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if err := backend.NewAuthApplier(secrets).Apply(httpReq.Header, sess.Backend.Auth); err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("runner delete session status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out liveRunnerDeleteResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

type liveSessionOpenRequest struct {
	GatewaySessionID string                `json:"gateway_session_id"`
	SessionParams    map[string]any        `json:"session_params"`
	OutputCredential *liveOutputCredential `json:"output_credential,omitempty"`
	IngestAccept     *liveIngestAccept     `json:"ingest_accept,omitempty"`
}

type liveOutputCredential struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	KeyPrefix       string `json:"key_prefix"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token"`
	ExpiresAt       string `json:"expires_at"`
}

type liveIngestAccept struct {
	StreamKey string `json:"stream_key"`
}

type liveSessionOpenResponse struct {
	GatewaySessionID string `json:"gateway_session_id,omitempty"`
	BrokerSessionID  string `json:"broker_session_id"`
	RunnerSessionID  string `json:"runner_session_id"`
	WorkID           string `json:"work_id"`
	State            string `json:"state"`
	Media            *struct {
		Ingest struct {
			RTMPURL   string `json:"rtmp_url"`
			StreamKey string `json:"stream_key,omitempty"`
		} `json:"ingest"`
		Playback struct {
			HLSURL string `json:"hls_url"`
		} `json:"playback"`
	} `json:"media,omitempty"`
	PrivateIngestURL string `json:"private_ingest_url,omitempty"`
	Control          struct {
		TopupURL  string `json:"topup_url"`
		StatusURL string `json:"status_url"`
		EndURL    string `json:"end_url"`
	} `json:"control"`
	ExpiresAt string `json:"expires_at"`
}

type liveSessionTopupRequest struct {
	GatewaySessionID string `json:"gateway_session_id"`
}

type liveSessionTopupResponse struct {
	BrokerSessionID string `json:"broker_session_id"`
	WorkID          string `json:"work_id"`
	State           string `json:"state"`
	Balance         struct {
		Status                string `json:"status"`
		RunwaySecondsEstimate int64  `json:"runway_seconds_estimate"`
	} `json:"balance"`
}

type liveSessionStatusResponse struct {
	GatewaySessionID string `json:"gateway_session_id"`
	BrokerSessionID  string `json:"broker_session_id"`
	RunnerSessionID  string `json:"runner_session_id"`
	WorkID           string `json:"work_id"`
	State            string `json:"state"`
	Media            *struct {
		Ingest struct {
			RTMPURL string `json:"rtmp_url"`
		} `json:"ingest"`
		Playback struct {
			HLSURL string `json:"hls_url"`
		} `json:"playback"`
	} `json:"media,omitempty"`
	StartedAt       *string `json:"started_at"`
	LastHeartbeatAt *string `json:"last_heartbeat_at"`
	EndedAt         *string `json:"ended_at"`
	CloseReason     *string `json:"close_reason"`
}

type liveSessionEndRequest struct {
	Reason string `json:"reason"`
}

type liveSessionEndResponse struct {
	BrokerSessionID string  `json:"broker_session_id"`
	RunnerSessionID string  `json:"runner_session_id"`
	State           string  `json:"state"`
	CloseReason     *string `json:"close_reason"`
	EndedAt         *string `json:"ended_at"`
}

type liveRunnerEventRequest struct {
	BrokerSessionID string `json:"broker_session_id"`
	RunnerSessionID string `json:"runner_session_id"`
	EventID         string `json:"event_id"`
	Sequence        uint64 `json:"sequence"`
	EventType       string `json:"event_type"`
	EventTime       string `json:"event_time"`
	State           string `json:"state"`
	Usage           struct {
		Unit  string `json:"unit"`
		Delta uint64 `json:"delta"`
		Total uint64 `json:"total"`
	} `json:"usage"`
	CloseReason *string        `json:"close_reason"`
	Details     map[string]any `json:"details"`
}

func randomPrefixedID(prefix string, n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw), nil
}

func deriveExternalBaseURL(r *http.Request) (string, error) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xfp := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); xfp != "" {
		scheme = strings.TrimSpace(strings.Split(xfp, ",")[0])
	}
	if r.Host == "" {
		return "", errors.New("request host is empty")
	}
	return scheme + "://" + r.Host, nil
}

func sessionTimeString(ts *time.Time) *string {
	if ts == nil || ts.IsZero() {
		return nil
	}
	v := ts.UTC().Format(time.RFC3339)
	return &v
}

func runwayEstimate(balance, pricePerUnit *big.Int) int64 {
	if balance == nil || pricePerUnit == nil || pricePerUnit.Sign() <= 0 || balance.Sign() <= 0 {
		return 0
	}
	q := new(big.Int).Div(new(big.Int).Set(balance), pricePerUnit)
	if !q.IsInt64() {
		return math.MaxInt64
	}
	return q.Int64()
}

func decodePaymentHeader(r *http.Request) ([]byte, error) {
	header := r.Header.Get(livepeerheader.Payment)
	if header == "" {
		return nil, errors.New("missing Livepeer-Payment header")
	}
	return base64.StdEncoding.DecodeString(header)
}

func (s *Server) isRemoteLiveOpenRequest(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if !isLiveRunnerMode(r.Header.Get(livepeerheader.Mode)) {
		return false
	}
	capID := r.Header.Get(livepeerheader.Capability)
	offID := r.Header.Get(livepeerheader.Offering)
	group, found := s.groupFor(capID, offID)
	if !found || group == nil || len(group.Backends) == 0 {
		return false
	}
	cap, err := s.selectBackend(group)
	if err != nil || cap == nil {
		return false
	}
	return cap.Backend.Transport == remoteLiveRunnerTransport
}

func (s *Server) currentLiveRunnerMinRunwayUnits() uint64 {
	if s.opts.InterimDebit.MinRunwayUnits > 0 {
		return s.opts.InterimDebit.MinRunwayUnits
	}
	return 1
}

func (s *Server) dispatchCapPost(paidChain func(http.Handler) http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isRemoteLiveOpenRequest(r) {
			s.liveOpenSession(w, r)
			return
		}
		paidChain(http.HandlerFunc(s.dispatch)).ServeHTTP(w, r)
	})
}

func (s *Server) dispatchCapEnd(w http.ResponseWriter, r *http.Request) {
	sessID := r.PathValue("session_id")
	if sessID != "" && s.liveRunnerStore != nil && s.liveRunnerStore.Get(sessID) != nil {
		s.liveEndSession(w, r)
		return
	}
	s.rtmpCloseSession(w, r)
}

func (s *Server) liveOpenSession(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(livepeerheader.Capability) == "" {
		livepeerheader.WriteBadRequest(w, "missing required header: "+livepeerheader.Capability)
		return
	}
	if r.Header.Get(livepeerheader.Offering) == "" {
		livepeerheader.WriteBadRequest(w, "missing required header: "+livepeerheader.Offering)
		return
	}
	if r.Header.Get(livepeerheader.SpecVersion) == "" {
		livepeerheader.WriteBadRequest(w, "missing required header: "+livepeerheader.SpecVersion)
		return
	}
	mode := r.Header.Get(livepeerheader.Mode)
	if !isLiveRunnerMode(mode) {
		livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported, livepeerheader.ErrModeUnsupported, "unsupported mode for remote live runner open")
		return
	}
	capID := r.Header.Get(livepeerheader.Capability)
	offID := r.Header.Get(livepeerheader.Offering)
	group, found := s.groupFor(capID, offID)
	if !found || group == nil || len(group.Backends) == 0 {
		livepeerheader.WriteError(w, http.StatusNotFound, livepeerheader.ErrCapabilityNotServed, "capability "+capID+"/"+offID+" is not served by this broker")
		return
	}
	cap, err := s.selectBackend(group)
	if err != nil || cap == nil {
		livepeerheader.WriteError(w, http.StatusServiceUnavailable, livepeerheader.ErrCapacityExhausted, "no eligible backend currently available for "+capID+"/"+offID)
		return
	}
	releaseBackend, reserved := s.reserveBackend(cap)
	if !reserved {
		livepeerheader.WriteError(w, http.StatusServiceUnavailable, livepeerheader.ErrCapacityExhausted, "selected backend is at max in-flight capacity")
		return
	}
	capacityHeldBySession := false
	defer func() {
		if !capacityHeldBySession {
			releaseBackend()
		}
	}()
	if cap.Backend.Transport != remoteLiveRunnerTransport {
		livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported, livepeerheader.ErrModeUnsupported, "selected backend is not a remote live runner")
		return
	}
	spec, ok := s.lookupSpec(capID, offID)
	if !ok {
		livepeerheader.WriteError(w, http.StatusNotFound, livepeerheader.ErrCapabilityNotServed, "capability "+capID+"/"+offID+" is not served by this broker")
		return
	}
	paymentBytes, err := decodePaymentHeader(r)
	if err != nil {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid, err.Error())
		return
	}
	if err := middleware.ValidateExpectedPriceForRequest(paymentBytes, capID, offID, spec); err != nil {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch, "expected price mismatch: "+err.Error())
		return
	}
	var req liveSessionOpenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		livepeerheader.WriteBadRequest(w, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.GatewaySessionID) == "" {
		livepeerheader.WriteBadRequest(w, "gateway_session_id is required")
		return
	}
	if mode == livesessiongatewayingest.Mode {
		if err := validateGatewayIngestOpenRequest(req); err != nil {
			livepeerheader.WriteBadRequest(w, err.Error())
			return
		}
	}
	if req.SessionParams == nil {
		req.SessionParams = map[string]any{}
	}

	brokerSessionID, err := randomPrefixedID("bsess_", 16)
	if err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError, "broker session id: "+err.Error())
		return
	}
	callbackToken, err := randomPrefixedID("cb_", 16)
	if err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError, "callback token: "+err.Error())
		return
	}
	workID := uuid.NewString()
	if _, err := s.payment.OpenSession(r.Context(), payment.OpenSessionRequest{
		WorkID:              workID,
		Capability:          capID,
		Offering:            offID,
		PricePerWorkUnitWei: spec.PricePerWorkUnitWei,
		WorkUnit:            spec.WorkUnit,
	}); err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError, "open payment session: "+err.Error())
		return
	}
	paymentResult, err := s.payment.ProcessPayment(r.Context(), payment.ProcessPaymentRequest{
		WorkID:       workID,
		PaymentBytes: paymentBytes,
	})
	if err != nil {
		code, errCode := middlewareMapClientErr(err)
		livepeerheader.WriteError(w, code, errCode, "process payment: "+err.Error())
		return
	}

	baseURL, err := deriveExternalBaseURL(r)
	if err != nil {
		_ = s.payment.CloseSession(r.Context(), paymentResult.Sender, workID)
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError, "derive broker base URL: "+err.Error())
		return
	}
	runnerResp, err := s.liveRunnerClient.CreateSession(r.Context(), cap, liveRunnerCreateRequest{
		BrokerSessionID:  brokerSessionID,
		WorkID:           workID,
		CapabilityID:     capID,
		OfferingID:       offID,
		SessionParams:    req.SessionParams,
		OutputCredential: req.OutputCredential,
		IngestAccept:     req.IngestAccept,
		BrokerCallbacks: liveRunnerCallbacksReq{
			EventURL:  baseURL + "/internal/v1/live/events",
			AuthToken: callbackToken,
		},
	}, s.secrets)
	if err != nil {
		_ = s.payment.CloseSession(r.Context(), paymentResult.Sender, workID)
		livepeerheader.WriteError(w, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable, "create runner session: "+err.Error())
		return
	}
	if err := validateRunnerCreateResponse(mode, runnerResp); err != nil {
		_ = s.payment.CloseSession(r.Context(), paymentResult.Sender, workID)
		livepeerheader.WriteError(w, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable, "runner create session response: "+err.Error())
		return
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	if runnerResp.ExpiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, runnerResp.ExpiresAt); err == nil && !parsed.IsZero() {
			expiresAt = parsed.UTC()
		}
	} else if createdAt, err := time.Parse(time.RFC3339, runnerResp.CreatedAt); err == nil && !createdAt.IsZero() {
		expiresAt = createdAt.UTC().Add(time.Hour)
	}
	sess := &liveRunnerSession{
		BrokerSessionID:  brokerSessionID,
		GatewaySessionID: req.GatewaySessionID,
		RunnerSessionID:  runnerResp.RunnerSessionID,
		WorkID:           workID,
		CapabilityID:     capID,
		OfferingID:       offID,
		Sender:           append([]byte(nil), paymentResult.Sender...),
		CallbackToken:    callbackToken,
		RequestID:        middleware.RequestIDFromContext(r.Context()),
		Mode:             mode,
		State:            runnerResp.State,
		ExpiresAt:        expiresAt,
		CreatedAt:        time.Now().UTC(),
		EventIDs:         map[string]struct{}{},
		InitialStreamKey: runnerResp.Media.Ingest.StreamKey,
		IngestRTMPURL:    runnerResp.Media.Ingest.RTMPURL,
		PlaybackHLSURL:   runnerResp.Media.Playback.HLSURL,
		PrivateIngestURL: runnerResp.PrivateIngestURL,
		Backend:          cap.Backend,
		CapacityRelease:  releaseBackend,
	}
	if err := s.liveRunnerStore.Add(sess); err != nil {
		_ = s.payment.CloseSession(r.Context(), paymentResult.Sender, workID)
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError, "session store: "+err.Error())
		return
	}
	capacityHeldBySession = true

	resp := liveSessionOpenResponse{
		BrokerSessionID: brokerSessionID,
		RunnerSessionID: runnerResp.RunnerSessionID,
		WorkID:          workID,
		State:           runnerResp.State,
		ExpiresAt:       expiresAt.Format(time.RFC3339),
	}
	if mode == sessioncontrolexternalmedia.Mode {
		resp.GatewaySessionID = req.GatewaySessionID
		resp.Media = &struct {
			Ingest struct {
				RTMPURL   string `json:"rtmp_url"`
				StreamKey string `json:"stream_key,omitempty"`
			} `json:"ingest"`
			Playback struct {
				HLSURL string `json:"hls_url"`
			} `json:"playback"`
		}{}
		resp.Media.Ingest.RTMPURL = runnerResp.Media.Ingest.RTMPURL
		resp.Media.Ingest.StreamKey = runnerResp.Media.Ingest.StreamKey
		resp.Media.Playback.HLSURL = runnerResp.Media.Playback.HLSURL
	} else {
		resp.PrivateIngestURL = runnerResp.PrivateIngestURL
	}
	resp.Control.TopupURL = baseURL + "/v1/cap/" + brokerSessionID + "/topup"
	resp.Control.StatusURL = baseURL + "/v1/cap/" + brokerSessionID
	resp.Control.EndURL = baseURL + "/v1/cap/" + brokerSessionID + "/end"

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) liveTopupSession(w http.ResponseWriter, r *http.Request) {
	sess := s.liveRunnerStore.Get(r.PathValue("session_id"))
	if sess == nil {
		http.NotFound(w, r)
		return
	}
	paymentBytes, err := decodePaymentHeader(r)
	if err != nil {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid, err.Error())
		return
	}
	spec, ok := s.lookupSpec(sess.CapabilityID, sess.OfferingID)
	if !ok {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError, "capability pricing not found")
		return
	}
	if err := middleware.ValidateExpectedPriceForRequest(paymentBytes, sess.CapabilityID, sess.OfferingID, spec); err != nil {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch, "expected price mismatch: "+err.Error())
		return
	}
	var req liveSessionTopupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		livepeerheader.WriteBadRequest(w, "invalid JSON body: "+err.Error())
		return
	}

	sess.mu.Lock()
	if req.GatewaySessionID != "" && req.GatewaySessionID != sess.GatewaySessionID {
		sess.mu.Unlock()
		livepeerheader.WriteBadRequest(w, "gateway_session_id does not match broker session")
		return
	}
	workID := sess.WorkID
	state := sess.State
	sess.mu.Unlock()

	res, err := s.payment.ProcessPayment(r.Context(), payment.ProcessPaymentRequest{
		WorkID:       workID,
		PaymentBytes: paymentBytes,
	})
	if err != nil {
		code, errCode := middlewareMapClientErr(err)
		livepeerheader.WriteError(w, code, errCode, "process payment: "+err.Error())
		return
	}
	resp := liveSessionTopupResponse{
		BrokerSessionID: sess.BrokerSessionID,
		WorkID:          workID,
		State:           state,
	}
	resp.Balance.Status = "ok"
	resp.Balance.RunwaySecondsEstimate = runwayEstimate(res.Balance, spec.PricePerWorkUnitWei)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) liveGetSession(w http.ResponseWriter, r *http.Request) {
	sess := s.liveRunnerStore.Get(r.PathValue("session_id"))
	if sess == nil {
		http.NotFound(w, r)
		return
	}
	sess.mu.Lock()
	resp := liveSessionStatusResponse{
		GatewaySessionID: sess.GatewaySessionID,
		BrokerSessionID:  sess.BrokerSessionID,
		RunnerSessionID:  sess.RunnerSessionID,
		WorkID:           sess.WorkID,
		State:            sess.State,
		StartedAt:        sessionTimeString(sess.StartedAt),
		LastHeartbeatAt:  sessionTimeString(sess.LastHeartbeatAt),
		EndedAt:          sessionTimeString(sess.EndedAt),
		CloseReason:      sess.CloseReason,
	}
	if sess.Mode == sessioncontrolexternalmedia.Mode {
		resp.Media = &struct {
			Ingest struct {
				RTMPURL string `json:"rtmp_url"`
			} `json:"ingest"`
			Playback struct {
				HLSURL string `json:"hls_url"`
			} `json:"playback"`
		}{}
		resp.Media.Ingest.RTMPURL = sess.IngestRTMPURL
		resp.Media.Playback.HLSURL = sess.PlaybackHLSURL
	}
	sess.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) liveEndSession(w http.ResponseWriter, r *http.Request) {
	sess := s.liveRunnerStore.Get(r.PathValue("session_id"))
	if sess == nil {
		http.NotFound(w, r)
		return
	}
	var req liveSessionEndRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if strings.TrimSpace(req.Reason) == "" {
		req.Reason = "gateway_close"
	}
	resp, _ := s.finalizeLiveRunnerSession(r.Context(), sess, req.Reason, true)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) liveRunnerEvents(w http.ResponseWriter, r *http.Request) {
	var req liveRunnerEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		livepeerheader.WriteBadRequest(w, "invalid JSON body: "+err.Error())
		return
	}
	sess := s.liveRunnerStore.Get(req.BrokerSessionID)
	if sess == nil {
		http.NotFound(w, r)
		return
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	want := "Bearer " + sess.CallbackToken
	if authz != want {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if req.RunnerSessionID != "" && req.RunnerSessionID != sess.RunnerSessionID {
		livepeerheader.WriteBadRequest(w, "runner_session_id does not match broker session")
		return
	}

	eventTime := time.Now().UTC()
	if parsed, err := time.Parse(time.RFC3339, req.EventTime); err == nil {
		eventTime = parsed.UTC()
	}

	var forceInsufficient bool
	var shouldFinalize bool
	var finalizeReason string

	sess.mu.Lock()
	if sess.EventIDs == nil {
		sess.EventIDs = map[string]struct{}{}
	}
	if _, ok := sess.EventIDs[req.EventID]; ok {
		sess.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}
	if req.Sequence <= sess.LastSequence && req.EventID != "" {
		sess.mu.Unlock()
		http.Error(w, "out-of-order sequence", http.StatusConflict)
		return
	}
	sess.EventIDs[req.EventID] = struct{}{}
	sess.LastSequence = req.Sequence
	if req.State != "" {
		sess.State = req.State
	}
	switch req.EventType {
	case "session.started":
		sess.StartedAt = &eventTime
		sess.LastHeartbeatAt = &eventTime
		if sess.State == "" {
			sess.State = "publishing"
		}
	case "session.ready":
		sess.LastHeartbeatAt = &eventTime
		if sess.State == "" || sess.State == "provisioning" {
			sess.State = "ready"
		}
	case "session.publish_started":
		sess.StartedAt = &eventTime
		sess.LastHeartbeatAt = &eventTime
		if req.State == "" {
			sess.State = "publishing"
		}
	case "session.publish_stopped":
		sess.LastHeartbeatAt = &eventTime
		if req.State == "" {
			sess.State = "stalled"
		}
	case "session.heartbeat":
		sess.LastHeartbeatAt = &eventTime
	case "session.usage.tick":
		sess.LastHeartbeatAt = &eventTime
	case "session.upload.healthy":
		sess.LastHeartbeatAt = &eventTime
		if req.State == "" && sess.State == "publishing" {
			sess.State = "uploading"
		}
	case "session.upload.failed":
		sess.LastHeartbeatAt = &eventTime
		shouldFinalize = true
		if req.CloseReason != nil {
			finalizeReason = *req.CloseReason
		} else {
			finalizeReason = "output_upload_failed"
		}
	case "session.failed":
		sess.LastHeartbeatAt = &eventTime
		shouldFinalize = true
		if req.CloseReason != nil {
			finalizeReason = *req.CloseReason
		} else {
			finalizeReason = "runner_failed"
		}
	case "session.ended":
		sess.LastHeartbeatAt = &eventTime
		shouldFinalize = true
		if req.CloseReason != nil {
			finalizeReason = *req.CloseReason
		} else {
			finalizeReason = "runner_ended"
		}
	}
	var usageDelta uint64
	if req.Usage.Total > sess.ReportedUsageTotal {
		usageDelta = req.Usage.Total - sess.ReportedUsageTotal
		sess.ReportedUsageTotal = req.Usage.Total
		sess.ReportedUsageUnit = req.Usage.Unit
	}
	workID := sess.WorkID
	sender := append([]byte(nil), sess.Sender...)
	debitSeq := sess.DebitSeq + 1
	sess.mu.Unlock()

	if usageDelta > 0 {
		if _, err := s.payment.DebitBalance(r.Context(), payment.DebitBalanceRequest{
			Sender:    sender,
			WorkID:    workID,
			WorkUnits: int64(usageDelta),
			DebitSeq:  debitSeq,
		}); err != nil {
			livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError, "debit balance: "+err.Error())
			return
		}
		sess.mu.Lock()
		sess.DebitSeq = debitSeq
		sess.DebitedUsageTotal += usageDelta
		sess.mu.Unlock()
		res, err := s.payment.SufficientBalance(r.Context(), payment.SufficientBalanceRequest{
			Sender:       sender,
			WorkID:       workID,
			MinWorkUnits: int64(s.currentLiveRunnerMinRunwayUnits()),
		})
		if err != nil {
			livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError, "sufficient balance: "+err.Error())
			return
		}
		if !res.Sufficient {
			forceInsufficient = true
		}
	}

	if forceInsufficient {
		_, _ = s.finalizeLiveRunnerSession(r.Context(), sess, livepeerheader.ErrInsufficientBalance, true)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if shouldFinalize {
		_, _ = s.finalizeLiveRunnerSession(r.Context(), sess, finalizeReason, false)
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) finalizeLiveRunnerSession(ctx context.Context, sess *liveRunnerSession, reason string, terminateRunner bool) (liveSessionEndResponse, error) {
	sess.mu.Lock()
	if sess.CloseReason != nil && sess.EndedAt != nil {
		resp := liveSessionEndResponse{
			BrokerSessionID: sess.BrokerSessionID,
			RunnerSessionID: sess.RunnerSessionID,
			State:           sess.State,
			CloseReason:     sess.CloseReason,
			EndedAt:         sessionTimeString(sess.EndedAt),
		}
		sess.mu.Unlock()
		return resp, nil
	}
	now := time.Now().UTC()
	reasonCopy := reason
	sess.CloseReason = &reasonCopy
	sess.EndedAt = &now
	if sess.State == "failed" || reason == "runner_failed" || strings.Contains(reason, "runner_") {
		sess.State = "failed"
	} else {
		sess.State = "ended"
	}
	workID := sess.WorkID
	sender := append([]byte(nil), sess.Sender...)
	needClosePayment := !sess.PaymentClosed
	sess.PaymentClosed = true
	releaseBackend := sess.CapacityRelease
	sess.CapacityRelease = nil
	sess.mu.Unlock()

	if releaseBackend != nil {
		releaseBackend()
	}
	if terminateRunner {
		_, _ = s.liveRunnerClient.DeleteSession(ctx, sess, reason, s.secrets)
	}
	if needClosePayment {
		_ = s.payment.CloseSession(ctx, sender, workID)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return liveSessionEndResponse{
		BrokerSessionID: sess.BrokerSessionID,
		RunnerSessionID: sess.RunnerSessionID,
		State:           sess.State,
		CloseReason:     sess.CloseReason,
		EndedAt:         sessionTimeString(sess.EndedAt),
	}, nil
}

func validateGatewayIngestOpenRequest(req liveSessionOpenRequest) error {
	if req.OutputCredential == nil {
		return errors.New("output_credential is required")
	}
	if req.IngestAccept == nil {
		return errors.New("ingest_accept is required")
	}
	required := map[string]string{
		"output_credential.endpoint":          req.OutputCredential.Endpoint,
		"output_credential.region":            req.OutputCredential.Region,
		"output_credential.bucket":            req.OutputCredential.Bucket,
		"output_credential.key_prefix":        req.OutputCredential.KeyPrefix,
		"output_credential.access_key_id":     req.OutputCredential.AccessKeyID,
		"output_credential.secret_access_key": req.OutputCredential.SecretAccessKey,
		"output_credential.session_token":     req.OutputCredential.SessionToken,
		"output_credential.expires_at":        req.OutputCredential.ExpiresAt,
		"ingest_accept.stream_key":            req.IngestAccept.StreamKey,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", field)
		}
	}
	return nil
}

func validateRunnerCreateResponse(mode string, resp *liveRunnerCreateResponse) error {
	if resp == nil {
		return errors.New("missing response")
	}
	if strings.TrimSpace(resp.RunnerSessionID) == "" {
		return errors.New("runner_session_id is required")
	}
	if strings.TrimSpace(resp.State) == "" {
		return errors.New("state is required")
	}
	switch mode {
	case livesessiongatewayingest.Mode:
		if strings.TrimSpace(resp.PrivateIngestURL) == "" {
			return errors.New("private_ingest_url is required for gateway-ingest mode")
		}
	case sessioncontrolexternalmedia.Mode:
		if strings.TrimSpace(resp.Media.Ingest.RTMPURL) == "" {
			return errors.New("media.ingest.rtmp_url is required for remote-runner mode")
		}
		if strings.TrimSpace(resp.Media.Ingest.StreamKey) == "" {
			return errors.New("media.ingest.stream_key is required for remote-runner mode")
		}
		if strings.TrimSpace(resp.Media.Playback.HLSURL) == "" {
			return errors.New("media.playback.hls_url is required for remote-runner mode")
		}
	}
	return nil
}

func middlewareMapClientErr(err error) (int, string) {
	if errors.Is(err, errors.ErrUnsupported) {
		return http.StatusInternalServerError, livepeerheader.ErrInternalError
	}
	return http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid
}
