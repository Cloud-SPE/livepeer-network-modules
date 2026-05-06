package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log.jsonl")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	for i := 0; i < 3; i++ {
		if err := l.Append(Event{Kind: KindBoot, Note: "test"}); err != nil {
			t.Fatal(err)
		}
	}

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if e.Kind != KindBoot {
			t.Fatalf("got kind %s", e.Kind)
		}
		if e.At.IsZero() {
			t.Fatal("At not set")
		}
		count++
	}
	if count != 3 {
		t.Fatalf("expected 3 events, got %d", count)
	}
}

func TestAppendConcurrent(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "a.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Append(Event{Kind: KindSign}); err != nil {
				t.Errorf("append: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestAppendAfterClose(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "a.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Event{Kind: KindBoot}); err != ErrLogClosed {
		t.Fatalf("expected ErrLogClosed, got %v", err)
	}
}

func TestAppendRejectsEmptyKind(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "a.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Append(Event{}); err == nil {
		t.Fatal("expected error on empty Kind")
	}
}
