package vfs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"

	"gotrackfs/internal/cutter"

	"github.com/winfsp/cgofuse/fuse"
	"golang.org/x/text/unicode/norm"
)

// TrackSlicer defines the interface needed by VFS to slice and acquire tracks on-demand.
type TrackSlicer interface {
	Acquire(ctx context.Context, key string, req cutter.TrackRequest) (string, error)
	Release(key string)
	GetExisting(key string) (int64, bool)
	Close() error
}

// Options holds configuration options for the VFS filesystem.
type Options struct {
	SourceRoot string
	KeepAlbum  bool         // If true, keep monolithic audio files visible alongside virtual tracks
	Debug      bool         // If true, enables verbose debug logging
	Logger     *slog.Logger // Optional structured logger. If nil, a default text logger is used with level based on Debug.
	Slicer     TrackSlicer  // Optional audio slicer implementation. If nil, virtual track playback is disabled (Open returns ENOSYS).
}

type fileHandle struct {
	file      *os.File
	cutterKey string // non-empty if acquired via cutter
	track     *VirtualTrack
	mu        sync.Mutex
}

// VFS implements fuse.FileSystemInterface using a path-based routing model.
type VFS struct {
	fuse.FileSystemBase
	sourceRoot string
	keepAlbum  bool
	logger     *slog.Logger
	cache      *AlbumCache
	slicer     TrackSlicer

	mu         sync.Mutex
	openFiles  map[uint64]*fileHandle
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
		logger:     logger,
		cache:      NewAlbumCache(logger),
		slicer:     opts.Slicer,
		openFiles:  make(map[uint64]*fileHandle),
	}
}

type resolvedNode struct {
	cleanPath    string
	realPath     string
	dirState     *DirState
	track        *VirtualTrack
	isVirtualDir bool
	subDirState  *DirState
	isHidden     bool
}

func cleanNormPath(path string) string {
	return norm.NFC.String(filepath.Clean(path))
}

func (v *VFS) resolve(path, op string) resolvedNode {
	cleanPath := cleanNormPath(path)
	if cleanPath == "/" || cleanPath == "." {
		return resolvedNode{
			cleanPath: cleanPath,
			realPath:  v.sourceRoot,
		}
	}

	base := filepath.Base(cleanPath)
	dir := filepath.Dir(cleanPath)
	realDir := filepath.Join(v.sourceRoot, dir)
	realPath := filepath.Join(v.sourceRoot, cleanPath)

	// 1. Try resolving dir as a real directory
	dirState, err := v.cache.GetDirState(realDir)
	if err != nil && !os.IsNotExist(err) {
		v.logger.Debug("cache: failed to get dir state", "op", op, "dir", realDir, "path", cleanPath, "error", err)
	}

	if dirState != nil {
		node := resolvedNode{
			cleanPath: cleanPath,
			realPath:  realPath,
			dirState:  dirState,
		}

		// Check if base is a virtual subdirectory (e.g. CD1)
		if subState, ok := dirState.Subdirs[base]; ok {
			node.isVirtualDir = true
			node.subDirState = subState
			return node
		}

		// Check if base is a hidden monolith
		if !v.keepAlbum && dirState.HiddenMonoliths[base] {
			node.isHidden = true
			return node
		}

		// Check if base is a virtual track (in flat single-album mode)
		if vt, ok := dirState.TracksByName[base]; ok {
			node.track = vt
			return node
		}

		// Otherwise it's a real file/dir in realDir
		return node
	}

	// 2. dir might be a virtual subdirectory inside a real parent dir (e.g. /TheWall/CD1/01. In the Flesh.flac)
	parentDir := filepath.Dir(dir)
	subDirName := filepath.Base(dir)
	parentRealDir := filepath.Join(v.sourceRoot, parentDir)

	parentState, pErr := v.cache.GetDirState(parentRealDir)
	if pErr != nil && !os.IsNotExist(pErr) {
		v.logger.Debug("cache: failed to get parent dir state", "op", op, "parent", parentRealDir, "path", cleanPath, "error", pErr)
	}

	if parentState != nil {
		if subState, ok := parentState.Subdirs[subDirName]; ok {
			node := resolvedNode{
				cleanPath:   cleanPath,
				realPath:    realPath,
				dirState:    subState,
				subDirState: subState,
			}

			// Check if base is a virtual track in this subState
			if vt, ok := subState.TracksByName[base]; ok {
				node.track = vt
				return node
			}

			// Check if base is a mirrored file (e.g. cover.jpg)
			if mirrorPath, ok := subState.MirroredFiles[base]; ok {
				node.realPath = mirrorPath
				return node
			}

			// Neither track nor mirrored file in virtual subdir -> it does not exist
			return node
		}
	}

	// 3. Fallback: plain real path
	return resolvedNode{
		cleanPath: cleanPath,
		realPath:  realPath,
	}
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
	cleanPath := cleanNormPath(path)
	if cleanPath == "/" || cleanPath == "." {
		return v.statRealPath(v.sourceRoot, stat)
	}

	node := v.resolve(cleanPath, "Getattr")
	if node.isHidden {
		return -fuse.ENOENT
	}

	if node.isVirtualDir {
		parentRealDir := filepath.Dir(node.realPath)
		var st syscall.Stat_t
		if err := syscall.Lstat(parentRealDir, &st); err != nil {
			return -fuse.ENOENT
		}
		copyStat(stat, &st)
		stat.Mode = syscall.S_IFDIR | 0555
		return 0
	}

	if node.track != nil {
		// Virtual track entry
		var st syscall.Stat_t
		if err := syscall.Lstat(node.track.Request.SourceAudioPath, &st); err != nil {
			return -fuse.ENOENT
		}

		copyStat(stat, &st)
		size := node.track.EstimatedSize
		if v.slicer != nil {
			if exactSize, ok := v.slicer.GetExisting(node.track.CutterKey); ok {
				size = exactSize
			}
		}
		stat.Size = size
		stat.Mode = syscall.S_IFREG | 0444
		return 0
	}

	// Real file / directory (or mirrored file)
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
	cleanPath := cleanNormPath(path)
	if cleanPath == "/" || cleanPath == "." {
		return 0, 0
	}

	node := v.resolve(cleanPath, "Opendir")
	if node.isHidden {
		return -fuse.ENOENT, ^uint64(0)
	}
	if node.isVirtualDir {
		return 0, 0
	}
	if node.track != nil {
		return -fuse.ENOTDIR, ^uint64(0)
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
	cleanPath := cleanNormPath(path)
	if cleanPath != "/" && cleanPath != "." {
		node := v.resolve(cleanPath, "Readdir")
		if node.isHidden {
			return -fuse.ENOENT
		}
		if node.track != nil {
			return -fuse.ENOTDIR
		}
		if node.isVirtualDir {
			fill(".", nil, 0)
			fill("..", nil, 0)

			var names []string
			for mName := range node.subDirState.MirroredFiles {
				names = append(names, mName)
			}
			for tName := range node.subDirState.TracksByName {
				names = append(names, tName)
			}
			slices.Sort(names)
			for _, name := range names {
				fill(name, nil, 0)
			}
			return 0
		}
	}

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

	var names []string
	if dirState != nil {
		for _, e := range entries {
			eName := norm.NFC.String(e.Name())
			if !v.keepAlbum && dirState.HiddenMonoliths[eName] {
				continue
			}
			names = append(names, e.Name())
		}

		for subName := range dirState.Subdirs {
			names = append(names, subName)
		}
		for trackName := range dirState.TracksByName {
			names = append(names, trackName)
		}
	} else {
		for _, e := range entries {
			names = append(names, e.Name())
		}
	}

	slices.Sort(names)
	for _, name := range names {
		fill(name, nil, 0)
	}

	return 0
}

func (v *VFS) Open(path string, flags int) (int, uint64) {
	// Read-only filesystem
	if (flags & fuse.O_ACCMODE) != fuse.O_RDONLY {
		return -fuse.EACCES, ^uint64(0)
	}

	cleanPath := cleanNormPath(path)
	node := v.resolve(cleanPath, "Open")
	if node.isHidden {
		return -fuse.ENOENT, ^uint64(0)
	}
	if node.isVirtualDir {
		return -fuse.EISDIR, ^uint64(0)
	}
	if node.track != nil {
		if v.slicer == nil {
			v.logger.Warn("vfs: cannot slice audio track: no audio slicer configured (is ffmpeg installed?)", "track", node.track.FileName)
			return -fuse.ENOSYS, ^uint64(0)
		}

		v.mu.Lock()
		v.nextHandle++
		fh := v.nextHandle
		v.openFiles[fh] = &fileHandle{
			cutterKey: node.track.CutterKey,
			track:     node.track,
		}
		v.mu.Unlock()

		return 0, fh
	}

	f, err := os.Open(node.realPath)
	if err != nil {
		if os.IsNotExist(err) {
			return -fuse.ENOENT, ^uint64(0)
		}
		return -fuse.EACCES, ^uint64(0)
	}

	if fi, statErr := f.Stat(); statErr == nil && fi.IsDir() {
		_ = f.Close()
		return -fuse.EISDIR, ^uint64(0)
	}

	v.mu.Lock()
	v.nextHandle++
	fh := v.nextHandle
	v.openFiles[fh] = &fileHandle{file: f}
	v.mu.Unlock()

	return 0, fh
}

func (v *VFS) Read(path string, buff []byte, ofst int64, fh uint64) int {
	v.mu.Lock()
	h, ok := v.openFiles[fh]
	v.mu.Unlock()

	if !ok {
		return -fuse.EBADF
	}

	if h.file == nil {
		if h.track == nil {
			return -fuse.EBADF
		}

		// Lazy slice on first Read!
		h.mu.Lock()
		if h.file == nil {
			tempPath, err := v.slicer.Acquire(context.Background(), h.cutterKey, h.track.Request)
			if err != nil {
				h.mu.Unlock()
				v.logger.Error("vfs: failed to slice audio track", "track", h.track.FileName, "error", err)
				return -fuse.EIO
			}

			f, err := os.Open(tempPath)
			if err != nil {
				v.slicer.Release(h.cutterKey)
				h.mu.Unlock()
				return -fuse.EIO
			}
			h.file = f
		}
		h.mu.Unlock()
	}

	n, err := h.file.ReadAt(buff, ofst)
	if err != nil && err != io.EOF {
		return -fuse.EIO
	}

	return n
}

func (v *VFS) Flush(path string, fh uint64) int {
	return 0
}

func (v *VFS) Release(path string, fh uint64) int {
	v.mu.Lock()
	h, ok := v.openFiles[fh]
	if ok {
		delete(v.openFiles, fh)
	}
	v.mu.Unlock()

	if ok && h != nil {
		h.mu.Lock()
		if h.file != nil {
			_ = h.file.Close()
			if h.cutterKey != "" && v.slicer != nil {
				v.slicer.Release(h.cutterKey)
			}
		}
		h.mu.Unlock()
	}

	return 0
}

func (v *VFS) Destroy() {
	if v.slicer != nil {
		_ = v.slicer.Close()
	}
}
