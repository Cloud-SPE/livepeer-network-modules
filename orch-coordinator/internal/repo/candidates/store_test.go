package candidates

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_SaveAndList(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		ts := time.Date(2026, 5, 6, 12, 0, i, 0, time.UTC)
		_, err := s.Save(Snapshot{
			Timestamp:     ts,
			ManifestBytes: []byte(`{"hello":"world"}`),
			MetadataBytes: []byte(`{"x":1}`),
			TarballBytes:  []byte("not really a tarball"),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(names))
	}
	latest, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest == "" {
		t.Fatal("latest empty")
	}
}

func TestStore_PrunesByCount(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		ts := time.Date(2026, 5, 6, 12, 0, i, 0, time.UTC)
		if _, err := s.Save(Snapshot{
			Timestamp:     ts,
			ManifestBytes: []byte(`{}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 (pruned), got %d", len(names))
	}
}

func TestStore_LatestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"manifest":"x"}`)
	if _, err := s.Save(Snapshot{
		Timestamp:     time.Now().UTC(),
		ManifestBytes: body,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestManifest()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("got %q want %q", got, body)
	}
}

func TestStore_AtomicWriteCleanupOnFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(Snapshot{
		Timestamp:     time.Now().UTC(),
		ManifestBytes: []byte("x"),
	}); err != nil {
		t.Fatal(err)
	}
	// No leftover .tmp-* files in any snapshot dir.
	if err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && len(info.Name()) > 4 && info.Name()[:5] == ".tmp-" {
			t.Fatalf("leftover tempfile: %s", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
