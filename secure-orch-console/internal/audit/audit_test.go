package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log.jsonl")
	l, err := Open(path, DefaultRotateSize)
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
	l, err := Open(filepath.Join(dir, "a.log"), DefaultRotateSize)
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
	l, err := Open(filepath.Join(t.TempDir(), "a.log"), DefaultRotateSize)
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
	l, err := Open(filepath.Join(t.TempDir(), "a.log"), DefaultRotateSize)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Append(Event{}); err == nil {
		t.Fatal("expected error on empty Kind")
	}
}

func TestRotateOnSizeThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log.jsonl")
	l, err := Open(path, 256)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	bigNote := strings.Repeat("x", 200)
	for i := 0; i < 5; i++ {
		if err := l.Append(Event{Kind: KindSign, Note: bigNote}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	rotated := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "audit.log.jsonl.") {
			rotated++
		}
	}
	if rotated == 0 {
		t.Fatalf("expected at least one rotated file, dir contents: %v", entries)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() == 0 {
		t.Fatal("current log should have at least the rotate marker")
	}
}

func TestRotateMarkerWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log.jsonl")
	l, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 3; i++ {
		if err := l.Append(Event{Kind: KindSign, Note: strings.Repeat("y", 80)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"kind":"rotate"`) {
		t.Fatalf("expected rotate marker, got %s", string(b))
	}
}

func TestRotateDisabledByZeroSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log.jsonl")
	l, err := Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 5; i++ {
		if err := l.Append(Event{Kind: KindSign, Note: strings.Repeat("z", 200)}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "audit.log.jsonl.") {
			t.Fatalf("rotation should be disabled, found rotated file %s", e.Name())
		}
	}
}
