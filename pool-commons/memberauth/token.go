// Package memberauth implements the fixed Ed25519 regional-member token format.
// It grants wallet identity within one pool, never operator or payout authority.
package memberauth

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

const MaxTTL = 2 * time.Minute
const Prefix = "lpm1."
const Scope = "regional-member"

var walletPattern = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
var sessionPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var poolPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Claims struct {
	Version   int    `json:"version"`
	Issuer    string `json:"issuer"`
	KeyID     string `json:"key_id"`
	PoolID    string `json:"pool_id"`
	Wallet    string `json:"wallet"`
	SessionID string `json:"session_id"`
	Scope     string `json:"scope"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
}
type Signer struct {
	Issuer, KeyID string
	PrivateKey    ed25519.PrivateKey
}
type PrivateKeyFile struct {
	Issuer     string `json:"issuer"`
	KeyID      string `json:"key_id"`
	PrivateKey string `json:"private_key"`
}
type PublicKey struct {
	ID        string    `json:"id"`
	PublicKey string    `json:"public_key"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	Revoked   bool      `json:"revoked,omitempty"`
}
type Trust struct {
	Issuer          string      `json:"issuer"`
	Keys            []PublicKey `json:"keys"`
	RevokedSessions []string    `json:"revoked_sessions,omitempty"`
}
type Verifier struct{ Path, PoolID string }

func LoadSigner(path string) (*Signer, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("member signer unavailable")
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("member signing key must be owner-readable only")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("member signer unavailable")
	}
	var file PrivateKeyFile
	if err := decode(raw, &file); err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(file.PrivateKey)
	if err != nil || len(key) != ed25519.PrivateKeySize || file.Issuer == "" || file.KeyID == "" {
		return nil, fmt.Errorf("invalid member signing key")
	}
	canonical := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
	if !bytes.Equal(key, canonical) {
		return nil, fmt.Errorf("invalid member signing key")
	}
	return &Signer{Issuer: file.Issuer, KeyID: file.KeyID, PrivateKey: key}, nil
}
func (s *Signer) Sign(wallet, pool, session string, now time.Time, ttl time.Duration) (string, error) {
	if s == nil || len(s.PrivateKey) != ed25519.PrivateKeySize || ttl <= 0 || ttl > MaxTTL {
		return "", fmt.Errorf("invalid member signer or token lifetime")
	}
	claims := Claims{Version: 1, Issuer: s.Issuer, KeyID: s.KeyID, PoolID: pool, Wallet: strings.ToLower(wallet), SessionID: session, Scope: Scope, IssuedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix()}
	if err := claims.validate(pool, now); err != nil {
		return "", err
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payload := Prefix + base64.RawURLEncoding.EncodeToString(raw)
	signature := ed25519.Sign(s.PrivateKey, []byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
func (v Verifier) Verify(token string, now time.Time) (Claims, error) {
	var claims Claims
	if v.Path == "" || v.PoolID == "" || len(token) > 8192 || !strings.HasPrefix(token, Prefix) {
		return claims, errors.New("member token unavailable or malformed")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims, errors.New("malformed member token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, errors.New("malformed member token")
	}
	if err := decode(raw, &claims); err != nil {
		return Claims{}, err
	}
	if err := claims.validate(v.PoolID, now); err != nil {
		return Claims{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Claims{}, errors.New("malformed member signature")
	}
	file, err := os.Open(v.Path)
	if err != nil {
		return Claims{}, errors.New("member issuer trust unavailable")
	}
	defer file.Close()
	trustRaw, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(trustRaw) > 1<<20 {
		return Claims{}, errors.New("invalid member issuer trust")
	}
	var trust Trust
	if err := decode(trustRaw, &trust); err != nil {
		return Claims{}, err
	}
	if trust.Issuer != claims.Issuer || len(trust.Keys) == 0 {
		return Claims{}, errors.New("untrusted member issuer")
	}
	for _, session := range trust.RevokedSessions {
		if session == claims.SessionID {
			return Claims{}, errors.New("member session revoked")
		}
	}
	seen := map[string]bool{}
	var accepted *PublicKey
	var public ed25519.PublicKey
	for i := range trust.Keys {
		key := &trust.Keys[i]
		decoded, err := base64.StdEncoding.DecodeString(key.PublicKey)
		if err != nil || len(decoded) != ed25519.PublicKeySize || key.ID == "" || seen[key.ID] || key.NotBefore.IsZero() || !key.NotAfter.After(key.NotBefore) {
			return Claims{}, errors.New("invalid member issuer trust")
		}
		seen[key.ID] = true
		if key.ID == claims.KeyID {
			accepted = key
			public = decoded
		}
	}
	if accepted == nil || accepted.Revoked || claims.IssuedAt < accepted.NotBefore.Unix() || claims.ExpiresAt > accepted.NotAfter.Unix() {
		return Claims{}, errors.New("member signing key unavailable, expired or revoked")
	}
	if !ed25519.Verify(public, []byte(parts[0]+"."+parts[1]), signature) {
		return Claims{}, errors.New("invalid member signature")
	}
	return claims, nil
}
func (c Claims) validate(pool string, now time.Time) error {
	if c.Version != 1 || c.Scope != Scope || c.Issuer == "" || c.KeyID == "" || c.PoolID != pool || !poolPattern.MatchString(c.PoolID) || !walletPattern.MatchString(c.Wallet) || c.Wallet == "0x"+strings.Repeat("0", 40) || !sessionPattern.MatchString(c.SessionID) {
		return errors.New("invalid regional member scope")
	}
	if c.IssuedAt <= 0 || c.ExpiresAt <= c.IssuedAt || c.ExpiresAt-c.IssuedAt > int64(MaxTTL/time.Second) || c.IssuedAt > now.Add(5*time.Second).Unix() || now.Unix() >= c.ExpiresAt {
		return errors.New("expired or invalid member token lifetime")
	}
	return nil
}
func decode(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid member authorization encoding")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing member authorization data")
	}
	return nil
}
