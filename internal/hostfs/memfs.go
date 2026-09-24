package hostfs

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemFS is an in-memory FS for tests, mirroring pebble/vfs.NewMem: no disk,
// no t.TempDir(), instant setup of exact directory shapes.
type MemFS struct {
	mu   sync.RWMutex
	root *memNode
}

type memNode struct {
	name     string
	isDir    bool
	data     []byte
	children map[string]*memNode
	modTime  time.Time
}

// NewMem returns a new empty memory-backed FS.
func NewMem() *MemFS {
	return &MemFS{root: &memNode{name: "/", isDir: true, children: map[string]*memNode{}, modTime: time.Now()}}
}

// AddFile creates path (with parents) holding data. Existing files are
// replaced. It returns the FS for chaining.
func (m *MemFS) AddFile(filePath string, data []byte) *MemFS {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.ensure(path.Clean("/"+filePath), true)
	n.data = append([]byte(nil), data...)
	return m
}

// AddDir creates an empty directory path (with parents).
func (m *MemFS) AddDir(dirPath string) *MemFS {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensure(path.Clean("/"+dirPath), false)
	return m
}

// ensure walks to the node at cleanPath, creating parents as needed.
// If isFile is true the final segment becomes a file, otherwise a directory.
func (m *MemFS) ensure(cleanPath string, isFile bool) *memNode {
	cur := m.root
	if cleanPath == "/" {
		return cur
	}
	for _, seg := range strings.Split(strings.Trim(cleanPath, "/"), "/") {
		next, ok := cur.children[seg]
		if !ok {
			// Intermediate segments are always directories; only the final
			// segment may become a file (isFile below).
			next = &memNode{name: seg, isDir: true, children: map[string]*memNode{}, modTime: time.Now()}
			cur.children[seg] = next
		}
		cur = next
	}
	cur.isDir = !isFile
	if cur.modTime.IsZero() {
		cur.modTime = time.Now()
	}
	return cur
}

// Stat implements FS.
func (m *MemFS) Stat(name string) (os.FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n, ok := m.lookup(name)
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return memFileInfo{n: n}, nil
}

// List implements FS.
func (m *MemFS) List(dir string) ([]fs.DirEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n, ok := m.lookup(dir)
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: dir, Err: fs.ErrNotExist}
	}
	if !n.isDir {
		return nil, &fs.PathError{Op: "readdir", Path: dir, Err: fs.ErrInvalid}
	}
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]fs.DirEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, memDirEntry{n: n.children[name]})
	}
	return entries, nil
}

// Open implements FS. The returned handle reads a point-in-time snapshot of
// the file's bytes; MemFS files are immutable after AddFile.
func (m *MemFS) Open(name string) (File, error) {
	m.mu.RLock()
	n, ok := m.lookup(name)
	if !ok {
		m.mu.RUnlock()
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if n.isDir {
		m.mu.RUnlock()
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	data := append([]byte(nil), n.data...)
	m.mu.RUnlock()
	return &memFile{r: bytes.NewReader(data)}, nil
}

// memFile is an immutable in-memory file handle over a byte snapshot.
type memFile struct{ r *bytes.Reader }

func (f *memFile) Read(p []byte) (int, error)              { return f.r.Read(p) }
func (f *memFile) ReadAt(p []byte, off int64) (int, error) { return f.r.ReadAt(p, off) }
func (f *memFile) Seek(off int64, whence int) (int64, error) {
	return f.r.Seek(off, whence)
}
func (f *memFile) Close() error { return nil }

// lookup resolves a nested path from the root. Dir paths may have a
// trailing slash; the root itself is "/".
func (m *MemFS) lookup(name string) (*memNode, bool) {
	clean := path.Clean("/" + name)
	if clean == "/" {
		return m.root, true
	}
	cur := m.root
	for _, seg := range strings.Split(strings.Trim(clean, "/"), "/") {
		next, ok := cur.children[seg]
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

var _ fs.FileInfo = memFileInfo{}

type memFileInfo struct{ n *memNode }

func (f memFileInfo) Name() string       { return f.n.name }
func (f memFileInfo) Size() int64        { return int64(len(f.n.data)) }
func (f memFileInfo) Mode() os.FileMode  { return f.n.mode() }
func (f memFileInfo) ModTime() time.Time { return f.n.modTime }
func (f memFileInfo) IsDir() bool        { return f.n.isDir }
func (f memFileInfo) Sys() any           { return nil }

func (n *memNode) mode() os.FileMode {
	if n.isDir {
		return fs.ModeDir | 0o555
	}
	return 0o644
}

var _ fs.DirEntry = memDirEntry{}

type memDirEntry struct{ n *memNode }

func (e memDirEntry) Name() string               { return e.n.name }
func (e memDirEntry) IsDir() bool                { return e.n.isDir }
func (e memDirEntry) Type() fs.FileMode          { return e.n.mode() }
func (e memDirEntry) Info() (fs.FileInfo, error) { return memFileInfo{n: e.n}, nil }

func (m *MemFS) String() string { return fmt.Sprintf("MemFS(%d dirs/files)", len(m.root.children)) }

var _ FS = (*MemFS)(nil)
