// Package hostfs abstracts the real filesystem behind a small interface,
// modeled after cockroachdb/pebble/vfs: production code and tests both talk
// to FS instead of calling os.* directly. The minimal surface is exactly
// what gotrackfs needs today (stat + directory listing); it grows as
// consumers appear (YAGNI).
package hostfs

import (
	"io/fs"
	"os"
)

// FS is a namespace for files. Names are platform filepath names.
//
// Keeping path semantics inside the interface (rather than sprinkling
// filepath/os calls through callers) is what makes the MemFS and
// composable decorators (see WithNFDFallback) possible.
type FS interface {
	// Stat returns a FileInfo describing the named file or directory.
	// It must return an error wrapping fs.ErrNotExist when missing.
	Stat(name string) (os.FileInfo, error)

	// List returns a listing of the given directory. The entries are
	// relative to dir (same semantics as os.ReadDir).
	List(dir string) ([]fs.DirEntry, error)
}

// Default returns an FS backed by the real disk (os.Stat/os.ReadDir).
func Default() FS { return diskFS{} }

type diskFS struct{}

func (diskFS) Stat(name string) (os.FileInfo, error) { return os.Stat(name) }
func (diskFS) List(dir string) ([]fs.DirEntry, error) {
	return os.ReadDir(dir)
}
