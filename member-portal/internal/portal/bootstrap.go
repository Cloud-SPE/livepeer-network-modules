package portal

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Bootstrap struct {
	PoolID       string    `json:"pool_id"`
	EnrollmentID string    `json:"enrollment_id"`
	Wallet       string    `json:"wallet"`
	ExpiresAt    time.Time `json:"expires_at"`
	Bundle       []byte    `json:"bundle"`
}

func (s *Store) saveBootstrap(b Bootstrap) (string, error) {
	secret, err := randomSecret()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("bootstrap"))
		if bucket.Stats().KeyN >= 1024 {
			return errors.New("bootstrap capacity reached; retry after expired links are removed")
		}
		return bucket.Put([]byte(secretHash(secret)), raw)
	})
	return secret, err
}
func (s *Store) bootstrap(secret string, consume bool) (Bootstrap, error) {
	var out Bootstrap
	if len(secret) != 64 {
		return out, errUnauthorized
	}
	read := func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("bootstrap"))
		raw := b.Get([]byte(secretHash(secret)))
		if raw == nil {
			return errUnauthorized
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		if !time.Now().Before(out.ExpiresAt) {
			return errUnauthorized
		}
		if consume {
			return b.Delete([]byte(secretHash(secret)))
		}
		out.Bundle = nil
		return nil
	}
	var err error
	if consume {
		err = s.db.Update(read)
	} else {
		err = s.db.View(read)
	}
	return out, err
}

// FetchBundle uses only the configured controller and its constructed enrollment
// path. A controller response cannot redirect the enrollment credential elsewhere.
func (c *RegionalClient) FetchBundle(ctx context.Context, id, token string) ([]byte, error) {
	if !safeID.MatchString(id) || token == "" || len(token) > 8192 || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("invalid enrollment bundle authority")
	}
	target := *c.origin
	target.Path = "/member/v1/enrollments/" + id + "/bundle"
	target.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("regional bundle unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, errors.New("regional bundle incomplete")
	}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, errors.New("invalid regional bundle")
	}
	found := false
	var total uint64
	for _, file := range archive.File {
		if strings.ContainsAny(file.Name, "/\\") || file.Name == ".." || !file.Mode().IsRegular() {
			return nil, errors.New("unsafe regional bundle entry")
		}
		if file.UncompressedSize64 > (16<<20)-total {
			return nil, errors.New("regional bundle too large")
		}
		total += file.UncompressedSize64
		if file.Name == "docker-compose.yaml" {
			found = true
		}
	}
	if !found {
		return nil, errors.New("regional compose file missing")
	}
	return raw, nil
}
func (s *Server) prepareEnrollment(ctx context.Context, client *RegionalClient, session Session, raw []byte) (any, error) {
	var response struct {
		Enrollment struct {
			ID     string `json:"id"`
			PoolID string `json:"pool_id"`
			Member string `json:"member_eth_address"`
		} `json:"enrollment"`
		Token        string `json:"token"`
		RotatedToken string `json:"enrollment_token"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	e := response.Enrollment
	if !safeID.MatchString(e.ID) || e.PoolID != client.Region.PoolID || !strings.EqualFold(e.Member, session.Wallet) {
		return nil, errors.New("regional enrollment identity mismatch")
	}
	token := response.Token
	if token == "" {
		token = response.RotatedToken
	}
	bundle, err := client.FetchBundle(ctx, e.ID, token)
	if err != nil {
		return nil, err
	}
	expires := time.Now().UTC().Add(10 * time.Minute)
	secret, err := s.Store.saveBootstrap(Bootstrap{PoolID: e.PoolID, EnrollmentID: e.ID, Wallet: session.Wallet, ExpiresAt: expires, Bundle: bundle})
	if err != nil {
		return nil, err
	}
	return map[string]any{"pool_id": e.PoolID, "enrollment_id": e.ID, "expires_at": expires, "bootstrap_command": "curl -fsS '" + s.Origin + "/install/" + secret + "' | sh", "bundle_url": s.Origin + "/bootstrap/" + secret + "/bundle"}, nil
}
func (s *Server) registerBootstrap(mux *http.ServeMux) {
	mux.HandleFunc("GET /install/{secret}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		b, err := s.Store.bootstrap(r.PathValue("secret"), false)
		if err != nil {
			http.Error(w, "bootstrap link expired or consumed", 410)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintf(w, "#!/bin/sh\nset -eu\numask 077\ncommand -v curl >/dev/null\ncommand -v unzip >/dev/null\ndocker compose version >/dev/null\ninstall_dir=\"${HOME}/.local/share/livepeer-pool/%s\"\nmkdir -p \"$install_dir\"\ncd \"$install_dir\"\nbundle=$(mktemp)\ntrap 'rm -f \"$bundle\"' EXIT HUP INT TERM\ncurl -fsS '%s/bootstrap/%s/bundle' -o \"$bundle\"\nif [ -f docker-compose.yaml ]; then docker compose stop pool_member_agent; fi\nunzip -oq \"$bundle\"\nsh start.sh --force-recreate\nprintf 'Regional agent installed in %%s\\n' \"$install_dir\"\n", b.EnrollmentID, s.Origin, r.PathValue("secret"))
	})
	mux.HandleFunc("GET /bootstrap/{secret}/bundle", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		b, err := s.Store.bootstrap(r.PathValue("secret"), true)
		if err != nil {
			http.Error(w, "bootstrap link expired or consumed", 410)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="regional-enrollment.zip"`)
		_, _ = w.Write(b.Bundle)
	})
}
