package vfs

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The NFC/NFD retry itself lives in hostfs.RetryNormForms (covered by the
// hostfs tests through WithNFDFallback). These tests pin the two callers
// that cannot go through an FS: symlink-aware lstat and opening source
// audio for the slicer.

func TestOpenRealFile_NFDFallback(t *testing.T) {
	dir := t.TempDir()
	nfc := filepath.Join(dir, "caf\u00e9.txt")
	if err := os.WriteFile(nfc, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := openRealFile(filepath.Join(dir, "cafe\u0301.txt"))
	if err != nil {
		t.Fatalf("openRealFile(NFD query) = %v, want success on the NFC file", err)
	}
	defer f.Close()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "content" {
		t.Errorf("read = %q, want %q", got, "content")
	}
}

func TestLstatSyscall_NFDFallback(t *testing.T) {
	dir := t.TempDir()
	nfc := filepath.Join(dir, "caf\u00e9.txt")
	if err := os.WriteFile(nfc, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var st syscall.Stat_t
	if err := lstatSyscall(filepath.Join(dir, "cafe\u0301.txt"), &st); err != nil {
		t.Fatalf("lstatSyscall(NFD query) = %v, want success on the NFC file", err)
	}
	if st.Ino == 0 {
		t.Errorf("lstat inode = 0, want the real file's inode")
	}
}
