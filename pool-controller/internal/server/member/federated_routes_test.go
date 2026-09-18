package member

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberauth"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/memberenrollment"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestFederatedIdentityJoinsRegionsExplicitlyAndCannotImpersonateAgent(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	trustPath := filepath.Join(t.TempDir(), "trust.json")
	trust := memberauth.Trust{Issuer: "member-portal", Keys: []memberauth.PublicKey{{ID: "key", PublicKey: base64.StdEncoding.EncodeToString(public), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}}}
	raw, _ := json.Marshal(trust)
	if err := os.WriteFile(trustPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	signer := memberauth.Signer{Issuer: trust.Issuer, KeyID: "key", PrivateKey: private}
	wallet := "0x" + strings.Repeat("1", 40)
	other := "0x" + strings.Repeat("2", 40)
	var stores []*repo.StateRepo
	var muxes []*http.ServeMux
	var tokens []string
	for _, region := range []string{"eu", "us"} {
		st, err := repo.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		term := types.RegionalTerms{PoolID: st.PoolID(), Version: region + "-v1", WindowRounds: 14, ParticipationRules: region, ZeroWorkToOperator: true, RoundingToOperator: true}
		if err := st.PutRegionalTerms(term); err != nil {
			t.Fatal(err)
		}
		sessions := NewSessionAuth()
		sessions.Federation = &memberauth.Verifier{Path: trustPath, PoolID: st.PoolID()}
		mux := http.NewServeMux()
		Register(mux, Deps{Repo: st, Sessions: sessions})
		token, err := signer.Sign(wallet, st.PoolID(), strings.Repeat("a", 64), now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		stores = append(stores, st)
		muxes = append(muxes, mux)
		tokens = append(tokens, token)
	}
	call := func(i int, method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "https://region.example"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", "https://region.example")
		w := httptest.NewRecorder()
		muxes[i].ServeHTTP(w, req)
		return w
	}
	for _, origin := range []string{"", "https://sibling.region.example"} {
		req := httptest.NewRequest("POST", "https://region.example/member/v1/enrollments", strings.NewReader(`{"host_label":"csrf"}`))
		req.Header.Set("Authorization", "Bearer "+tokens[0])
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		muxes[0].ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatal("enrollment CSRF accepted", origin, w.Code)
		}
	}
	if w := call(1, "GET", "/member/v1/regional-report", tokens[0], ""); w.Code != 401 {
		t.Fatal("cross-pool member report", w.Code)
	}
	if w := call(0, "GET", "/member/v1/regional-report?wallet="+other, tokens[0], ""); w.Code != 200 || !strings.Contains(w.Body.String(), wallet) || strings.Contains(w.Body.String(), other) {
		t.Fatal("report wallet not bound to authorization", w.Code, w.Body.String())
	}
	if w := call(1, "GET", "/member/v1/membership", tokens[0], ""); w.Code != 401 {
		t.Fatal("EU token authorized US", w.Code)
	}
	if w := call(0, "GET", "/member/v1/membership", tokens[0], ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"joined":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	members, _ := stores[0].ListPoolMembers()
	if len(members) != 0 {
		t.Fatal("sign-in or read silently joined region")
	}
	invalid := `{"pool_id":"` + stores[0].PoolID() + `","terms_version":"unknown"}`
	if w := call(0, "POST", "/member/v1/join", tokens[0], invalid); w.Code != 409 {
		t.Fatal("unknown terms joined")
	}
	members, _ = stores[0].ListPoolMembers()
	if len(members) != 0 {
		t.Fatal("failed join left membership")
	}
	for i, version := range []string{"eu-v1", "us-v1"} {
		body := `{"pool_id":"` + stores[i].PoolID() + `","terms_version":"` + version + `"}`
		if w := call(i, "POST", "/member/v1/join", tokens[i], body); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if i == 0 {
			members, _ := stores[1].ListPoolMembers()
			if len(members) != 0 {
				t.Fatal("EU join joined US")
			}
		}
		accepted, err := stores[i].RequireTermsAcceptance(wallet, version)
		if err != nil || accepted.PoolID != stores[i].PoolID() {
			t.Fatal("acceptance not regional", err)
		}
	}
	created, err := memberenrollment.New(stores[0]).CreateEnrollment(memberenrollment.CreateEnrollmentRequest{MemberEthAddress: wallet})
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := signer.Sign(other, stores[0].PoolID(), strings.Repeat("b", 64), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	path := "/member/v1/enrollments/" + created.Enrollment.ID
	if w := call(0, "GET", path+"/agent-credentials", tokens[0], ""); w.Code != 401 {
		t.Fatal("member token read attach credential", w.Code)
	}
	if w := call(0, "POST", path+"/agent-rotation", tokens[0], `{"request_id":"`+strings.Repeat("c", 64)+`"}`); w.Code != 401 {
		t.Fatal("member token performed agent recovery", w.Code)
	}
	if w := call(0, "GET", path+"/agent-credentials", created.Token, ""); w.Code != 200 || !strings.Contains(w.Body.String(), created.Enrollment.BrokerSessionCredential) {
		t.Fatal("agent credential read unavailable", w.Code)
	}
	if w := call(0, "GET", path+"/earnings", otherToken, ""); w.Code != 401 {
		t.Fatal("cross-member earnings exposed", w.Code)
	}
	if w := call(0, "GET", path+"/earnings", tokens[0], ""); w.Code != 200 {
		t.Fatal("own earnings denied", w.Code, w.Body.String())
	}
	if w := call(0, "POST", path+"/status", tokens[0], `{"revision":"fake","services":[]}`); w.Code != 401 {
		t.Fatal("member token impersonated agent stop", w.Code)
	}
	if w := call(0, "GET", "/admin/v1/payout-intents", tokens[0], ""); w.Code != 404 {
		t.Fatal("member listener exposed admin route", w.Code)
	}
	member, err := stores[0].GetPoolMember(wallet)
	if err != nil {
		t.Fatal(err)
	}
	member.Status = types.MemberStatusSuspended
	if err := stores[0].PutPoolMember(member); err != nil {
		t.Fatal(err)
	}
	if w := call(0, "POST", "/member/v1/join", tokens[0], `{"pool_id":"`+stores[0].PoolID()+`","terms_version":"eu-v1"}`); w.Code != 409 {
		t.Fatal("join lifted regional suspension", w.Code)
	}
	trust.Keys[0].Revoked = true
	raw, _ = json.Marshal(trust)
	if err := os.WriteFile(trustPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if w := call(1, "GET", "/member/v1/membership", tokens[1], ""); w.Code != 401 {
		t.Fatal("controller failed to reload issuer revocation", w.Code)
	}
}
