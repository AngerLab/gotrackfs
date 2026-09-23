package hostfs

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/text/unicode/norm"
)

// WithNFDFallback wraps fs so that any Stat/List that fails with
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

func (n *normFallbackFS) Stat(name string) (os.FileInfo, error) {
	s, err := n.FS.Stat(name)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return s, err
	}
	for _, normPath := range []string{norm.NFD.String(name), norm.NFC.String(name)} {
		if normPath == name {
			continue
		}
		if s2, err2 := n.FS.Stat(normPath); err2 == nil {
			return s2, nil
		}
	}
	return s, err
}

func (n *normFallbackFS) List(dir string) ([]fs.DirEntry, error) {
	e, err := n.FS.List(dir)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return e, err
	}
	for _, normDir := range []string{norm.NFD.String(dir), norm.NFC.String(dir)} {
		if normDir == dir {
			continue
		}
		if e2, err2 := n.FS.List(normDir); err2 == nil {
			return e2, nil
		}
	}
	return e, err
}
