package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/credentialstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workerconn"
	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
)

// Runner attach (protocols/runner-attach.md) over both tunnel transports.
//
// The first frame on a connection must be a register carrying the attach
// document; the broker answers with register_result; the runner may
// re-send on change; the document is gone on disconnect. Dispatch over
// attached runners, matching, freeze, and certification are plan 0043
// item 8 — this file admits runners and makes them visible.

// remoteProbeTypes are the readiness probe kinds a runner may declare
// (runner-attach §3.2): the broker-local kinds are excluded.
var remoteProbeTypes = map[string]bool{
	"http-status": true, "http-jsonpath": true, "http-openai-model-ready": true, "tcp-connect": true,
}

var attachConnSeq uint64

func (s *Server) attachKnown() runnerattach.Known {
	return runnerattach.Known{
		Extractor:  s.extractors.Has,
		ProbeTypes: remoteProbeTypes,
		Protocols:  map[string]bool{"paid-job/v1": true, "paid-session/v1": true},
		Credential: func(kind, token string) (string, bool, bool) {
			if kind != credentialstore.KindBearer {
				return "", false, false
			}
			if s.credentialStore == nil {
				return "", false, true
			}
			rec, err := s.credentialStore.Authenticate(token)
			if err != nil {
				return "", false, true
			}
			return rec.HostID, true, true
		},
	}
}

// evaluateAttach runs the document through the validator and resolves
// the enrollment for an accepted one.
func (s *Server) evaluateAttach(raw []byte) (*runnerattach.Document, *runnerattach.Result, runners.Enrollment) {
	doc, res := runnerattach.Evaluate(raw, s.attachKnown())
	var enr runners.Enrollment
	if doc != nil && s.credentialStore != nil {
		if rec, err := s.credentialStore.ByHost(doc.HostID); err == nil {
			enr = runners.Enrollment{CredentialID: rec.CredentialID, Label: rec.Label, MemberEthAddress: rec.MemberEthAddress}
		}
	}
	return doc, res, enr
}

func resultFrame(id string, res *runnerattach.Result) workerconn.TunnelMessage {
	body, _ := json.Marshal(res)
	return workerconn.TunnelMessage{Type: workerconn.MessageTypeRegisterResult, ID: id, Body: body}
}

// --- WebSocket -------------------------------------------------------------

func (s *Server) handleAttachWS(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	first, err := workerconn.ReadRegister(conn, 15*time.Second)
	if err != nil {
		// Anything but a register first closes the connection (§2).
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "register first"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	if !workerconn.IsAttachRegister(first) {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "register must carry an attach document"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	fwd := workerconn.NewSessionForwarderDeferred(conn)
	connID := "ws-" + strconv.FormatUint(atomic.AddUint64(&attachConnSeq, 1), 10)
	hostID := ""
	rejections := 0
	// Register before acknowledging. `accepted` is the broker telling a
	// runner it is attached, so it has to be true by the time the frame
	// goes out: between the old send-then-attach order the host was
	// absent from /admin/v1/runners and invisible to ConnFor, so a
	// dispatch in that window found no runner to send to.
	handle := func(msg workerconn.TunnelMessage) bool {
		doc, res, enr := s.evaluateAttach(msg.Body)
		accepted := doc != nil && (hostID == "" || hostID == doc.HostID)
		if accepted {
			hostID = doc.HostID
			s.runners.Attach(connID, fwd, enr, doc, res)
		}
		_ = fwd.SendMessage(resultFrame(msg.ID, res))
		if !accepted {
			if doc == nil {
				rejections++
			}
			// A connection serves exactly one host (§2).
			return false
		}
		// After the acknowledgement: matching can dispatch to this
		// runner, and it should not arrive before the runner has been
		// told it is attached.
		s.onRunnerAttached()
		return true
	}
	if !handle(first) {
		_ = conn.Close()
		return
	}
	untrack := s.trackAttachedHostID(hostID, fwd)
	fwd.SetRegisterHandler(func(msg workerconn.TunnelMessage) {
		if !workerconn.IsAttachRegister(msg) || !handle(msg) {
			if rejections >= 3 {
				_ = fwd.Close()
			}
		}
	})
	fwd.Start()
	defer func() {
		untrack()
		s.runners.Detach(hostID, connID)
		_ = fwd.Close()
	}()
	select {
	case <-r.Context().Done():
	case <-fwd.Done():
	}
}

// --- QUIC ------------------------------------------------------------------

func (s *Server) handleAttachQUIC(ctx context.Context, conn *quic.Conn, first workerconn.TunnelMessage, stream *quic.Stream) {
	fwd := workerconn.NewQUICSessionForwarder(conn)
	connID := "quic-" + strconv.FormatUint(atomic.AddUint64(&attachConnSeq, 1), 10)
	hostID := ""
	// Same order as the WebSocket path, for the same reason: the
	// acknowledgement must not outrun the registration it reports.
	reply := func(st *quic.Stream, msg workerconn.TunnelMessage) bool {
		doc, res, enr := s.evaluateAttach(msg.Body)
		accepted := doc != nil && (hostID == "" || hostID == doc.HostID)
		if accepted {
			hostID = doc.HostID
			s.runners.Attach(connID, fwd, enr, doc, res)
		}
		_ = json.NewEncoder(st).Encode(resultFrame(msg.ID, res))
		_ = st.Close()
		if !accepted {
			return false
		}
		s.onRunnerAttached()
		return true
	}
	if !reply(stream, first) {
		_ = conn.CloseWithError(1, "attach rejected")
		return
	}
	untrack := s.trackAttachedHostID(hostID, fwd)
	defer func() {
		untrack()
		s.runners.Detach(hostID, connID)
		_ = fwd.Close()
	}()
	// Re-sent documents arrive on new runner-opened streams. Requests the
	// broker dispatches use broker-opened streams, so every accepted
	// stream here is a register by contract.
	go func() {
		for {
			msg, st, err := workerconn.AcceptQUICRegister(ctx, conn)
			if err != nil {
				return
			}
			if !workerconn.IsAttachRegister(msg) {
				_ = st.Close()
				continue
			}
			reply(st, msg)
		}
	}()
	select {
	case <-ctx.Done():
	case <-fwd.Done():
	}
}

// trackAttachedHostID is trackAttachedHost keyed directly by host id
// (the document already authenticated).
func (s *Server) trackAttachedHostID(hostID string, conn interface{ Close() error }) func() {
	s.attachedMu.Lock()
	s.attachedHosts[hostID] = append(s.attachedHosts[hostID], conn)
	s.attachedMu.Unlock()
	return func() {
		s.attachedMu.Lock()
		defer s.attachedMu.Unlock()
		conns := s.attachedHosts[hostID]
		for i, c := range conns {
			if c == conn {
				s.attachedHosts[hostID] = append(conns[:i], conns[i+1:]...)
				break
			}
		}
		if len(s.attachedHosts[hostID]) == 0 {
			delete(s.attachedHosts, hostID)
		}
	}
}

// --- admin: runners (broker-admin §3) --------------------------------------

type runnerView struct {
	HostID          string                  `json:"host_id"`
	Enrollment      runners.Enrollment      `json:"enrollment"`
	State           string                  `json:"state"`
	ConnectedSince  time.Time               `json:"connected_since"`
	LastSeen        time.Time               `json:"last_seen"`
	Connections     int                     `json:"connections"`
	AgentVersion    string                  `json:"agent_version,omitempty"`
	ContractVersion string                  `json:"contract_version,omitempty"`
	PublicURL       string                  `json:"public_url,omitempty"`
	Hardware        []runnerattach.Hardware `json:"hardware"`
	Capabilities    []runnerCapabilityView  `json:"capabilities"`
	Extensions      map[string]any          `json:"extensions,omitempty"`
}

type runnerCapabilityView struct {
	LocalID      string                     `json:"local_id"`
	CapabilityID string                     `json:"capability_id"`
	Protocol     string                     `json:"protocol,omitempty"`
	Attach       runnerAttachView           `json:"attach"`
	Declared     map[string]any             `json:"declared,omitempty"`
	Offers       []offers.PairView          `json:"offers"`
	Extensions   map[string]json.RawMessage `json:"extensions,omitempty"`
}

type runnerAttachView struct {
	Status   string                `json:"status"`
	Reasons  []runnerattach.Reason `json:"reasons,omitempty"`
	Warnings []runnerattach.Reason `json:"warnings,omitempty"`
}

func (s *Server) runnerViewOf(sn runners.Snapshot, includePaths bool) runnerView {
	v := runnerView{
		HostID: sn.HostID, Enrollment: sn.Enrollment, State: sn.State, ConnectedSince: sn.Since,
		LastSeen: sn.LastSeen, Connections: sn.Connections, AgentVersion: sn.AgentVersion,
		ContractVersion: sn.ContractVersion, PublicURL: sn.PublicURL, Hardware: sn.Hardware, Capabilities: []runnerCapabilityView{},
		Extensions: sn.Extensions,
	}
	if v.Hardware == nil {
		v.Hardware = []runnerattach.Hardware{}
	}
	for _, cv := range sn.Capabilities {
		rc := runnerCapabilityView{
			LocalID: cv.Result.LocalID, CapabilityID: cv.Result.CapabilityID,
			Attach: runnerAttachView{Status: cv.Result.Status, Reasons: cv.Result.Reasons, Warnings: cv.Result.Warnings},
			Offers: []offers.PairView{},
		}
		if s.offersEngine != nil && cv.Capability != nil {
			if pv := s.offersEngine.PairsFor(sn.HostID, cv.Result.LocalID); pv != nil {
				rc.Offers = pv
			}
		}
		if c := cv.Capability; c != nil {
			rc.Protocol = c.Protocol
			rc.Extensions = c.Extensions
			rc.Declared = map[string]any{
				"work_unit": c.WorkUnit, "identity": c.Identity, "schema_versions": c.SchemaVersions,
			}
			if c.IsJob() {
				rc.Declared["transports"] = c.Transports
			} else {
				rc.Declared["descriptor_schemas"] = c.DescriptorSchemas
				rc.Declared["metering"] = c.Metering
				if c.Heartbeat != nil {
					rc.Declared["heartbeat"] = c.Heartbeat
				}
			}
			if c.Requirements != nil {
				rc.Declared["requirements"] = c.Requirements
			}
			if len(c.Devices) > 0 {
				rc.Declared["devices"] = c.Devices
			}
			if includePaths {
				rc.Declared["paths"] = c.Paths
				rc.Declared["readiness"] = c.Readiness
			} else {
				rc.Declared["readiness"] = map[string]any{"type": c.Readiness.Type}
			}
		}
		v.Capabilities = append(v.Capabilities, rc)
	}
	return v
}

func (s *Server) handleRunnersList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	q := r.URL.Query()
	includePaths := strings.Contains(q.Get("include"), "paths")
	stateF, hostF, capF := q.Get("state"), q.Get("host_id"), q.Get("capability_id")
	views := []runnerView{}
	for _, sn := range s.runners.List() {
		if stateF != "" && sn.State != stateF || hostF != "" && sn.HostID != hostF {
			continue
		}
		if capF != "" {
			found := false
			for _, c := range sn.Capabilities {
				if c.Result.CapabilityID == capF {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		views = append(views, s.runnerViewOf(sn, includePaths))
	}
	adminJSON(w, http.StatusOK, map[string]any{"runners": views, "next_cursor": nil})
}

func (s *Server) handleRunnerGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	sn, ok := s.runners.Get(r.PathValue("host_id"))
	if !ok {
		adminError(w, http.StatusNotFound, "runner_not_found", "no such runner within retention")
		return
	}
	adminJSON(w, http.StatusOK, s.runnerViewOf(sn, strings.Contains(r.URL.Query().Get("include"), "paths")))
}

func (s *Server) handleRunnerDisconnect(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	hostID := r.PathValue("host_id")
	if _, ok := s.runners.Get(hostID); !ok {
		adminError(w, http.StatusNotFound, "runner_not_found", "no such runner within retention")
		return
	}
	n := s.runners.Disconnect(hostID)
	adminJSON(w, http.StatusOK, map[string]any{"host_id": hostID, "connections_closed": n})
}

// runRunnerEviction drops disconnected hosts past retention.
func (s *Server) runRunnerEviction(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n := s.runners.Evict(); n > 0 {
				log.Printf("runners: evicted %d disconnected host(s) past retention", n)
			}
		}
	}
}
