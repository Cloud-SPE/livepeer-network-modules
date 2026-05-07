package published

import (
	"errors"
	"testing"
)

func TestStore_PublishAndRead(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Read(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("expected ErrEmpty, got %v", err)
	}
	if err := s.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := s.Unlock(); err != nil {
		t.Fatal(err)
	}
	body, _, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Fatalf("got %q", body)
	}
}

func TestStore_LockExclusive(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Lock(); err != nil {
		t.Fatal(err)
	}
	defer a.Unlock()
	if err := b.Lock(); !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
}

func TestStore_PublishWithoutLockFails(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish([]byte("nope")); err == nil {
		t.Fatal("expected error when publishing without lock")
	}
}

func TestStore_AtomicReplace(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish([]byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish([]byte("v2")); err != nil {
		t.Fatal(err)
	}
	body, _, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "v2" {
		t.Fatalf("got %q", body)
	}
	_ = s.Unlock()
}
