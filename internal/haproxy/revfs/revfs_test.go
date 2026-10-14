package revfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSwapGC(t *testing.T) {
	f := New(t.TempDir())
	files := map[string][]byte{"haproxy.cfg": []byte("a"), "certs/1.pem": []byte("pem")}
	if _, err := f.Write(1, files, []byte("check")); err != nil {
		t.Fatal(err)
	}
	if err := f.Swap(1); err != nil {
		t.Fatal(err)
	}
	if f.Live() != 1 {
		t.Fatal("live")
	}
	files["haproxy.cfg"] = []byte("b")
	if _, err := f.Write(2, files, []byte("check")); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(filepath.Join(f.RevDir(1), "certs/1.pem"))
	b, _ := os.Stat(filepath.Join(f.RevDir(2), "certs/1.pem"))
	if !os.SameFile(a, b) {
		t.Error("unchanged file not hard-linked")
	}
	if err := f.Swap(2); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(f.LiveDir(), "haproxy.cfg"))
	if string(got) != "b" {
		t.Fatalf("live content %q", got)
	}
	for i := int64(3); i <= 30; i++ {
		_, _ = f.Write(i, files, nil)
	}
	f.GC(20, map[int64]bool{1: true})
	revs := f.Revisions()
	if len(revs) != 22 || revs[0] != 1 || revs[1] != 2 {
		t.Fatalf("gc kept %v", revs)
	}
}
