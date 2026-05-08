package chaintesting

import "testing"

func TestNewFakeStore_BasicUsable(t *testing.T) {
	s := NewFakeStore()
	defer s.Close()

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
	if string(got) != "v" {
		t.Errorf("Get = %q, want v", got)
	}
}
