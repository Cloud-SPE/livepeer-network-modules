package bolt

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
)

func openTempStore(t *testing.T) (store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := Open(path, Default())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestOpen_RequiresPath(t *testing.T) {
	if _, err := Open("", Default()); err == nil {
		t.Errorf("Open(\"\") should fail")
	}
}

func TestOpen_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "test.db")
	s, err := Open(path, Default())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
}

func TestBucket_PutGetDelete(t *testing.T) {
	s, _ := openTempStore(t)
	b, err := s.Bucket("foo")
	if err != nil {
		t.Fatalf("Bucket: %v", err)
	}
	if err := b.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := b.Get([]byte("k"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, []byte("v")) {
		t.Errorf("Get = %q, want v", got)
	}
	if err := b.Delete([]byte("k")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := b.Get([]byte("k")); err != store.ErrNotFound {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestBucket_GetNotFound(t *testing.T) {
	s, _ := openTempStore(t)
	b, _ := s.Bucket("foo")
	if _, err := b.Get([]byte("missing")); err != store.ErrNotFound {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
}

func TestBucket_ForEach(t *testing.T) {
	s, _ := openTempStore(t)
	b, _ := s.Bucket("foo")
	_ = b.Put([]byte("a"), []byte("1"))
	_ = b.Put([]byte("b"), []byte("2"))
	_ = b.Put([]byte("c"), []byte("3"))

	var keys []string
	_ = b.ForEach(func(k, _ []byte) error {
		keys = append(keys, string(k))
		return nil
	})
	if len(keys) != 3 {
		t.Errorf("ForEach saw %d keys, want 3", len(keys))
	}
}

func TestBucket_Scan_Prefix(t *testing.T) {
	s, _ := openTempStore(t)
	b, _ := s.Bucket("foo")
	_ = b.Put([]byte("user:1"), []byte("alice"))
	_ = b.Put([]byte("user:2"), []byte("bob"))
	_ = b.Put([]byte("admin:1"), []byte("eve"))
	_ = b.Put([]byte("other"), []byte("zzz"))

	var keys []string
	_ = b.Scan([]byte("user:"), func(k, _ []byte) error {
		keys = append(keys, string(k))
		return nil
	})
	if len(keys) != 2 {
		t.Errorf("Scan(prefix=user:) saw %d keys, want 2", len(keys))
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	s1, err := Open(path, Default())
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	b1, _ := s1.Bucket("foo")
	_ = b1.Put([]byte("k"), []byte("v"))
	if err := s1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	s2, err := Open(path, Default())
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer s2.Close()
	b2, _ := s2.Bucket("foo")
	got, err := b2.Get([]byte("k"))
	if err != nil {
		t.Fatalf("Get from re-opened: %v", err)
	}
	if !bytes.Equal(got, []byte("v")) {
		t.Errorf("Get from re-opened = %q, want v", got)
	}
}

func TestUpdate_TransactionalAcrossBuckets(t *testing.T) {
	s, _ := openTempStore(t)
	err := s.Update(func(tx store.Tx) error {
		a, err := tx.Bucket("a")
		if err != nil {
			return err
		}
		b, err := tx.Bucket("b")
		if err != nil {
			return err
		}
		_ = a.Put([]byte("k"), []byte("av"))
		_ = b.Put([]byte("k"), []byte("bv"))
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	a, _ := s.Bucket("a")
	b, _ := s.Bucket("b")
	gotA, _ := a.Get([]byte("k"))
	gotB, _ := b.Get([]byte("k"))
	if !bytes.Equal(gotA, []byte("av")) || !bytes.Equal(gotB, []byte("bv")) {
		t.Errorf("multi-bucket Update: a=%q b=%q", gotA, gotB)
	}
}

func TestView_ReadOnly(t *testing.T) {
	s, _ := openTempStore(t)
	b, _ := s.Bucket("foo")
	_ = b.Put([]byte("k"), []byte("v"))

	err := s.View(func(tx store.Tx) error {
		bkt, err := tx.Bucket("foo")
		if err != nil {
			return err
		}
		got, err := bkt.Get([]byte("k"))
		if err != nil {
			return err
		}
		if !bytes.Equal(got, []byte("v")) {
			t.Errorf("View Get = %q", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestView_NonExistentBucket(t *testing.T) {
	s, _ := openTempStore(t)
	err := s.View(func(tx store.Tx) error {
		_, err := tx.Bucket("nope")
		return err
	})
	if err == nil {
		t.Errorf("View on non-existent bucket should fail")
	}
}

func TestUpdate_RolledBackOnError(t *testing.T) {
	s, _ := openTempStore(t)
	failErr := bytes.ErrTooLarge // any non-nil
	_ = s.Update(func(tx store.Tx) error {
		b, _ := tx.Bucket("foo")
		_ = b.Put([]byte("k"), []byte("v"))
		return failErr
	})
	b, _ := s.Bucket("foo")
	if _, err := b.Get([]byte("k")); err != store.ErrNotFound {
		t.Errorf("rolled-back Update should not persist: got err=%v", err)
	}
}

func TestBucket_GetCopiesValues(t *testing.T) {
	s, _ := openTempStore(t)
	b, _ := s.Bucket("foo")
	_ = b.Put([]byte("k"), []byte{1, 2, 3})
	got, _ := b.Get([]byte("k"))
	got[0] = 99
	got2, _ := b.Get([]byte("k"))
	if got2[0] == 99 {
		t.Errorf("Get should return a copy; mutation of caller's buffer leaked")
	}
}
