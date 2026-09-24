package hostfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"
)

func TestMemFS_StatAndList(t *testing.T) {
	m := NewMem().
		AddFile("Album/track.flac", []byte("flac")).
		AddDir("Album/CD1")

	fi, err := m.Stat("Album/track.flac")
	if err != nil {
		t.Fatal(err)
	}
	if fi.IsDir() || fi.Size() != 4 {
		t.Errorf("file info = (dir=%v, size=%d), want file of size 4", fi.IsDir(), fi.Size())
	}

	entries, err := m.List("Album")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("List(Album) = %d entries, want 2", len(entries))
	}
	if entries[0].Name() != "CD1" || !entries[0].IsDir() {
		t.Errorf("entries[0] = %q (dir=%v), want CD1 dir", entries[0].Name(), entries[0].IsDir())
	}
	if entries[1].Name() != "track.flac" || entries[1].IsDir() {
		t.Errorf("entries[1] = %q (dir=%v), want track.flac file", entries[1].Name(), entries[1].IsDir())
	}
}

func TestMemFS_ListSortsDeterministically(t *testing.T) {
	m := NewMem().AddFile("/b", nil).AddFile("/a", nil).AddFile("/c", nil)
	entries, err := m.List("/")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{entries[0].Name(), entries[1].Name(), entries[2].Name()}
	if got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("List order = %v, want [a b c]", got)
	}
}

func TestMemFS_MissingPaths(t *testing.T) {
	m := NewMem().AddFile("present.flac", nil)

	if _, err := m.Stat("absent.flac"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(absent) err = %v, want ErrNotExist", err)
	}
	if _, err := m.List("Album"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("List(Album) err = %v, want ErrNotExist", err)
	}
	if _, err := m.List("present.flac"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("List(file) err = %v, want ErrInvalid", err)
	}
}

func TestMemFS_AddFileReplacesExisting(t *testing.T) {
	m := NewMem().AddFile("x.bin", []byte("old"))
	fi1, err := m.Stat("x.bin")
	if err != nil {
		t.Fatal(err)
	}

	m.AddFile("x.bin", []byte("new!"))
	fi2, err := m.Stat("x.bin")
	if err != nil {
		t.Fatal(err)
	}
	if fi2.Size() != 4 {
		t.Errorf("file size = %d, want 4 (replaced content)", fi2.Size())
	}
	// A write must refresh the file's mtime; a stale mtime would silently
	// defeat modTime-based cache invalidation.
	if !fi2.ModTime().After(fi1.ModTime()) {
		t.Errorf("replacing a file must bump its modTime: before=%v after=%v", fi1.ModTime(), fi2.ModTime())
	}
}

func TestMemFS_DirModTimeMatchesOnDiskSemantics(t *testing.T) {
	m := NewMem().AddDir("alb").AddFile("alb/track.flac", []byte("v1"))

	before := mustModTime(t, m, "alb")
	m.AddFile("alb/track.flac", []byte("v2")) // overwrite: dir entry unchanged
	if after := mustModTime(t, m, "alb"); !after.Equal(before) {
		t.Errorf("overwriting a file must NOT bump the dir mtime (disk semantics): before=%v after=%v", before, after)
	}

	before = mustModTime(t, m, "alb")
	m.AddFile("alb/new.cue", nil) // new entry: dir mtime must move
	if after := mustModTime(t, m, "alb"); !after.After(before) {
		t.Errorf("adding a new child must bump the dir mtime (disk semantics): before=%v after=%v", before, after)
	}
}

func mustModTime(t *testing.T, m FS, name string) time.Time {
	t.Helper()
	fi, err := m.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	return fi.ModTime()
}

func TestMemFS_StatSnapshotIsImmutable(t *testing.T) {
	m := NewMem().AddFile("x.bin", []byte("old"))
	fi, err := m.Stat("x.bin")
	if err != nil {
		t.Fatal(err)
	}

	m.AddFile("x.bin", []byte("new!"))
	// A held FileInfo is a snapshot, like os.Stat's: later writes must not
	// change what it reports.
	if fi.Size() != 3 {
		t.Errorf("held Stat snapshot size = %d, want 3 (\"old\")", fi.Size())
	}
}

func TestMemFS_NoPhantomChildrenOnDirFileConflict(t *testing.T) {
	// dir -> file: children must not survive the conversion.
	m := NewMem().AddDir("x/y").AddFile("x", []byte("now a file"))
	fi, err := m.Stat("x")
	if err != nil || fi.IsDir() {
		t.Errorf("x = %v, %v; want a plain file", fi, err)
	}
	if _, err := m.Stat("x/y"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("phantom child survived dir->file conversion: Stat(x/y) err = %v, want ErrNotExist", err)
	}

	// file -> deeper path: the walk must not panic on a nil map and the
	// node becomes a directory again.
	m2 := NewMem().AddFile("x", []byte("bytes")).AddFile("x/y", []byte("deeper"))
	if fi, err := m2.Stat("x"); err != nil || !fi.IsDir() {
		t.Errorf("x = %v, %v; want a directory after a deeper path was added", fi, err)
	}
	if _, err := m2.Stat("x/y"); err != nil {
		t.Errorf("deeper file unreachable: %v", err)
	}
}

func TestNFDFallback_FindsNFDStoredFileViaNFCQuery(t *testing.T) {
	// Byte-exact store (MemFS): the NFC query must miss deterministically.
	m := NewMem().AddFile(norm.NFD.String("Épisode 1/flac"), nil)
	if _, err := m.Stat(norm.NFC.String("Épisode 1") + "/flac"); err == nil {
		t.Fatal("MemFS is byte-exact: NFC query must not match the NFD path")
	}

	// Wrapped FS must transparently find the NFD form.
	f := WithNFDFallback(m)
	if _, err := f.Stat(norm.NFC.String("Épisode 1") + "/flac"); err != nil {
		t.Errorf("WithNFDFallback.Stat(nfc) = %v, want success via NFD retry", err)
	}
}

// TestNFDFallback_RealDiskByteExactHost proves the fallback against the real
// filesystem: on a byte-exact host (Linux ext4, NFS) an NFD-stored name is
// NOT found through the NFC query, and WithNFDFallback finds it — for Stat,
// Lstat and Open alike. Hosts with normalization-insensitive filesystems
// (APFS) skip: there is nothing to prove.
func TestNFDFallback_RealDiskByteExactHost(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, norm.NFD.String("Épisode 1")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, norm.NFD.String("Épisode 1"), "flac"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	plain := Default()
	wrapped := WithNFDFallback(plain)
	nfcPath := filepath.Join(dir, norm.NFC.String("Épisode 1"), "flac")
	if _, err := plain.Stat(nfcPath); err == nil {
		t.Skip("host filesystem is normalization-insensitive; nothing to prove")
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("byte-exact miss must be ErrNotExist, got %v", err)
	}

	ops := []struct {
		name string
		do   func(FS) error
	}{
		{"Stat", func(f FS) error {
			_, err := f.Stat(nfcPath)
			return err
		}},
		{"Lstat", func(f FS) error {
			_, err := f.Lstat(nfcPath)
			return err
		}},
		{"Open", func(f FS) error {
			fh, err := f.Open(nfcPath)
			if err == nil {
				_ = fh.Close()
			}
			return err
		}},
	}
	for _, op := range ops {
		if err := op.do(plain); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: byte-exact miss on plain = %v, want ErrNotExist", op.name, err)
			continue
		}
		if err := op.do(wrapped); err != nil {
			t.Errorf("%s: WithNFDFallback = %v, want success via NFD retry", op.name, err)
		}
	}
}

func TestDiskFS_OpenRejectsDirectories(t *testing.T) {
	dir := t.TempDir()
	f, err := Default().Open(dir)
	if err == nil {
		_ = f.Close()
		t.Fatal("disk Open on a directory must fail per the FS contract")
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("disk Open on dir err = %v, want fs.ErrInvalid", err)
	}
}

func TestMemFS_Open(t *testing.T) {
	m := NewMem().AddFile("/a/data.txt", []byte("hello world"))

	f, err := m.Open("/a/data.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Streaming read.
	buf := make([]byte, 5)
	if _, err := io.ReadFull(f, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Errorf("first read = %q, want %q", buf, "hello")
	}
	// Seek + read.
	if _, err := f.Seek(6, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(f, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "world" {
		t.Errorf("seeked read = %q, want %q", buf, "world")
	}
	// ReaderAt (offset reads).
	if _, err := f.ReadAt(buf, 6); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "world" {
		t.Errorf("ReadAt = %q, want %q", buf, "world")
	}
}

func TestMemFS_OpenMissingAndDir(t *testing.T) {
	m := NewMem().AddDir("/d")

	if _, err := m.Open("/nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing open error = %v, want ErrNotExist", err)
	}
	if _, err := m.Open("/d"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("dir open error = %v, want ErrInvalid", err)
	}
}

func TestNFDFallback_OpenRetriesNormalized(t *testing.T) {
	// File stored under NFC; query it in NFD. The fallback must retry the
	// NFC form and return a readable handle.
	m := NewMem().AddFile("/caf\u00e9/file.txt", []byte("caf\u00e9 data"))

	f, err := WithNFDFallback(m).Open("/cafe\u0301/file.txt")
	if err != nil {
		t.Fatalf("Open via NFD fallback: %v", err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "caf\u00e9 data" {
		t.Errorf("read = %q, want %q", got, "caf\u00e9 data")
	}
}

func TestNFDFallback_KeepsOtherErrorsUntouched(t *testing.T) {
	m := NewMem().AddFile("a.flac", nil)
	f := WithNFDFallback(m)
	if _, err := f.List("a.flac"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("List(file) err = %v, want ErrInvalid (not swallowed)", err)
	}
}

var _ os.FileInfo = memFileInfo{} // compile-time check
