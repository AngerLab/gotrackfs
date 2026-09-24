package hostfs

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/text/unicode/norm"
)

// WithNFDFallback wraps fs so that any Stat/List/Open that fails with
// ErrNotExist is transparently retried on the NFD and NFC normalized forms
// of the path. Files stored in either normalization on byte-exact
// filesystems (Linux ext4, exFAT, NFS, SMB) are then found regardless of
// the caller's form.
func WithNFDFallback(fs FS) FS {
	return &normFallbackFS{FS: fs}
}

type normFallbackFS struct {
	FS
}

// RetryNormForms runs op(name) and, when it fails with fs.ErrNotExist,
// retries op on the NFD and NFC normalized forms of name. It is the single
// implementation of the NFC/NFD retry: every method of WithNFDFallback
// delegates to it, including Lstat for symlink-aware callers.
func RetryNormForms[T any](name string, op func(string) (T, error)) (T, error) {
	v, err := op(name)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return v, err
	}
	for _, normName := range []string{norm.NFD.String(name), norm.NFC.String(name)} {
		if normName == name {
			continue
		}
		if v2, err2 := op(normName); err2 == nil {
			return v2, nil
		}
	}
	return v, err
}

// Stat implements FS.
func (n *normFallbackFS) Stat(name string) (os.FileInfo, error) {
	return RetryNormForms(name, n.FS.Stat)
}

// Lstat implements FS. Like Stat it retries the NFC/NFD forms: symlinks in
// either normalization must resolve on byte-exact filesystems too.
func (n *normFallbackFS) Lstat(name string) (os.FileInfo, error) {
	return RetryNormForms(name, n.FS.Lstat)
}

// List implements FS.
func (n *normFallbackFS) List(dir string) ([]fs.DirEntry, error) {
	return RetryNormForms(dir, n.FS.List)
}

// Open implements FS.
func (n *normFallbackFS) Open(name string) (File, error) {
	return RetryNormForms(name, n.FS.Open)
}

var _ FS = (*normFallbackFS)(nil)
