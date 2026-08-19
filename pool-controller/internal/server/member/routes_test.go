package member

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/memberenrollment"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestValidateJoinRequestRejectsOutOfPoolScopeClaim(t *testing.T) {
	cases := []struct {
		name          string
		capabilityID  string
		protocol      string
		wantSubstring string
	}{
		{
			name:          "video live rtmp capability",
			capabilityID:  "video:live.rtmp",
			protocol:      "paid-job/v1",
			wantSubstring: "video:live.rtmp",
		},
		{
			name:          "paid session protocol",
			capabilityID:  "openai:chat-completions",
			protocol:      "paid-session/v1",
			wantSubstring: "paid-session/v1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := types.JoinRequest{
				MemberEthAddress: "0xabc",
				PayoutMode:       "onchain",
				RequestedBackends: []types.RequestedBackend{{
					ID:        "backend-1",
					Transport: "http",
					URL:       "http://backend",
					ClaimedCapabilities: []types.ClaimedOffer{{
						CapabilityID: tc.capabilityID,
						Protocol:     tc.protocol,
					}},
				}},
			}
			err := validateJoinRequest(req)
			if err == nil {
				t.Fatalf("validateJoinRequest() expected rejection, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstring) {
				t.Fatalf("validateJoinRequest() err = %q, missing %q", err.Error(), tc.wantSubstring)
			}
			if !strings.Contains(err.Error(), "0032") {
				t.Fatalf("validateJoinRequest() err = %q, expected reference to plan 0032", err.Error())
			}
		})
	}
}

func TestValidateJoinRequestAllowsSupportedClaim(t *testing.T) {
	req := types.JoinRequest{
		MemberEthAddress: "0xabc",
		PayoutMode:       "onchain",
		RequestedBackends: []types.RequestedBackend{{
			ID:        "backend-1",
			Transport: "http",
			URL:       "http://backend",
			ClaimedCapabilities: []types.ClaimedOffer{{
				CapabilityID: "openai:chat-completions",
				Protocol:     "paid-job/v1",
			}},
		}},
	}
	if err := validateJoinRequest(req); err != nil {
		t.Fatalf("validateJoinRequest() unexpected error = %v", err)
	}
}

func TestConnectedMemberSignupBundleAndHardwareRoutes(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()

	mux := http.NewServeMux()
	Register(mux, Deps{Repo: stateRepo, Enrollment: memberenrollment.New(stateRepo), Sessions: NewSessionAuth()})
	server := httptest.NewServer(mux)
	defer server.Close()

	nonceResp := postJSON(t, server.URL+"/member/v1/auth/nonce", map[string]any{"member_eth_address": addr}, nil)
	var nonce memberenrollment.NonceIssueResult
	decodeJSON(t, nonceResp, &nonce)

	signature, err := crypto.Sign(accounts.TextHash([]byte(nonce.Message)), key)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	signature[64] += 27
	verifyResp := postJSON(t, server.URL+"/member/v1/auth/verify", map[string]any{
		"nonce_id":   nonce.NonceID,
		"signature":  "0x" + hex.EncodeToString(signature),
		"contact":    "ops@example.com",
		"host_label": "ignored",
	}, nil)
	if verifyResp.StatusCode != http.StatusOK {
		t.Fatalf("verify status=%d", verifyResp.StatusCode)
	}
	cookie := firstCookie(t, verifyResp, memberSessionCookieName)

	enrollResp := postJSON(t, server.URL+"/member/v1/enrollments", map[string]any{"host_label": "rig-1"}, cookie)
	var enrollment struct {
		memberenrollment.CreateEnrollmentResult
		BundleURL string `json:"bundle_url"`
	}
	decodeJSON(t, enrollResp, &enrollment)
	if enrollment.Enrollment.ID == "" || enrollment.Token == "" || enrollment.BundleURL == "" {
		t.Fatalf("incomplete enrollment response: %+v", enrollment)
	}

	bundleReq, err := http.NewRequest(http.MethodGet, server.URL+enrollment.BundleURL, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	bundleReq.Header.Set("Authorization", "Bearer "+enrollment.Token)
	bundleResp, err := http.DefaultClient.Do(bundleReq)
	if err != nil {
		t.Fatalf("bundle request error = %v", err)
	}
	defer func() { _ = bundleResp.Body.Close() }()
	if bundleResp.StatusCode != http.StatusOK {
		t.Fatalf("bundle status=%d", bundleResp.StatusCode)
	}
	body := readBody(t, bundleResp)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip.NewReader() error = %v", err)
	}
	if !zipHasFile(zr, "docker-compose.yaml") || !zipHasFile(zr, "pool-member-agent.yaml") {
		t.Fatalf("bundle missing expected files")
	}

	hardwareResp := postJSONWithBearer(t, server.URL+"/member/v1/enrollments/"+enrollment.Enrollment.ID+"/hardware", enrollment.Token, map[string]any{
		"hardware_units": []map[string]any{{
			"gpu_uuid":  "GPU-abc",
			"gpu_model": "NVIDIA GeForce RTX 4090",
		}},
	})
	if hardwareResp.StatusCode != http.StatusOK {
		t.Fatalf("hardware status=%d", hardwareResp.StatusCode)
	}
}

func postJSON(t *testing.T, url string, body any, cookie *http.Cookie) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s error = %v", url, err)
	}
	return resp
}

func postJSONWithBearer(t *testing.T, url, token string, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s error = %v", url, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(readBody(t, resp)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	return buf.Bytes()
}

func firstCookie(t *testing.T, resp *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range resp.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %s not found", name)
	return nil
}

func zipHasFile(zr *zip.Reader, name string) bool {
	for _, file := range zr.File {
		if file.Name == name {
			return true
		}
	}
	return false
}
