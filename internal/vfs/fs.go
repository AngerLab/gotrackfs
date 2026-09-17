package vfs

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/winfsp/cgofuse/fuse"
)

// Options holds configuration options for the VFS filesystem.
type Options struct {
	SourceRoot string
	KeepAlbum  bool // If true, keep monolithic audio files visible alongside virtual tracks
	Debug      bool
}

// VFS implements fuse.FileSystemInterface using a path-based routing model.
type VFS struct {
	fuse.FileSystemBase
	sourceRoot string
	keepAlbum  bool
	cache      *AlbumCache

	mu         sync.Mutex
	openFiles  map[uint64]*os.File
	nextHandle uint64
}

// New creates a new VFS instance.
func New(opts Options) *VFS {
	absSource, err := filepath.Abs(opts.SourceRoot)
	if err != nil {
		absSource = opts.SourceRoot
	}

	return &VFS{
		sourceRoot: absSource,
		keepAlbum:  opts.KeepAlbum,
		cache:      NewAlbumCache(),
		openFiles:  make(map[uint64]*os.File),
	}
}

func (v *VFS) isHidden(dir, base string) bool {
	if v.keepAlbum {
		return false
	}
	realDir := filepath.Join(v.sourceRoot, dir)
	dirState, _ := v.cache.GetDirState(realDir)
	return dirState != nil && dirState.HiddenMonoliths[base]
}

func (v *VFS) Statfs(path string, stat *fuse.Statfs_t) int {
	var st syscall.Statfs_t
	if err := syscall.Statfs(v.sourceRoot, &st); err != nil {
		return -fuse.ENOENT
	}
	fillStatfs(stat, &st)
	return 0
}

func (v *VFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	cleanPath := filepath.Clean(path)
	if cleanPath == "/" || cleanPath == "." {
		return v.statRealPath(v.sourceRoot, stat)
	}

	dir := filepath.Dir(cleanPath)
	base := filepath.Base(cleanPath)

	// If this is one of the hidden monolithic audio files, return ENOENT
	if v.isHidden(dir, base) {
		return -fuse.ENOENT
	}

	realDir := filepath.Join(v.sourceRoot, dir)
	dirState, _ := v.cache.GetDirState(realDir)
	if dirState != nil {
		if vt, ok := dirState.TracksByName[base]; ok {
			// Virtual track entry
			var st syscall.Stat_t
			if err := syscall.Lstat(vt.SourceAudioPath, &st); err != nil {
				return -fuse.ENOENT
			}

			copyStat(stat, &st)
			stat.Size = vt.EstimatedSize
			stat.Mode = syscall.S_IFREG | 0444
			return 0
		}
	}

	// Real file / directory
	realPath := filepath.Join(v.sourceRoot, cleanPath)
	return v.statRealPath(realPath, stat)
}

func (v *VFS) statRealPath(realPath string, stat *fuse.Stat_t) int {
	var st syscall.Stat_t
	if err := syscall.Lstat(realPath, &st); err != nil {
		return -fuse.ENOENT
	}
	copyStat(stat, &st)
	return 0
}

func (v *VFS) Opendir(path string) (int, uint64) {
	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	base := filepath.Base(cleanPath)

	if v.isHidden(dir, base) {
		return -fuse.ENOENT, ^uint64(0)
	}

	realDir := filepath.Join(v.sourceRoot, cleanPath)
	var st syscall.Stat_t
	if err := syscall.Lstat(realDir, &st); err != nil {
		return -fuse.ENOENT, ^uint64(0)
	}
	if (st.Mode & syscall.S_IFMT) != syscall.S_IFDIR {
		return -fuse.ENOTDIR, ^uint64(0)
	}

	return 0, 0
}

func (v *VFS) Releasedir(path string, fh uint64) int {
	return 0
}

func (v *VFS) Readdir(path string, fill func(name string, stat *fuse.Stat_t, ofst int64) bool, ofst int64, fh uint64) int {
	cleanPath := filepath.Clean(path)
	realDir := filepath.Join(v.sourceRoot, cleanPath)

	entries, err := os.ReadDir(realDir)
	if err != nil {
		return -fuse.ENOENT
	}

	fill(".", nil, 0)
	fill("..", nil, 0)

	dirState, _ := v.cache.GetDirState(realDir)
	if dirState != nil {
		// Emit real entries (excluding the hidden monolithic audio files)
		for _, e := range entries {
			if !v.keepAlbum && dirState.HiddenMonoliths[e.Name()] {
				continue
			}
			fill(e.Name(), nil, 0)
		}

		// Emit virtual tracks across all albums in this directory
		for _, album := range dirState.Albums {
			for _, vt := range album.Tracks {
				fill(vt.FileName, nil, 0)
			}
		}
		return 0
	}

	// Plain directory without album CUE
	for _, e := range entries {
		fill(e.Name(), nil, 0)
	}

	return 0
}

func (v *VFS) Open(path string, flags int) (int, uint64) {
	// Read-only filesystem
	if (flags & fuse.O_ACCMODE) != fuse.O_RDONLY {
		return -fuse.EACCES, ^uint64(0)
	}

	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	base := filepath.Base(cleanPath)

	// Consistent with Getattr: hidden monoliths cannot be opened
	if v.isHidden(dir, base) {
		return -fuse.ENOENT, ^uint64(0)
	}

	realDir := filepath.Join(v.sourceRoot, dir)
	dirState, _ := v.cache.GetDirState(realDir)
	if dirState != nil {
		if _, ok := dirState.TracksByName[base]; ok {
			// Slicing not implemented yet
			return -fuse.ENOSYS, ^uint64(0)
		}
	}

	realPath := filepath.Join(v.sourceRoot, cleanPath)
	f, err := os.Open(realPath)
	if err != nil {
		if os.IsNotExist(err) {
			return -fuse.ENOENT, ^uint64(0)
		}
		return -fuse.EACCES, ^uint64(0)
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	v.nextHandle++
	fh := v.nextHandle
	v.openFiles[fh] = f

	return 0, fh
}

func (v *VFS) Read(path string, buff []byte, ofst int64, fh uint64) int {
	v.mu.Lock()
	f, ok := v.openFiles[fh]
	v.mu.Unlock()

	if !ok {
		return -fuse.EBADF
	}

	n, err := f.ReadAt(buff, ofst)
	if err != nil && err != io.EOF {
		return -fuse.EIO
	}

	return n
}

func (v *VFS) Release(path string, fh uint64) int {
	v.mu.Lock()
	f, ok := v.openFiles[fh]
	if ok {
		delete(v.openFiles, fh)
	}
	v.mu.Unlock()

	if ok && f != nil {
		_ = f.Close()
	}

	return 0
}
