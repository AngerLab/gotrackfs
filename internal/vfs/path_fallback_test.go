package vfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// TestNFDFallback_RealDiskByteExactHost proves the shared NFC/NFD helper
// against the real filesystem: on a byte-exact host (Linux ext4, NFS) an
// NFD-stored name is NOT found through the NFC query, and
// probeWithNormFallback finds it — for statPath, lstatSyscall, openRealFile
// and readDir alike. Hosts with normalization-insensitive filesystems (APFS)
// skip: there is nothing to prove.
func TestNFDFallback_RealDiskByteExactHost(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, norm.NFD.String("Épisode 1"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "flac")
	if err := os.WriteFile(file, []byte("RIFF...."), 0o644); err != nil {
		t.Fatal(err)
	}

	nfcDir := filepath.Join(base, norm.NFC.String("Épisode 1"))
	nfcFile := filepath.Join(nfcDir, "flac")
	if _, err := os.Stat(nfcDir); err == nil {
		t.Skip("host filesystem is normalization-insensitive; nothing to prove")
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("byte-exact miss must be ErrNotExist, got %v", err)
	}

	ops := []struct {
		name string
		do   func() error
	}{
		{"statPath", func() error {
			_, err := statPath(nfcFile)
			return err
		}},
		{"lstatSyscall", func() error {
			var st syscall.Stat_t
			return lstatSyscall(nfcFile, &st)
		}},
		{"openRealFile", func() error {
			f, err := openRealFile(nfcFile)
			if err == nil {
				_ = f.Close()
			}
			return err
		}},
		{"readDir", func() error {
			entries, err := readDir(nfcDir)
			if err == nil && len(entries) != 1 {
				return errors.New("want exactly the NFD-stored entry")
			}
			return err
		}},
	}
	for _, op := range ops {
		if err := op.do(); err != nil {
			t.Errorf("%s: NFC query on NFD-stored name = %v, want nil (fallback misaligned)", op.name, err)
		}
	}

	// Sanity: the same file is reachable by its real NFD path.
	if _, err := os.Stat(file); err != nil {
		t.Errorf("NFD-stored file itself must be stat-able: %v", err)
	}
}
