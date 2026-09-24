package vfs

import (
	"os"
	"syscall"

	"github.com/AngerLab/gotrackfs/internal/hostfs"
)

// defaultFS is the production filesystem: the real disk wrapped with the
// transparent NFC/NFD fallback. It backs Options.FS when none is injected.
// lstatSyscall and openRealFile intentionally operate on the real path:
// symlink-aware stat and slicer file handles are runtime-only concerns and
// are not part of the FS abstraction.
var defaultFS = hostfs.WithNFDFallback(hostfs.Default())

// lstatSyscall performs syscall.Lstat with a transparent Unicode normalization fallback on ENOENT.
func lstatSyscall(path string, st *syscall.Stat_t) error {
	_, err := hostfs.RetryNormForms(path, func(p string) (struct{}, error) {
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
	return hostfs.RetryNormForms(path, os.Open)
}
