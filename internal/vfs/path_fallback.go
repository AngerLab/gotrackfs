package vfs

import (
	"errors"
	"io/fs"
	"os"
	"syscall"

	"github.com/AngerLab/gotrackfs/internal/hostfs"

	"golang.org/x/text/unicode/norm"
)

// defaultFS is the production filesystem: the real disk wrapped with the
// transparent NFC/NFD fallback. All real-disk access goes through it (or
// through an FS injected via Options.FS) instead of calling os.* directly.
var defaultFS = hostfs.WithNFDFallback(hostfs.Default())

// probeWithNormFallback runs probe on path and, when it fails with
// ErrNotExist, transparently retries the NFD and NFC normalized forms.
// Files stored in either normalization on byte-exact filesystems (Linux
// ext4, exFAT, NFS, SMB) are then found regardless of the caller's form.
func probeWithNormFallback[T any](path string, probe func(string) (T, error)) (T, error) {
	v, err := probe(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return v, err
	}
	for _, normPath := range []string{norm.NFD.String(path), norm.NFC.String(path)} {
		if normPath == path {
			continue
		}
		if v2, err2 := probe(normPath); err2 == nil {
			return v2, nil
		}
	}
	return v, err
}

// lstatSyscall performs syscall.Lstat with a transparent Unicode normalization fallback on ENOENT.
func lstatSyscall(path string, st *syscall.Stat_t) error {
	_, err := probeWithNormFallback(path, func(p string) (struct{}, error) {
		var s syscall.Stat_t
		if err := syscall.Lstat(p, &s); err != nil {
			return struct{}{}, err
		}
		*st = s
		return struct{}{}, nil
	})
	return err
}

// openRealFile opens an existing real file with transparent NFC/NFD fallback on ErrNotExist.
func openRealFile(path string) (*os.File, error) {
	return probeWithNormFallback(path, os.Open)
}
