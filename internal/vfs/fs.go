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

func (v *VFS) Statfs(path string, stat *fuse.Statfs_t) int {
	var st syscall.Statfs_t
	if err := syscall.Statfs(v.sourceRoot, &st); err != nil {
		return -fuse.ENOENT
	}
	stat.Bsize = uint64(st.Bsize)
	stat.Frsize = 1
	stat.Blocks = uint64(st.Blocks)
	stat.Bfree = uint64(st.Bfree)
	stat.Bavail = uint64(st.Bavail)
	stat.Files = uint64(st.Files)
	stat.Ffree = uint64(st.Ffree)
	stat.Favail = uint64(st.Ffree)
	stat.Namemax = 255
	return 0
}

func (v *VFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	cleanPath := filepath.Clean(path)
	if cleanPath == "/" || cleanPath == "." {
		return v.statRealPath(v.sourceRoot, stat)
	}

	dir := filepath.Dir(cleanPath)
	base := filepath.Base(cleanPath)
	realDir := filepath.Join(v.sourceRoot, dir)

	// Check if this path references a virtual track in an album directory
	album, _ := v.cache.GetAlbum(realDir)
	if album != nil {
		if vt, ok := album.TracksByName[base]; ok {
			// Virtual track entry
			var st syscall.Stat_t
			if err := syscall.Lstat(album.SourceAudioPath, &st); err != nil {
				return -fuse.ENOENT
			}

			copyStat(stat, &st)
			stat.Size = vt.EstimatedSize
			stat.Mode = syscall.S_IFREG | 0444
			return 0
		}

		// If this is the monolithic source audio and keepAlbum is false, hide it
		if !v.keepAlbum && filepath.Clean(album.SourceAudioPath) == filepath.Join(realDir, base) {
			return -fuse.ENOENT
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

	album, _ := v.cache.GetAlbum(realDir)
	if album != nil {
		sourceAudioBase := filepath.Base(album.SourceAudioPath)

		// Emit real entries (excluding the monolithic audio if !keepAlbum)
		for _, e := range entries {
			if !v.keepAlbum && e.Name() == sourceAudioBase {
				continue
			}
			fill(e.Name(), nil, 0)
		}

		// Emit virtual tracks
		for _, vt := range album.Tracks {
			fill(vt.FileName, nil, 0)
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
	realDir := filepath.Join(v.sourceRoot, dir)

	album, _ := v.cache.GetAlbum(realDir)
	if album != nil {
		if _, ok := album.TracksByName[base]; ok {
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

func copyStat(dst *fuse.Stat_t, src *syscall.Stat_t) {
	dst.Dev = uint64(src.Dev)
	dst.Ino = uint64(src.Ino)
	dst.Mode = uint32(src.Mode)
	dst.Nlink = uint32(src.Nlink)
	dst.Uid = uint32(src.Uid)
	dst.Gid = uint32(src.Gid)
	dst.Rdev = uint64(src.Rdev)
	dst.Size = int64(src.Size)
	dst.Atim = fuse.Timespec{Sec: src.Atimespec.Sec, Nsec: src.Atimespec.Nsec}
	dst.Mtim = fuse.Timespec{Sec: src.Mtimespec.Sec, Nsec: src.Mtimespec.Nsec}
	dst.Ctim = fuse.Timespec{Sec: src.Ctimespec.Sec, Nsec: src.Ctimespec.Nsec}
	dst.Birthtim = fuse.Timespec{Sec: src.Birthtimespec.Sec, Nsec: src.Birthtimespec.Nsec}
	dst.Blksize = int64(src.Blksize)
	dst.Blocks = int64(src.Blocks)
}
