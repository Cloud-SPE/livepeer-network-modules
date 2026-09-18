package portal

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	bolt "go.etcd.io/bbolt"
)

var errUnauthorized = errors.New("wallet authentication required")
var walletPattern = regexp.MustCompile(`^0x[0-9a-f]{40}$`)

type Store struct{ db *bolt.DB }
type Challenge struct {
	ID        string    `json:"id"`
	Wallet    string    `json:"wallet"`
	Message   string    `json:"message"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Session struct {
	ID        string    `json:"id"`
	Wallet    string    `json:"wallet"`
	ExpiresAt time.Time `json:"expires_at"`
}

func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("persistent state path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"challenges", "sessions", "limits", "bootstrap"} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func randomSecret() (string, error) {
	var b [32]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func secretHash(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}
func (s *Store) Challenge(origin, wallet, ip string, now time.Time) (Challenge, error) {
	wallet = strings.ToLower(wallet)
	if !walletPattern.MatchString(wallet) || wallet == "0x"+strings.Repeat("0", 40) {
		return Challenge{}, errors.New("invalid wallet")
	}
	nonce, err := randomSecret()
	if err != nil {
		return Challenge{}, err
	}
	c := Challenge{ID: nonce, Wallet: wallet, ExpiresAt: now.Add(5 * time.Minute)}
	c.Message = fmt.Sprintf("Sign in to the member portal at %s\nWallet: %s\nNonce: %s\nExpires: %s\nThis proves wallet identity only. Joining each regional pool requires separate acceptance of its terms.", origin, wallet, nonce, c.ExpiresAt.UTC().Format(time.RFC3339))
	err = s.db.Update(func(tx *bolt.Tx) error {
		limits := tx.Bucket([]byte("limits"))
		for _, key := range []string{"wallet:" + wallet, "ip:" + ip} {
			var window struct {
				Start time.Time
				Count int
			}
			raw := limits.Get([]byte(key))
			if raw != nil {
				if err := json.Unmarshal(raw, &window); err != nil {
					return err
				}
			}
			if now.Sub(window.Start) >= time.Minute {
				window.Start = now
				window.Count = 0
			}
			if window.Count >= 5 {
				return errors.New("sign-in rate limit reached")
			}
			window.Count++
			raw, err := json.Marshal(window)
			if err != nil {
				return err
			}
			if err := limits.Put([]byte(key), raw); err != nil {
				return err
			}
		}
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		return tx.Bucket([]byte("challenges")).Put([]byte(c.ID), raw)
	})
	return c, err
}

// Consume before verifying: failed signatures cannot reuse a challenge, including after restart.
func (s *Store) Login(id, signature string, now time.Time) (string, Session, error) {
	var c Challenge
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("challenges"))
		raw := b.Get([]byte(id))
		if raw == nil {
			return errUnauthorized
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		return b.Delete([]byte(id))
	})
	if err != nil {
		return "", Session{}, err
	}
	if !now.Before(c.ExpiresAt) {
		return "", Session{}, errUnauthorized
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(signature, "0x"))
	if err != nil || len(sig) != 65 {
		return "", Session{}, errUnauthorized
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	if sig[64] > 1 {
		return "", Session{}, errUnauthorized
	}
	key, err := crypto.SigToPub(accounts.TextHash([]byte(c.Message)), sig)
	if err != nil || strings.ToLower(crypto.PubkeyToAddress(*key).Hex()) != c.Wallet {
		return "", Session{}, errUnauthorized
	}
	secret, err := randomSecret()
	if err != nil {
		return "", Session{}, err
	}
	session := Session{ID: secretHash(secret), Wallet: c.Wallet, ExpiresAt: now.Add(24 * time.Hour)}
	err = s.db.Update(func(tx *bolt.Tx) error {
		raw, err := json.Marshal(session)
		if err != nil {
			return err
		}
		return tx.Bucket([]byte("sessions")).Put([]byte(session.ID), raw)
	})
	return secret, session, err
}
func (s *Store) Session(secret string, now time.Time) (Session, error) {
	var session Session
	if len(secret) != 64 {
		return session, errUnauthorized
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte("sessions")).Get([]byte(secretHash(secret)))
		if raw == nil {
			return errUnauthorized
		}
		return json.Unmarshal(raw, &session)
	})
	if err == nil && !now.Before(session.ExpiresAt) {
		err = errUnauthorized
	}
	return session, err
}
func (s *Store) Logout(secret string) error {
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("sessions")).Delete([]byte(secretHash(secret))) })
}
