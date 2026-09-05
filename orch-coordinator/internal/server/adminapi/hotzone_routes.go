package adminapi

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/providers/brokeradmin"
)

// The hot-zone console (plan 0043 §3.6).
//
// Everything an operator does about runners now lives here, over the
// broker admin API: which hosts are attached and what they declared,
// which offers are frozen and against what shape, the accept-shape
// gesture that supersedes one, host enrollment, and certification
// results. This is the *hot* zone deliberately — it changes what a
// runner may serve. It cannot change what is sold: prices come from
// offers, and the manifest is still only ever changed by the cold key.
//
// Pages aggregate every configured broker, because a pool can run more
// than one and an operator asking "why is this member not earning?"
// should not have to know which broker it attached to.

// hotzoneDeps is what the four pages need.
type hotzoneDeps struct {
	Admin   brokeradmin.Client
	Brokers []brokerTarget
	Timeout time.Duration
}

type brokerTarget struct {
	Name          string
	BaseURL       string
	Administrable bool
}

// brokerError records a broker that could not be reached or is not
// administrable, so a partial view says which part is missing rather
// than silently showing less.
type brokerError struct {
	Broker  string
	Message string
}

type hotzonePage struct {
	pageHeader
	Brokers      []brokerTarget
	BrokerErrors []brokerError
	Flash        *actionFlash
}

// --- Runners ---------------------------------------------------------------

type runnerRow struct {
	Broker string
	brokeradmin.Runner
}

type runnersPage struct {
	hotzonePage
	Runners []runnerRow
	Filter  string
}

// --- Offers ----------------------------------------------------------------

type offerRow struct {
	Broker string
	brokeradmin.Offer
}

type offersPage struct {
	hotzonePage
	Offers []offerRow
}

// --- Enroll ----------------------------------------------------------------

type enrollPage struct {
	hotzonePage
	Credentials []credentialRow
	// Issued is set immediately after an enrollment: the token appears
	// here and nowhere else, ever.
	Issued *brokeradmin.Enrollment
	// IssuedBroker names which broker minted it.
	IssuedBroker string
}

type credentialRow struct {
	Broker string
	brokeradmin.Credential
}

// --- Certification ---------------------------------------------------------

type certificationPage struct {
	hotzonePage
	Runs []certificationRow
}

type certificationRow struct {
	Broker string
	brokeradmin.CertificationRun
}

// registerHotzoneRoutes wires the four pages and their gestures.
func (s *Server) registerHotzoneRoutes(pages map[string]*template.Template, deps WebDeps, hz hotzoneDeps) {
	s.mux.HandleFunc("GET /runners", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, pages["runners"], buildRunnersPage(r, deps, hz))
	}))
	s.mux.HandleFunc("GET /offers", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, pages["offers"], buildOffersPage(r, deps, hz))
	}))
	s.mux.HandleFunc("GET /enroll", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, pages["enroll"], buildEnrollPage(r, deps, hz, nil, ""))
	}))
	s.mux.HandleFunc("GET /certification", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, pages["certification"], buildCertificationPage(r, deps, hz))
	}))

	// Gestures. Each redirects with a flash so a refresh never repeats a
	// write.
	s.mux.HandleFunc("POST /offers/accept-shape", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), hz.Timeout)
		defer cancel()
		broker := r.FormValue("broker")
		offering := r.FormValue("offering_id")
		hash := r.FormValue("shape_hash")
		err := hz.Admin.AcceptShape(ctx, broker, offering, hash)
		if err != nil {
			redirectFlash(w, r, "/offers", "error", fmt.Sprintf("accept-shape %s on %s: %v", offering, broker, err))
			return
		}
		// The signature is the acceptance: the broker now advertises the
		// new shape, and the operator still has to sign the candidate.
		redirectFlash(w, r, "/offers", "success", fmt.Sprintf(
			"%s on %s now advertises %s. Review the candidate diff and sign to publish it.", offering, broker, shortHash(hash)))
	}))
	s.mux.HandleFunc("POST /certification/run", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), hz.Timeout)
		defer cancel()
		broker, host, offering := r.FormValue("broker"), r.FormValue("host_id"), r.FormValue("offering_id")
		runID, err := hz.Admin.RunCertification(ctx, broker, host, offering, r.FormValue("local_id"))
		if err != nil {
			redirectFlash(w, r, "/certification", "error", fmt.Sprintf("certify %s × %s: %v", host, offering, err))
			return
		}
		redirectFlash(w, r, "/certification", "success", fmt.Sprintf("started %s for %s × %s", runID, host, offering))
	}))
	s.mux.HandleFunc("POST /runners/disconnect", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), hz.Timeout)
		defer cancel()
		broker, host := r.FormValue("broker"), r.FormValue("host_id")
		if err := hz.Admin.Disconnect(ctx, broker, host); err != nil {
			redirectFlash(w, r, "/runners", "error", fmt.Sprintf("disconnect %s: %v", host, err))
			return
		}
		redirectFlash(w, r, "/runners", "success", fmt.Sprintf(
			"closed %s's connections. It may reconnect — revoke its credential to stop that.", host))
	}))
	s.mux.HandleFunc("POST /enroll", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), hz.Timeout)
		defer cancel()
		broker := r.FormValue("broker")
		issued, err := hz.Admin.Enroll(ctx, broker, strings.TrimSpace(r.FormValue("host_id")), strings.TrimSpace(r.FormValue("label")))
		if err != nil {
			redirectFlash(w, r, "/enroll", "error", fmt.Sprintf("enroll on %s: %v", broker, err))
			return
		}
		// The credential is rendered inline rather than redirected: it is
		// shown exactly once and a redirect would put it in a URL.
		renderPage(w, pages["enroll"], buildEnrollPage(r, deps, hz, issued, broker))
	}))
	s.mux.HandleFunc("POST /enroll/revoke", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), hz.Timeout)
		defer cancel()
		broker, credID := r.FormValue("broker"), r.FormValue("credential_id")
		reason := strings.TrimSpace(r.FormValue("reason"))
		if reason == "" {
			reason = "revoked from the coordinator console"
		}
		if err := hz.Admin.Revoke(ctx, broker, credID, reason); err != nil {
			redirectFlash(w, r, "/enroll", "error", fmt.Sprintf("revoke %s: %v", credID, err))
			return
		}
		redirectFlash(w, r, "/enroll", "success", fmt.Sprintf(
			"revoked %s and closed its connections. It can never re-attach.", credID))
	}))
}

func (h hotzoneDeps) base(r *http.Request, deps WebDeps, title, active string) hotzonePage {
	return hotzonePage{
		pageHeader: pageHeader{
			Title: title, ActivePage: active,
			OrchEthAddress: deps.OrchEthAddress, Version: deps.Version, Actor: actorFromRequest(r),
		},
		Brokers: h.Brokers,
		Flash:   readFlash(r),
	}
}

// eachBroker runs fn against every administrable broker, collecting
// per-broker failures instead of failing the page: one unreachable
// broker must not hide the others.
func (h hotzoneDeps) eachBroker(ctx context.Context, fn func(ctx context.Context, name string) error) []brokerError {
	var errs []brokerError
	for _, b := range h.Brokers {
		if !b.Administrable {
			errs = append(errs, brokerError{Broker: b.Name,
				Message: "no admin_token_ref configured — set one in coordinator-config to manage this broker here"})
			continue
		}
		if err := fn(ctx, b.Name); err != nil {
			errs = append(errs, brokerError{Broker: b.Name, Message: describeAdminErr(err)})
		}
	}
	return errs
}

func describeAdminErr(err error) string {
	switch {
	case errors.Is(err, brokeradmin.ErrNoToken):
		return "no admin token configured"
	case errors.Is(err, brokeradmin.ErrUnauthorized):
		return "admin token rejected — check admin_token_ref against the broker's admin_auth"
	case errors.Is(err, brokeradmin.ErrUnavailable):
		return "unreachable: " + err.Error()
	default:
		return err.Error()
	}
}

func buildRunnersPage(r *http.Request, deps WebDeps, hz hotzoneDeps) runnersPage {
	ctx, cancel := context.WithTimeout(r.Context(), hz.Timeout)
	defer cancel()
	page := runnersPage{hotzonePage: hz.base(r, deps, "Runners", "runners")}
	page.Filter = strings.TrimSpace(r.URL.Query().Get("q"))
	page.BrokerErrors = hz.eachBroker(ctx, func(ctx context.Context, name string) error {
		runners, err := hz.Admin.Runners(ctx, name)
		if err != nil {
			return err
		}
		for _, run := range runners {
			if page.Filter != "" && !runnerMatches(run, page.Filter) {
				continue
			}
			page.Runners = append(page.Runners, runnerRow{Broker: name, Runner: run})
		}
		return nil
	})
	sort.Slice(page.Runners, func(i, j int) bool {
		if page.Runners[i].Broker != page.Runners[j].Broker {
			return page.Runners[i].Broker < page.Runners[j].Broker
		}
		return page.Runners[i].HostID < page.Runners[j].HostID
	})
	return page
}

func runnerMatches(run brokeradmin.Runner, q string) bool {
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(run.HostID), q) ||
		strings.Contains(strings.ToLower(run.Enrollment.Label), q) ||
		strings.Contains(strings.ToLower(run.Enrollment.MemberEthAddress), q) {
		return true
	}
	for _, c := range run.Capabilities {
		if strings.Contains(strings.ToLower(c.CapabilityID), q) || strings.Contains(strings.ToLower(c.LocalID), q) {
			return true
		}
	}
	return false
}

func buildOffersPage(r *http.Request, deps WebDeps, hz hotzoneDeps) offersPage {
	ctx, cancel := context.WithTimeout(r.Context(), hz.Timeout)
	defer cancel()
	page := offersPage{hotzonePage: hz.base(r, deps, "Offers", "offers")}
	page.BrokerErrors = hz.eachBroker(ctx, func(ctx context.Context, name string) error {
		offers, err := hz.Admin.Offers(ctx, name)
		if err != nil {
			return err
		}
		for _, o := range offers {
			page.Offers = append(page.Offers, offerRow{Broker: name, Offer: o})
		}
		return nil
	})
	sort.Slice(page.Offers, func(i, j int) bool {
		if page.Offers[i].Broker != page.Offers[j].Broker {
			return page.Offers[i].Broker < page.Offers[j].Broker
		}
		return page.Offers[i].OfferingID < page.Offers[j].OfferingID
	})
	return page
}

func buildEnrollPage(r *http.Request, deps WebDeps, hz hotzoneDeps, issued *brokeradmin.Enrollment, issuedBroker string) enrollPage {
	ctx, cancel := context.WithTimeout(r.Context(), hz.Timeout)
	defer cancel()
	page := enrollPage{hotzonePage: hz.base(r, deps, "Enroll host", "enroll"), Issued: issued, IssuedBroker: issuedBroker}
	page.BrokerErrors = hz.eachBroker(ctx, func(ctx context.Context, name string) error {
		creds, err := hz.Admin.Credentials(ctx, name)
		if err != nil {
			return err
		}
		for _, c := range creds {
			page.Credentials = append(page.Credentials, credentialRow{Broker: name, Credential: c})
		}
		return nil
	})
	sort.Slice(page.Credentials, func(i, j int) bool {
		if page.Credentials[i].Broker != page.Credentials[j].Broker {
			return page.Credentials[i].Broker < page.Credentials[j].Broker
		}
		return page.Credentials[i].HostID < page.Credentials[j].HostID
	})
	return page
}

func buildCertificationPage(r *http.Request, deps WebDeps, hz hotzoneDeps) certificationPage {
	ctx, cancel := context.WithTimeout(r.Context(), hz.Timeout)
	defer cancel()
	page := certificationPage{hotzonePage: hz.base(r, deps, "Certification", "certification")}
	page.BrokerErrors = hz.eachBroker(ctx, func(ctx context.Context, name string) error {
		runs, err := hz.Admin.Certification(ctx, name)
		if err != nil {
			return err
		}
		for _, run := range runs {
			page.Runs = append(page.Runs, certificationRow{Broker: name, CertificationRun: run})
		}
		return nil
	})
	sort.Slice(page.Runs, func(i, j int) bool {
		return page.Runs[i].StartedAt.After(page.Runs[j].StartedAt)
	})
	return page
}

// shortHash trims a shape hash for display without losing its identity
// at a glance.
func shortHash(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func redirectFlash(w http.ResponseWriter, r *http.Request, path, outcome, message string) {
	q := url.Values{}
	q.Set("outcome", outcome)
	q.Set("message", message)
	http.Redirect(w, r, path+"?"+q.Encode(), http.StatusSeeOther)
}

func readFlash(r *http.Request) *actionFlash {
	outcome := r.URL.Query().Get("outcome")
	if outcome == "" {
		return nil
	}
	return &actionFlash{Outcome: outcome, Message: r.URL.Query().Get("message")}
}
