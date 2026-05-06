package outbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndLoadLastSigned(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "outbox")
	last := filepath.Join(root, "lib", "last-signed.json")
	o, err := New(out, last)
	if err != nil {
		t.Fatal(err)
	}

	if b, err := o.LoadLastSigned(); err != nil || b != nil {
		t.Fatalf("expected nil, nil; got %v, %v", b, err)
	}

	envelope := []byte(`{"manifest":{}, "signature":{"value":"0xab"}}`)
	dst, err := o.Write("signed-001.json", envelope)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dst) != out {
		t.Fatalf("wrote outside outbox: %s", dst)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(envelope) {
		t.Fatalf("outbox file mismatch")
	}

	got2, err := o.LoadLastSigned()
	if err != nil {
		t.Fatal(err)
	}
	if string(got2) != string(envelope) {
		t.Fatalf("last-signed mismatch")
	}

	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("outbox file mode %o", st.Mode().Perm())
	}
}

func TestWriteRejectsPathInName(t *testing.T) {
	root := t.TempDir()
	o, err := New(filepath.Join(root, "outbox"), filepath.Join(root, "last.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{"../evil.json", "subdir/x.json", ""}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := o.Write(c, []byte("{}")); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
