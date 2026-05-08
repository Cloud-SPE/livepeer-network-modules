package bolt

import (
	"bytes"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
)

// In-transaction bucket handles (boltBucketInTx) are exercised when callers
// use Store.Update/View. These tests cover the methods that the at-rest
// boltBucket tests don't.

func TestInTx_Delete(t *testing.T) {
	s, _ := openTempStore(t)
	b, _ := s.Bucket("foo")
	_ = b.Put([]byte("k"), []byte("v"))

	err := s.Update(func(tx store.Tx) error {
		bkt, err := tx.Bucket("foo")
		if err != nil {
			return err
		}
		return bkt.Delete([]byte("k"))
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if _, err := b.Get([]byte("k")); err != store.ErrNotFound {
		t.Errorf("Delete inside Update did not persist; got err=%v", err)
	}
}

func TestInTx_ForEach(t *testing.T) {
	s, _ := openTempStore(t)
	b, _ := s.Bucket("foo")
	_ = b.Put([]byte("a"), []byte("1"))
	_ = b.Put([]byte("b"), []byte("2"))

	var keys []string
	err := s.View(func(tx store.Tx) error {
		bkt, err := tx.Bucket("foo")
		if err != nil {
			return err
		}
		return bkt.ForEach(func(k, _ []byte) error {
			keys = append(keys, string(k))
			return nil
		})
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("InTx ForEach saw %d keys, want 2", len(keys))
	}
}

func TestInTx_Scan(t *testing.T) {
	s, _ := openTempStore(t)
	b, _ := s.Bucket("foo")
	_ = b.Put([]byte("user:1"), []byte("alice"))
	_ = b.Put([]byte("user:2"), []byte("bob"))
	_ = b.Put([]byte("admin:1"), []byte("eve"))

	var keys []string
	err := s.View(func(tx store.Tx) error {
		bkt, err := tx.Bucket("foo")
		if err != nil {
			return err
		}
		return bkt.Scan([]byte("user:"), func(k, _ []byte) error {
			keys = append(keys, string(k))
			return nil
		})
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("InTx Scan saw %d keys, want 2", len(keys))
	}
}

func TestInTx_GetAndPutInsideUpdate(t *testing.T) {
	s, _ := openTempStore(t)
	err := s.Update(func(tx store.Tx) error {
		bkt, err := tx.Bucket("foo")
		if err != nil {
			return err
		}
		if err := bkt.Put([]byte("k"), []byte("v1")); err != nil {
			return err
		}
		got, err := bkt.Get([]byte("k"))
		if err != nil {
			return err
		}
		if !bytes.Equal(got, []byte("v1")) {
			t.Errorf("InTx Get within same Update = %q", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestInTx_Get_NotFound(t *testing.T) {
	s, _ := openTempStore(t)
	err := s.Update(func(tx store.Tx) error {
		bkt, err := tx.Bucket("foo")
		if err != nil {
			return err
		}
		_, err = bkt.Get([]byte("missing"))
		if err != store.ErrNotFound {
			t.Errorf("InTx Get(missing) = %v, want ErrNotFound", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
}
