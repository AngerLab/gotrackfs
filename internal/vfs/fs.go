package vfs

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/winfsp/cgofuse/fuse"
)

// Options holds configuration options for the VFS filesystem.
type Options struct {
	SourceRoot string
	KeepAlbum  bool         // If true, keep monolithic audio files visible alongside virtual tracks
	Debug      bool         // If true, enables verbose debug logging
	Logger     *slog.Logger // Optional structured logger. If nil, a default text logger is used with level based on Debug.
}

// VFS implements fuse.FileSystemInterface using a path-based routing model.
type VFS struct {
	fuse.FileSystemBase
	sourceRoot string
	keepAlbum  bool
	debug      bool
	logger     *slog.Logger
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

	logger := opts.Logger
	if logger == nil {
		level := slog.LevelInfo
		if opts.Debug {
			level = slog.LevelDebug
		}
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	}

	return &VFS{
		sourceRoot: absSource,
		keepAlbum:  opts.KeepAlbum,
		debug:      opts.Debug,
		logger:     logger,
		cache:      NewAlbumCache(),
		openFiles:  make(map[uint64]*os.File),
	}
}

type resolvedNode struct {
	cleanPath string
	realPath  string
	dirState  *DirState
	track     *VirtualTrack
	isHidden  bool
}

func (v *VFS) resolve(path, op string) resolvedNode {
	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	base := filepath.Base(cleanPath)
	realDir := filepath.Join(v.sourceRoot, dir)
	realPath := filepath.Join(v.sourceRoot, cleanPath)

	dirState, err := v.cache.GetDirState(realDir)
	if err != nil {
		v.logger.Debug("cache: failed to get dir state", "op", op, "dir", realDir, "path", cleanPath, "error", err)
	}

	node := resolvedNode{
		cleanPath: cleanPath,
		realPath:  realPath,
		dirState:  dirState,
	}

	if dirState != nil {
		if !v.keepAlbum && dirState.HiddenMonoliths[base] {
			node.isHidden = true
			return node
		}
		if vt, ok := dirState.TracksByName[base]; ok {
			node.track = vt
		}
	}

	return node
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

	node := v.resolve(cleanPath, "Getattr")
	if node.isHidden {
		return -fuse.ENOENT
	}

	if node.track != nil {
		// Virtual track entry
		var st syscall.Stat_t
		if err := syscall.Lstat(node.track.SourceAudioPath, &st); err != nil {
			return -fuse.ENOENT
		}

		copyStat(stat, &st)
		stat.Size = node.track.EstimatedSize
		stat.Mode = syscall.S_IFREG | 0444
		return 0
	}

	// Real file / directory
	return v.statRealPath(node.realPath, stat)
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
	if cleanPath == "/" || cleanPath == "." {
		return 0, 0
	}

	node := v.resolve(cleanPath, "Opendir")
	if node.isHidden {
		return -fuse.ENOENT, ^uint64(0)
	}

	var st syscall.Stat_t
	if err := syscall.Lstat(node.realPath, &st); err != nil {
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

	dirState, err := v.cache.GetDirState(realDir)
	if err != nil {
		v.logger.Debug("cache: failed to get dir state in Readdir", "dir", realDir, "error", err)
	}
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
	node := v.resolve(cleanPath, "Open")
	if node.isHidden {
		return -fuse.ENOENT, ^uint64(0)
	}
	if node.track != nil {
		// Slicing not implemented yet
		return -fuse.ENOSYS, ^uint64(0)
	}

	f, err := os.Open(node.realPath)
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
