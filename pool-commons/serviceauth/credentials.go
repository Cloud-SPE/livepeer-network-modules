// Package serviceauth implements pool/role/resource-scoped service credentials.
// The verifier reloads an operator-owned file for each request so an atomic
// replacement immediately rotates or revokes credentials on that instance.
package serviceauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const PoolHeader = "X-Livepeer-Pool-ID"

var ErrUnauthorized = errors.New("service credential not authorized")

type Credential struct {
	SourceID    string    `json:"source_id,omitempty"`
	ID          string    `json:"id"`
	TokenSHA256 string    `json:"token_sha256"`
	PoolID      string    `json:"pool_id"`
	Roles       []string  `json:"roles"`
	Resources   []string  `json:"resources"`
	ExpiresAt   time.Time `json:"expires_at"`
	Revoked     bool      `json:"revoked,omitempty"`
}

type File struct {
	Credentials []Credential `json:"credentials"`
}

type Verifier struct {
	Path string
	Now  func() time.Time
}

func GenerateToken() (string, string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", "", err
	}
	secret := hex.EncodeToString(token[:])
	return secret, HashToken(secret), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (v Verifier) load() (File, error) {
	var file File
	f, err := os.Open(v.Path)
	if err != nil {
		return file, ErrUnauthorized
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return file, ErrUnauthorized
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return file, ErrUnauthorized
	}
	ids, hashes := map[string]bool{}, map[string]bool{}
	for _, c := range file.Credentials {
		hash, err := hex.DecodeString(c.TokenSHA256)
		if err != nil || len(hash) != sha256.Size || c.ID == "" || c.PoolID == "" || len(c.Roles) == 0 || len(c.Resources) == 0 || c.ExpiresAt.IsZero() || ids[c.ID] || hashes[strings.ToLower(c.TokenSHA256)] {
			return File{}, ErrUnauthorized
		}
		ids[c.ID], hashes[strings.ToLower(c.TokenSHA256)] = true, true
	}
	return file, nil
}

// Authorize checks a fixed server-side pool/resource and explicit allowed roles.
// Request headers cannot select the server's pool or expand permissions.
func (v Verifier) Authorize(r *http.Request, poolID, resource string, roles ...string) (Credential, error) {
	if poolID == "" || resource == "" || len(roles) == 0 || r.Header.Get(PoolHeader) != poolID {
		return Credential{}, ErrUnauthorized
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return Credential{}, ErrUnauthorized
	}
	token := strings.TrimPrefix(header, "Bearer ")
	if len(token) != 64 {
		return Credential{}, ErrUnauthorized
	}
	file, err := v.load()
	if err != nil {
		return Credential{}, err
	}
	now := time.Now()
	if v.Now != nil {
		now = v.Now()
	}
	actual, _ := hex.DecodeString(HashToken(token))
	for _, c := range file.Credentials {
		expected, _ := hex.DecodeString(c.TokenSHA256)
		matches := subtle.ConstantTimeCompare(actual, expected) == 1
		if !matches || c.Revoked || c.PoolID != poolID || !now.Before(c.ExpiresAt) {
			continue
		}
		if !contains(c.Resources, resource) {
			return Credential{}, ErrUnauthorized
		}
		for _, role := range roles {
			if contains(c.Roles, role) {
				return c, nil
			}
		}
	}
	return Credential{}, ErrUnauthorized
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func (v Verifier) Wrap(poolID, resource string, roles []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := v.Authorize(r, poolID, resource, roles...); err != nil {
			http.Error(w, "unauthorized service", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
