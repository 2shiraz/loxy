// Package revfs manages numbered revision directories and the atomic `live` symlink (spec §7.2).
package revfs

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type FS struct {
	Root string // e.g. /usr/local/etc/haproxy
}

func New(root string) *FS { return &FS{Root: root} }

func (f *FS) LiveDir() string         { return filepath.Join(f.Root, "live") }
func (f *FS) RevDir(rev int64) string { return filepath.Join(f.Root, "revs", fmt.Sprintf("%06d", rev)) }

// Live returns the revision number `live` points to, or 0.
func (f *FS) Live() int64 {
	t, err := os.Readlink(f.LiveDir())
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(filepath.Base(t), 10, 64)
	return n
}

// Write stages files into revs/<rev>. Files identical to the previous live revision are hard-linked.
// checkCfg is the same config rendered with Root=<rev dir>, used only for `haproxy -c`.
func (f *FS) Write(rev int64, files map[string][]byte, checkCfg []byte) (string, error) {
	dir := f.RevDir(rev)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	prev := ""
	if l := f.Live(); l > 0 && l != rev {
		prev = f.RevDir(l)
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if strings.Contains(n, "..") || filepath.IsAbs(n) {
			return "", fmt.Errorf("invalid path %q", n)
		}
		dst := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return "", err
		}
		if prev != "" {
			src := filepath.Join(prev, n)
			if old, err := os.ReadFile(src); err == nil && bytes.Equal(old, files[n]) {
				if os.Link(src, dst) == nil {
					continue
				}
			}
		}
		if err := writeFile(dst, files[n], 0o640); err != nil {
			return "", err
		}
	}
	if err := writeFile(filepath.Join(dir, "haproxy.check.cfg"), checkCfg, 0o640); err != nil {
		return "", err
	}
	return dir, nil
}

func writeFile(p string, b []byte, mode os.FileMode) error {
	fh, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := fh.Write(b); err != nil {
		fh.Close()
		return err
	}
	if err := fh.Sync(); err != nil {
		fh.Close()
		return err
	}
	return fh.Close()
}

// Swap atomically points `live` at revs/<rev>: symlink live.tmp, rename, fsync dir.
func (f *FS) Swap(rev int64) error {
	if _, err := os.Stat(f.RevDir(rev)); err != nil {
		return err
	}
	tmp := filepath.Join(f.Root, "live.tmp")
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Join("revs", fmt.Sprintf("%06d", rev)), tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, f.LiveDir()); err != nil {
		return err
	}
	d, err := os.Open(f.Root)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// ReadFile reads a file of a revision.
func (f *FS) ReadFile(rev int64, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(f.RevDir(rev), name))
}

// Revisions lists revision numbers on disk, ascending.
func (f *FS) Revisions() []int64 {
	ents, _ := os.ReadDir(filepath.Join(f.Root, "revs"))
	var out []int64
	for _, e := range ents {
		if n, err := strconv.ParseInt(e.Name(), 10, 64); err == nil {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// GC keeps the newest `keep` revisions, the live one and pinned ones.
func (f *FS) GC(keep int, pinned map[int64]bool) {
	revs := f.Revisions()
	live := f.Live()
	for i, r := range revs {
		if i >= len(revs)-keep || r == live || pinned[r] {
			continue
		}
		_ = os.RemoveAll(f.RevDir(r))
	}
}
