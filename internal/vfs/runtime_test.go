package vfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/winfsp/cgofuse/fuse"
	"golang.org/x/text/unicode/norm"
)

// Runtime read-path tests run against real temp files: openReal serves the
// handle table on the disk, exactly like a production mount.

func TestOpenReal_ServesPlainFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cover.jpg")
	if err := os.WriteFile(src, []byte("jpeg-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := New(Options{})
	defer v.Destroy()

	rc, fh := v.openReal(src)
	if rc != 0 {
		t.Fatalf("openReal rc = %d, want 0", rc)
	}
	defer v.Release("", fh)

	buf := make([]byte, 16)
	if n := v.Read("/cover.jpg", buf, 0, fh); n != len("jpeg-bytes") {
		t.Fatalf("Read = %d, want %d", n, len("jpeg-bytes"))
	}
	if got := string(buf[:len("jpeg-bytes")]); got != "jpeg-bytes" {
		t.Errorf("read %q, want %q", got, "jpeg-bytes")
	}

	// Offset read through the same handle.
	off := make([]byte, 4)
	if n := v.Read("/cover.jpg", off, 5, fh); n != 4 {
		t.Fatalf("offset Read = %d, want 4", n)
	}
	if got := string(off); got != "byte" {
		t.Errorf("offset read %q, want %q", got, "byte")
	}
}

func TestOpenReal_ErrorMapping(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "dir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.bin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	v := New(Options{})
	defer v.Destroy()

	if rc, _ := v.openReal(filepath.Join(dir, "nope")); rc != -fuse.ENOENT {
		t.Errorf("missing file rc = %d, want ENOENT", rc)
	}
	if rc, _ := v.openReal(sub); rc != -fuse.EISDIR {
		t.Errorf("directory rc = %d, want EISDIR", rc)
	}
	if rc, _ := v.openReal(filepath.Join(dir, "f.bin")); rc != 0 {
		t.Errorf("plain file rc = %d, want 0", rc)
	}
}

// TestOpenReal_NFDFallback pins that runtime serving inherits the NFC/NFD
// retry from openRealFile: a file stored under an NFD name is found through
// an NFC query. Byte-exact hosts (Linux ext4, NFS) exercise the retry;
// normalization-insensitive hosts (APFS) skip — there is nothing to prove.
func TestOpenReal_NFDFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, norm.NFD.String("Épisode 1.flac"))
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	nfcPath := filepath.Join(dir, norm.NFC.String("Épisode 1.flac"))
	if _, err := os.Stat(nfcPath); err == nil {
		t.Skip("host filesystem is normalization-insensitive; nothing to prove")
	}

	v := New(Options{})
	defer v.Destroy()

	rc, fh := v.openReal(nfcPath)
	if rc != 0 {
		t.Fatalf("openReal(nfc query) rc = %d, want 0 via NFD retry", rc)
	}
	defer v.Release("", fh)

	buf := make([]byte, 5)
	if n := v.Read("/", buf, 0, fh); n != 5 {
		t.Fatalf("Read = %d, want 5", n)
	}
	if got := string(buf); got != "audio" {
		t.Errorf("read %q, want %q", got, "audio")
	}
}
