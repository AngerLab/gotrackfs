package hostfs

import (
	"errors"
	"io/fs"
	"os"
	"testing"

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
	m := NewMem().AddFile("x.bin", []byte("old")).AddFile("x.bin", []byte("new!"))
	fi, err := m.Stat("x.bin")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 4 {
		t.Errorf("file size = %d, want 4 (replaced content)", fi.Size())
	}
}

func TestNFDFallback_FindsNFDStoredFileViaNFCQuery(t *testing.T) {
	m := NewMem().AddFile(norm.NFD.String("Épisode 1/flac"), nil)
	plain := Default()

	if _, err := plain.Stat(norm.NFC.String("Épisode 1") + "/flac"); err == nil {
		t.Fatal("disk-backed default should not match NFD path via NFC query")
	}

	// Wrapped FS must transparently find the NFD form.
	f := WithNFDFallback(m)
	if _, err := f.Stat(norm.NFC.String("Épisode 1") + "/flac"); err != nil {
		t.Errorf("WithNFDFallback.Stat(nfc) = %v, want success via NFD retry", err)
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
