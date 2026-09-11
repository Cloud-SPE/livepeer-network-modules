package e2e

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

// member is a pool member with a wallet, signing in the way the portal
// makes them sign in.
type member struct {
	t       *testing.T
	pool    *pool
	key     *ecdsa.PrivateKey
	address string
	cookie  *http.Cookie
}

// signIn walks the real SIWE handshake: ask for a nonce, sign it, trade
// the signature for a session.
func signIn(t *testing.T, p *pool) *member {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	m := &member{t: t, pool: p, key: key, address: crypto.PubkeyToAddress(key.PublicKey).Hex()}

	status, raw := p.do(http.MethodPost, p.controlURL+"/member/v1/auth/nonce", "",
		`{"member_eth_address":"`+m.address+`"}`)
	if status != http.StatusOK {
		t.Fatalf("nonce: %d %s", status, raw)
	}
	var nonce struct {
		NonceID string `json:"nonce_id"`
		Message string `json:"message"`
	}
	decode(t, raw, &nonce)

	sig, err := crypto.Sign(accounts.TextHash([]byte(nonce.Message)), key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig[64] += 27
	body, _ := json.Marshal(map[string]any{
		"nonce_id":  nonce.NonceID,
		"signature": "0x" + hex.EncodeToString(sig),
		"contact":   "ops@example.com",
	})
	req, _ := http.NewRequest(http.MethodPost, p.controlURL+"/member/v1/auth/verify", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify status %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if strings.Contains(c.Name, "session") {
			m.cookie = c
		}
	}
	if m.cookie == nil {
		t.Fatal("sign-in returned no session cookie")
	}
	return m
}

// host is one enrolled machine.
//
// The two secrets are deliberately separate fields rather than one
// "token": the agent authenticates to the CONTROLLER with Token and to
// the BROKER with AttachCredential, and they are different values. A
// harness that conflated them would pass while a real bundle failed.
type host struct {
	ID    string
	Token string
	// AttachCredential is what the bundle writes as
	// LIVEPEER_ATTACH_CREDENTIAL; it is the secret the broker holds the
	// hash of.
	AttachCredential string
}

// enrol registers a host and returns what the bundle would carry.
func (m *member) enrol(label string) host {
	m.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, m.pool.controlURL+"/member/v1/enrollments",
		strings.NewReader(`{"host_label":"`+label+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(m.cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.t.Fatalf("enrol: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Enrollment struct {
			ID                      string `json:"id"`
			BrokerSessionCredential string `json:"broker_session_credential"`
		} `json:"enrollment"`
		Token string `json:"token"`
	}
	raw := readAll(m.t, resp)
	if resp.StatusCode != http.StatusOK {
		m.t.Fatalf("enrol status %d: %s", resp.StatusCode, raw)
	}
	decode(m.t, raw, &out)
	if out.Enrollment.ID == "" || out.Token == "" {
		m.t.Fatalf("enrolment came back without an id or token: %s", raw)
	}
	if out.Enrollment.BrokerSessionCredential == "" {
		m.t.Fatalf("enrolment carries no broker credential — the host has nothing to attach with: %s", raw)
	}
	return host{
		ID:               out.Enrollment.ID,
		Token:            out.Token,
		AttachCredential: out.Enrollment.BrokerSessionCredential,
	}
}

// reportHardware is what the agent does on first boot.
func (h host) reportHardware(t *testing.T, p *pool, gpuUUID, gpuModel string) {
	t.Helper()
	body := `{"hardware_units":[{"gpu_uuid":"` + gpuUUID + `","gpu_model":"` + gpuModel +
		`","vram_bytes":25769803776}]}`
	status, raw := p.do(http.MethodPost,
		p.controlURL+"/member/v1/enrollments/"+h.ID+"/hardware", h.Token, body)
	if status != http.StatusOK {
		t.Fatalf("report hardware: %d %s", status, raw)
	}
}

// desiredState fetches what the pool wants this host running.
func (h host) desiredState(t *testing.T, p *pool) desiredDoc {
	t.Helper()
	status, raw := p.do(http.MethodGet,
		p.controlURL+"/member/v1/enrollments/"+h.ID+"/desired-state", h.Token, "")
	if status != http.StatusOK {
		t.Fatalf("desired state: %d %s", status, raw)
	}
	var doc desiredDoc
	decode(t, raw, &doc)
	return doc
}

// desiredDoc mirrors the agent's view of the document. It is redeclared
// here rather than imported so a field the controller renames is caught
// as a wire change, which is what it is.
type desiredDoc struct {
	EnrollmentID string           `json:"enrollment_id"`
	Revision     string           `json:"revision"`
	Services     []desiredService `json:"services"`
}

type desiredService struct {
	Name            string            `json:"name"`
	ComposeFragment string            `json:"compose_fragment"`
	DeviceIDs       []string          `json:"device_ids"`
	Draining        bool              `json:"draining"`
	TemplateID      string            `json:"template_id"`
	AssignmentID    string            `json:"assignment_id"`
	Capability      string            `json:"capability"`
	Protocol        string            `json:"protocol"`
	Identity        map[string]string `json:"identity"`
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return raw
}
