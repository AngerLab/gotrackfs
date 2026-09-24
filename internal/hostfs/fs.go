// Package hostfs abstracts the real filesystem behind a small interface,
// modeled after cockroachdb/pebble/vfs: production code and tests both talk
// to FS instead of calling os.* directly. The surface is what gotrackfs
// needs today — stat, directory listing and file reads (cue parsing, audio
// probing); it grows as consumers appear (YAGNI).
package hostfs

import (
	"io"
	"io/fs"
	"os"
)

// File is a readable file handle returned by FS.Open. The surface covers the
// current consumers: streaming reads (cue parser), offset reads (future FUSE
// reads) and seek (audio header probing). os.File and bytes.Reader both
// satisfy it.
type File interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
}

// FS is a namespace for files. Names are platform filepath names.
//
// Keeping path semantics inside the interface (rather than sprinkling
// filepath/os calls through callers) is what makes the MemFS and
// composable decorators (see WithNFDFallback) possible.
type FS interface {
	// Stat returns a FileInfo describing the named file or directory.
	// It must return an error wrapping fs.ErrNotExist when missing.
	Stat(name string) (os.FileInfo, error)

	// Lstat returns a FileInfo describing the named file or directory
	// without following a final symlink. Filesystems without symlinks
	// (MemFS) return the same result as Stat. It must return an error
	// wrapping fs.ErrNotExist when missing.
	Lstat(name string) (os.FileInfo, error)

	// List returns a listing of the given directory. The entries are
	// relative to dir (same semantics as os.ReadDir).
	List(dir string) ([]fs.DirEntry, error)

	// Open opens an existing file for reading. It must return an error
	// wrapping fs.ErrNotExist when missing and fs.ErrInvalid when name
	// names a directory.
	Open(name string) (File, error)
}

// Default returns an FS backed by the real disk (os.Stat/os.ReadDir/os.Open).
func Default() FS { return diskFS{} }

type diskFS struct{}

func (diskFS) Stat(name string) (os.FileInfo, error) { return os.Stat(name) }
func (diskFS) Lstat(name string) (os.FileInfo, error) {
	return os.Lstat(name)
}
func (diskFS) List(dir string) ([]fs.DirEntry, error) {
	return os.ReadDir(dir)
}
func (diskFS) Open(name string) (File, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	// The FS contract says Open must reject directories; os.Open lets them
	// through, so enforce it here for callers that map errors to EISDIR.
	if fi, err := f.Stat(); err == nil && fi.IsDir() {
		_ = f.Close()
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	return f, nil
}
