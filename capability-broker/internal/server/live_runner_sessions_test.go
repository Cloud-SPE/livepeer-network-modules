package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
)

type creditingPaymentClient struct {
	mock      *payment.Mock
	creditWei *big.Int
}

func newCreditingPaymentClient(creditWei int64) *creditingPaymentClient {
	return &creditingPaymentClient{
		mock:      payment.NewMock(),
		creditWei: big.NewInt(creditWei),
	}
}

func (c *creditingPaymentClient) GetTicketParams(ctx context.Context, req payment.GetTicketParamsRequest) (*payment.TicketParams, error) {
	return c.mock.GetTicketParams(ctx, req)
}

func (c *creditingPaymentClient) OpenSession(ctx context.Context, req payment.OpenSessionRequest) (*payment.OpenSessionResult, error) {
	return c.mock.OpenSession(ctx, req)
}

func (c *creditingPaymentClient) ProcessPayment(ctx context.Context, req payment.ProcessPaymentRequest) (*payment.ProcessPaymentResult, error) {
	res, err := c.mock.ProcessPayment(ctx, req)
	if err != nil {
		return nil, err
	}
	if c.creditWei.Sign() > 0 {
		if err := c.mock.CreditBalance(req.WorkID, c.creditWei); err != nil {
			return nil, err
		}
		bal, err := c.mock.GetBalance(ctx, res.Sender, req.WorkID)
		if err != nil {
			return nil, err
		}
		res.Balance = bal
	}
	return res, nil
}

func (c *creditingPaymentClient) DebitBalance(ctx context.Context, req payment.DebitBalanceRequest) (*big.Int, error) {
	return c.mock.DebitBalance(ctx, req)
}

func (c *creditingPaymentClient) SufficientBalance(ctx context.Context, req payment.SufficientBalanceRequest) (*payment.SufficientBalanceResult, error) {
	return c.mock.SufficientBalance(ctx, req)
}

func (c *creditingPaymentClient) GetBalance(ctx context.Context, sender []byte, workID string) (*big.Int, error) {
	return c.mock.GetBalance(ctx, sender, workID)
}

func (c *creditingPaymentClient) CloseSession(ctx context.Context, sender []byte, workID string) error {
	return c.mock.CloseSession(ctx, sender, workID)
}

func (c *creditingPaymentClient) Sessions() []payment.SessionRecord {
	return c.mock.Sessions()
}

type liveRunnerStub struct {
	t             *testing.T
	createCalls   int
	deleteCalls   int
	lastDeleteRaw map[string]any
}

func newLiveRunnerStub(t *testing.T) (*liveRunnerStub, *httptest.Server) {
	stub := &liveRunnerStub{t: t}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/video/live/sessions":
			stub.createCalls++
			var req liveRunnerCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if req.BrokerCallbacks.EventURL == "" || req.BrokerCallbacks.AuthToken == "" {
				t.Fatalf("missing broker callback fields: %+v", req.BrokerCallbacks)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(liveRunnerCreateResponse{
				RunnerSessionID: "rsess_test",
				State:           "ready",
				CreatedAt:       "2026-05-20T19:11:57Z",
				Media: liveRunnerMedia{
					Ingest: struct {
						RTMPURL   string `json:"rtmp_url"`
						StreamKey string `json:"stream_key,omitempty"`
					}{
						RTMPURL:   "rtmp://ingest.example.com/live",
						StreamKey: "lvk_secret",
					},
					Playback: struct {
						HLSURL string `json:"hls_url"`
					}{
						HLSURL: "https://playback.example.com/live/rsess_test/master.m3u8",
					},
				},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/video/live/sessions/rsess_test":
			stub.deleteCalls++
			var raw map[string]any
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				t.Fatalf("decode delete request: %v", err)
			}
			stub.lastDeleteRaw = raw
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(liveRunnerDeleteResponse{
				RunnerSessionID: "rsess_test",
				State:           "ended",
				CloseReason:     raw["reason"].(string),
				EndedAt:         "2026-05-20T19:14:23Z",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	return stub, srv
}

func newLiveRunnerTestServer(t *testing.T, creditWei int64, minRunwayUnits uint64) (*Server, *creditingPaymentClient, *liveRunnerStub, *httptest.Server) {
	t.Helper()
	runnerStub, runnerHTTP := newLiveRunnerStub(t)
	cfg := &config.Config{
		Capabilities: []config.Capability{{
			ID:              "livepeer:transcode/live-rtmp-hls-abr",
			OfferingID:      "default",
			InteractionMode: "live-session-remote-runner@v0",
			WorkUnit: config.WorkUnit{
				Name: "output_seconds",
				Extractor: map[string]any{
					"type":        "seconds-elapsed",
					"granularity": 1,
				},
			},
			Price: config.Price{
				AmountWei: "1",
				PerUnits:  1,
			},
			Backend: config.Backend{
				ID:        "runner-a",
				Transport: remoteLiveRunnerTransport,
				LiveRunner: &config.LiveRunnerBackend{
					BaseURL: runnerHTTP.URL,
				},
			},
			Health: config.Health{InitialStatus: "ready"},
		}},
	}
	paymentClient := newCreditingPaymentClient(creditWei)
	srv := &Server{
		cfg:              cfg,
		mux:              http.NewServeMux(),
		payment:          paymentClient,
		health:           health.New(cfg),
		liveRunnerStore:  newLiveRunnerSessionStore(),
		liveRunnerClient: newLiveRunnerBackendClient(),
		opts: Options{
			InterimDebit: middleware.InterimDebitConfig{MinRunwayUnits: minRunwayUnits},
		},
	}
	srv.registerRoutes()
	httpSrv := httptest.NewServer(srv.mux)
	t.Cleanup(func() {
		httpSrv.Close()
		runnerHTTP.Close()
	})
	return srv, paymentClient, runnerStub, httpSrv
}

func liveOpen(t *testing.T, httpSrv *httptest.Server) liveSessionOpenResponse {
	t.Helper()
	body := `{"gateway_session_id":"gw-123","session_params":{"name":"launch-stream"}}`
	req, err := http.NewRequest(http.MethodPost, httpSrv.URL+"/v1/cap", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set(livepeerheader.Capability, "livepeer:transcode/live-rtmp-hls-abr")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Mode, "live-session-remote-runner@v0")
	req.Header.Set(livepeerheader.SpecVersion, "0.1")
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("dummy-payment")))
	req.Header.Set(livepeerheader.RequestID, "req-open")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open status=%d", resp.StatusCode)
	}
	var out liveSessionOpenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode open response: %v", err)
	}
	return out
}

func TestRemoteLiveRunnerOpenPublishTopupAndClose(t *testing.T) {
	srv, paymentClient, runnerStub, httpSrv := newLiveRunnerTestServer(t, 10, 2)
	openResp := liveOpen(t, httpSrv)
	if openResp.BrokerSessionID == "" || openResp.WorkID == "" || openResp.Media.Ingest.StreamKey == "" {
		t.Fatalf("unexpected open response: %+v", openResp)
	}
	if runnerStub.createCalls != 1 {
		t.Fatalf("create calls=%d want=1", runnerStub.createCalls)
	}

	eventsURL := httpSrv.URL + "/internal/v1/live/events"
	startPayload := `{"broker_session_id":"` + openResp.BrokerSessionID + `","runner_session_id":"` + openResp.RunnerSessionID + `","event_id":"evt-1","sequence":1,"event_type":"session.started","event_time":"2026-05-20T19:12:09Z","state":"publishing","usage":{"unit":"output_seconds","delta":0,"total":0},"details":{}}`
	sess := paymentClient.Sessions()
	if len(sess) != 1 {
		t.Fatalf("sessions=%d want=1", len(sess))
	}
	serverSessReq, _ := http.NewRequest(http.MethodPost, eventsURL, bytes.NewBufferString(startPayload))
	serverSessReq.Header.Set("Authorization", "Bearer "+mustCallbackToken(t, srv, openResp.BrokerSessionID))
	serverSessReq.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(serverSessReq); err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("start event err=%v status=%v", err, respStatus(resp))
	}

	usagePayload := `{"broker_session_id":"` + openResp.BrokerSessionID + `","runner_session_id":"` + openResp.RunnerSessionID + `","event_id":"evt-2","sequence":2,"event_type":"session.usage.tick","event_time":"2026-05-20T19:13:00Z","state":"publishing","usage":{"unit":"output_seconds","delta":5,"total":5},"details":{}}`
	serverSessReq, _ = http.NewRequest(http.MethodPost, eventsURL, bytes.NewBufferString(usagePayload))
	serverSessReq.Header.Set("Authorization", "Bearer "+mustCallbackToken(t, srv, openResp.BrokerSessionID))
	serverSessReq.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(serverSessReq); err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("usage event err=%v status=%v", err, respStatus(resp))
	}

	statusResp, err := http.Get(httpSrv.URL + "/v1/cap/" + openResp.BrokerSessionID)
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	defer statusResp.Body.Close()
	var status liveSessionStatusResponse
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.State != "publishing" || status.StartedAt == nil || status.LastHeartbeatAt == nil {
		t.Fatalf("unexpected status after publish: %+v", status)
	}

	topupReq, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/v1/cap/"+openResp.BrokerSessionID+"/topup", bytes.NewBufferString(`{"gateway_session_id":"gw-123"}`))
	topupReq.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("dummy-payment-topup")))
	topupReq.Header.Set("Content-Type", "application/json")
	topupResp, err := http.DefaultClient.Do(topupReq)
	if err != nil {
		t.Fatalf("topup request: %v", err)
	}
	defer topupResp.Body.Close()
	var topup liveSessionTopupResponse
	if err := json.NewDecoder(topupResp.Body).Decode(&topup); err != nil {
		t.Fatalf("decode topup: %v", err)
	}
	if topup.Balance.RunwaySecondsEstimate <= 0 {
		t.Fatalf("unexpected topup runway: %+v", topup)
	}

	endReq, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/v1/cap/"+openResp.BrokerSessionID+"/end", bytes.NewBufferString(`{"reason":"gateway_close"}`))
	endReq.Header.Set("Content-Type", "application/json")
	endResp, err := http.DefaultClient.Do(endReq)
	if err != nil {
		t.Fatalf("end request: %v", err)
	}
	defer endResp.Body.Close()
	var ended liveSessionEndResponse
	if err := json.NewDecoder(endResp.Body).Decode(&ended); err != nil {
		t.Fatalf("decode end: %v", err)
	}
	if ended.CloseReason == nil || *ended.CloseReason != "gateway_close" {
		t.Fatalf("unexpected end response: %+v", ended)
	}
	if runnerStub.deleteCalls != 1 {
		t.Fatalf("delete calls=%d want=1", runnerStub.deleteCalls)
	}
	if sessions := paymentClient.Sessions(); len(sessions) != 1 || !sessions[0].Closed {
		t.Fatalf("payment session not closed: %+v", sessions)
	}
}

func TestRemoteLiveRunnerInsufficientBalanceForcesRunnerShutdown(t *testing.T) {
	srv, paymentClient, runnerStub, httpSrv := newLiveRunnerTestServer(t, 2, 2)
	openResp := liveOpen(t, httpSrv)
	token := mustCallbackToken(t, srv, openResp.BrokerSessionID)
	usagePayload := `{"broker_session_id":"` + openResp.BrokerSessionID + `","runner_session_id":"` + openResp.RunnerSessionID + `","event_id":"evt-1","sequence":1,"event_type":"session.usage.tick","event_time":"2026-05-20T19:13:00Z","state":"publishing","usage":{"unit":"output_seconds","delta":1,"total":1},"details":{}}`
	req, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/internal/v1/live/events", bytes.NewBufferString(usagePayload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("usage request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("usage status=%d", resp.StatusCode)
	}
	if runnerStub.deleteCalls != 1 {
		t.Fatalf("delete calls=%d want=1", runnerStub.deleteCalls)
	}
	statusResp, err := http.Get(httpSrv.URL + "/v1/cap/" + openResp.BrokerSessionID)
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	defer statusResp.Body.Close()
	var status liveSessionStatusResponse
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.CloseReason == nil || *status.CloseReason != livepeerheader.ErrInsufficientBalance {
		t.Fatalf("unexpected insufficient-balance status: %+v", status)
	}
	if sessions := paymentClient.Sessions(); len(sessions) != 1 || !sessions[0].Closed {
		t.Fatalf("payment session not closed: %+v", sessions)
	}
}

func TestRemoteLiveRunnerFailureClosesPaymentState(t *testing.T) {
	srv, paymentClient, runnerStub, httpSrv := newLiveRunnerTestServer(t, 10, 2)
	openResp := liveOpen(t, httpSrv)
	token := mustCallbackToken(t, srv, openResp.BrokerSessionID)
	failPayload := `{"broker_session_id":"` + openResp.BrokerSessionID + `","runner_session_id":"` + openResp.RunnerSessionID + `","event_id":"evt-1","sequence":1,"event_type":"session.failed","event_time":"2026-05-20T19:13:00Z","state":"failed","close_reason":"runner_crash","usage":{"unit":"output_seconds","delta":0,"total":0},"details":{}}`
	req, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/internal/v1/live/events", bytes.NewBufferString(failPayload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed-event request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("failed-event status=%d", resp.StatusCode)
	}
	if runnerStub.deleteCalls != 0 {
		t.Fatalf("delete calls=%d want=0", runnerStub.deleteCalls)
	}
	statusResp, err := http.Get(httpSrv.URL + "/v1/cap/" + openResp.BrokerSessionID)
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	defer statusResp.Body.Close()
	var status liveSessionStatusResponse
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.State != "failed" || status.CloseReason == nil || *status.CloseReason != "runner_crash" {
		t.Fatalf("unexpected failure status: %+v", status)
	}
	if sessions := paymentClient.Sessions(); len(sessions) != 1 || !sessions[0].Closed {
		t.Fatalf("payment session not closed: %+v", sessions)
	}
}

func mustCallbackToken(t *testing.T, srv *Server, brokerSessionID string) string {
	t.Helper()
	sess := srv.liveRunnerStore.Get(brokerSessionID)
	if sess == nil {
		t.Fatalf("live session %q not found", brokerSessionID)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.CallbackToken
}

func respStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}
