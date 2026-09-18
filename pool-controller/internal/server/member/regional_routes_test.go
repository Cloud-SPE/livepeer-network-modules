package member

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestRegionalJoinRequiresOwnSessionPoolAndOrigin(t *testing.T) {
	r, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	terms := types.RegionalTerms{PoolID: r.PoolID(), Version: "v1", WindowRounds: 14, ParticipationRules: "test", ZeroWorkToOperator: true, RoundingToOperator: true}
	if err := r.PutRegionalTerms(terms); err != nil {
		t.Fatal(err)
	}
	wallet := "0x0000000000000000000000000000000000000011"
	if err := r.PutPoolMember(types.PoolMember{ID: wallet, EthAddress: wallet, Status: types.MemberStatusActive}); err != nil {
		t.Fatal(err)
	}
	sessions := NewSessionAuth()
	session, err := sessions.Create(wallet)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerRegionalRoutes(mux, Deps{Repo: r, Sessions: sessions})
	for _, tc := range []struct {
		name, origin, pool, session string
		code                        int
	}{
		{"valid", "https://members.example", r.PoolID(), session, 200},
		{"cross region", "https://members.example", "pool_other", session, 409},
		{"anonymous", "https://members.example", r.PoolID(), "", 401},
		{"csrf", "https://attacker.example", r.PoolID(), session, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "https://members.example/member/v1/terms/accept", strings.NewReader(`{"pool_id":"`+tc.pool+`","terms_version":"v1"}`))
			req.Header.Set("Origin", tc.origin)
			req.AddCookie(&http.Cookie{Name: memberSessionCookieName, Value: tc.session})
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tc.code {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
		})
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/member/v1/pool", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), r.PoolID()) || strings.Contains(w.Body.String(), wallet) {
		t.Fatalf("public identity: %s", w.Body.String())
	}
}
