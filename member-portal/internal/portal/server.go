package portal

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const cookieName = "member_portal_session"

type Server struct {
	cache   reportCache
	Store   *Store
	Origin  string
	Regions map[string]*RegionalClient
}

func (s *Server) Handler() (http.Handler, error) {
	u, err := parseOrigin(s.Origin)
	if err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, errors.New("portal requires persistent store")
	}
	if err := s.Store.BindOrigin(s.Origin); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	s.registerBootstrap(mux)
	s.registerPage(mux)
	mux.HandleFunc("GET /api/reports", s.reports)
	mux.HandleFunc("POST /api/auth/challenge", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Wallet string `json:"wallet"`
		}
		if !decodeRequest(w, r, &input) {
			return
		}
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		challenge, err := s.Store.Challenge(s.Origin, input.Wallet, ip, time.Now())
		if err != nil {
			http.Error(w, err.Error(), 429)
			return
		}
		respond(w, 200, challenge)
	})
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID        string `json:"id"`
			Signature string `json:"signature"`
		}
		if !decodeRequest(w, r, &input) {
			return
		}
		secret, session, err := s.Store.Login(input.ID, input.Signature, time.Now())
		if err != nil {
			http.Error(w, "wallet signature rejected", 401)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: secret, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: session.ExpiresAt, MaxAge: 86400})
		respond(w, 200, map[string]any{"wallet": session.Wallet, "expires_at": session.ExpiresAt})
	})
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err == nil {
			if err := s.Store.Logout(cookie.Value); err != nil {
				http.Error(w, "logout unavailable", 503)
				return
			}
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		session, err := s.session(r)
		if err != nil {
			http.Error(w, "sign in required", 401)
			return
		}
		regions := []Region{}
		for _, client := range s.Regions {
			regions = append(regions, client.Region)
		}
		respond(w, 200, map[string]any{"wallet": session.Wallet, "expires_at": session.ExpiresAt, "regions": regions})
	})
	mux.HandleFunc("/api/regions/{pool}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		session, err := s.session(r)
		if err != nil {
			http.Error(w, "sign in required", 401)
			return
		}
		client := s.Regions[r.PathValue("pool")]
		if client == nil {
			http.Error(w, "unknown region", 404)
			return
		}
		path := "/member/v1/" + r.PathValue("path")
		if r.URL.RawQuery != "" || r.URL.RawPath != "" || !allowedMemberPath(r.Method, path) {
			http.Error(w, "member operation forbidden", 403)
			return
		}
		response, err := client.Do(r.Context(), session, r.Method, path, http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			http.Error(w, "region unavailable; no result assumed", 502)
			return
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
		if err != nil || len(raw) > 4<<20 {
			http.Error(w, "regional response incomplete", 502)
			return
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 && r.Method == "POST" && (path == "/member/v1/enrollments" || strings.HasSuffix(path, "/rotate")) {
			prepared, err := s.prepareEnrollment(r.Context(), client, session, raw)
			if err != nil {
				http.Error(w, "Enrollment saved; bundle preparation failed. Rotate its credential to create a new install link.", 502)
				return
			}
			respond(w, 200, prepared)
			return
		}
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.Header().Set("X-Pool-ID", client.Region.PoolID)
		w.Header().Set("X-Observed-At", time.Now().UTC().Format(time.RFC3339Nano))
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(raw)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.Host != u.Host {
			http.Error(w, "unknown portal host", 400)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != s.Origin {
			http.Error(w, "same-origin request required", 403)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}
func (s *Server) session(r *http.Request) (Session, error) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return Session{}, errUnauthorized
	}
	return s.Store.Session(cookie.Value, time.Now())
}
func decodeRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid request", 400)
		return false
	}
	return true
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func parseOrigin(origin string) (*url.URL, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(origin, "\"'`$\\ \r\n") {
		return nil, errors.New("exact HTTPS portal origin required")
	}
	return u, nil
}
