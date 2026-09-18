package portal

import (
	"encoding/hex"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestLoginRestartReplayLogout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portal.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	key, _ := crypto.GenerateKey()
	wallet := crypto.PubkeyToAddress(key.PublicKey).Hex()
	c, err := s.Challenge("https://portal.example", wallet, "192.0.2.1", now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Message, "Joining each regional pool requires separate acceptance") {
		t.Fatal("missing consent boundary")
	}
	sig, _ := crypto.Sign(accounts.TextHash([]byte(c.Message)), key)
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	secret, session, err := s.Login(c.ID, hex.EncodeToString(sig), now)
	if err != nil {
		t.Fatal(err)
	}
	if secret == session.ID || session.Wallet != strings.ToLower(wallet) {
		t.Fatal("opaque session or identity wrong")
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Session(secret, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login(c.ID, hex.EncodeToString(sig), now); err == nil {
		t.Fatal("nonce replay")
	}
	if _, err := s.Session(secret, now.Add(25*time.Hour)); err == nil {
		t.Fatal("expired session")
	}
	if err := s.Logout(secret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(secret, now); err == nil {
		t.Fatal("logged out session")
	}
}
func TestChallengeSingleWinnerAndBadSignatureBurnsNonce(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "portal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	key, _ := crypto.GenerateKey()
	wallet := crypto.PubkeyToAddress(key.PublicKey).Hex()
	c, _ := s.Challenge("https://portal.example", wallet, "192.0.2.1", now)
	sig, _ := crypto.Sign(accounts.TextHash([]byte(c.Message)), key)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, err := s.Login(c.ID, hex.EncodeToString(sig), now); results <- err }()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("%d winners", wins)
	}
	c, _ = s.Challenge("https://portal.example", wallet, "192.0.2.1", now)
	sig, _ = crypto.Sign(accounts.TextHash([]byte(c.Message)), key)
	if _, _, err := s.Login(c.ID, "bad", now); err == nil {
		t.Fatal("bad signature accepted")
	}
	if _, _, err := s.Login(c.ID, hex.EncodeToString(sig), now); err == nil {
		t.Fatal("failed attempt replay")
	}
	for range 3 {
		if _, err := s.Challenge("https://portal.example", wallet, "192.0.2.1", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Challenge("https://portal.example", wallet, "192.0.2.1", now); err == nil {
		t.Fatal("rate limit missing")
	}
}
