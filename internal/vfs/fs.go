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

	"github.com/AngerLab/gotrackfs/internal/cutter"
	"github.com/AngerLab/gotrackfs/internal/track"

	"github.com/cespare/xxhash/v2"
	"github.com/winfsp/cgofuse/fuse"
	"golang.org/x/text/unicode/norm"
)

// inodeFromPath deterministically generates a unique, stable 64-bit inode number from a clean VFS path.
// Following restic conventions: root is 1, 0 is invalid, and hashes < 2 are remapped to >= 2.
func inodeFromPath(cleanPath string) uint64 {
	if cleanPath == "/" || cleanPath == "." || cleanPath == "" {
		return 1
	}
	ino := xxhash.Sum64String(cleanPath)
	if ino < 2 {
		ino += 2
	}
	return ino
}

// Options holds configuration options for the VFS filesystem.
type Options struct {
	SourceRoot string
	KeepAlbum  bool                      // If true, keep monolithic audio files visible alongside virtual tracks
	Logger     *slog.Logger              // Optional structured logger. If nil, a default text logger is used.
	Slicer     *cutter.TrackCacheManager // Optional audio slicer. If nil, virtual track playback is disabled (Open returns ENOSYS).

	// MaxQuality optionally caps the output format of sliced tracks.
	// Sources strictly above the cap in bit depth or sample rate are lowered
	// to it; sources at or below the cap keep their original format.
	// Zero Quality (default) preserves the source format for every track.
	MaxQuality track.Quality
}

// EnsureDefaults fills in zero-value fields with production-ready defaults.
func (o *Options) EnsureDefaults() {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	if abs, err := filepath.Abs(o.SourceRoot); err == nil {
		o.SourceRoot = abs
	}
}

// VFS implements fuse.FileSystemInterface using a path-based routing model.
type VFS struct {
	fuse.FileSystemBase
	sourceRoot string
	keepAlbum  bool
	logger     *slog.Logger
	cache      *AlbumCache
	slicer     *cutter.TrackCacheManager
	ctx        context.Context
	cancel     context.CancelFunc

	mu         sync.Mutex
	openFiles  map[uint64]*fileHandle
	nextHandle uint64
}

// New creates a new VFS instance.
func New(opts Options) *VFS {
	opts.EnsureDefaults()

	ctx, cancel := context.WithCancel(context.Background())
	cache := NewAlbumCache(opts.Logger)
	cache.maxQuality = opts.MaxQuality
	return &VFS{
		sourceRoot: opts.SourceRoot,
		keepAlbum:  opts.KeepAlbum,
		logger:     opts.Logger,
		cache:      cache,
		slicer:     opts.Slicer,
		ctx:        ctx,
		cancel:     cancel,
		openFiles:  make(map[uint64]*fileHandle),
	}
}

// nodeKind classifies what a resolved path refers to. The flags previously
// carried separately (isVirtualDir, isHidden, track != nil) are mutually
// exclusive, so they are merged into one switchable kind.
type nodeKind int

const (
	nodeKindReal       nodeKind = iota // plain real file/dir (or mirrored artwork path)
	nodeKindVirtualDir                 // virtual album subdirectory (e.g. CD1)
	nodeKindTrack                      // virtual sliced track
	nodeKindHidden                     // hidden monolith: must look absent
)

type resolvedNode struct {
	kind        nodeKind
	realPath    string
	track       *VirtualTrack
	subDirState *DirState
}

func cleanNormPath(path string) string {
	return norm.NFC.String(filepath.Clean(path))
}

func (v *VFS) resolve(path, op string) resolvedNode {
	cleanPath := cleanNormPath(path)
	realPath := filepath.Join(v.sourceRoot, cleanPath)
	if cleanPath == "/" || cleanPath == "." {
		return resolvedNode{kind: nodeKindReal, realPath: v.sourceRoot}
	}

	base := filepath.Base(cleanPath)
	dir := filepath.Dir(cleanPath)

	// 1. The directory itself has cached state (real dir with virtual content,
	//    or a real dir without any): classify base against it.
	if dirState := v.dirStateFor(dir, cleanPath, op); dirState != nil {
		return v.resolveInDirState(dirState, base, realPath)
	}

	// 2. dir may be a virtual subdirectory inside a real parent
	//    (e.g. /TheWall/CD1/01. In the Flesh.flac).
	if parentState := v.dirStateFor(filepath.Dir(dir), cleanPath, op); parentState != nil {
		if subState, ok := parentState.Subdirs[filepath.Base(dir)]; ok {
			return v.resolveInSubDir(subState, base, realPath)
		}
	}

	// 3. Fallback: plain real path.
	return resolvedNode{kind: nodeKindReal, realPath: realPath}
}

// dirStateFor returns the cached directory state for a virtual path component,
// logging cache errors (except plain not-exist) against the operation.
func (v *VFS) dirStateFor(dir, cleanPath, op string) *DirState {
	realDir := filepath.Join(v.sourceRoot, dir)
	dirState, err := v.cache.GetDirState(realDir)
	if err != nil && !os.IsNotExist(err) {
		v.logger.Debug("cache: failed to get dir state", "op", op, "dir", realDir, "path", cleanPath, "error", err)
	}
	return dirState
}

// resolveInDirState classifies base against the cached state of its directory:
// a virtual subdirectory, a hidden monolith, a virtual track, or a real entry.
func (v *VFS) resolveInDirState(dirState *DirState, base, realPath string) resolvedNode {
	if subState, ok := dirState.Subdirs[base]; ok {
		return resolvedNode{kind: nodeKindVirtualDir, realPath: realPath, subDirState: subState}
	}
	if !v.keepAlbum && dirState.HiddenMonoliths[base] {
		return resolvedNode{kind: nodeKindHidden, realPath: realPath}
	}
	if vt, ok := dirState.TracksByName[base]; ok {
		return resolvedNode{kind: nodeKindTrack, realPath: realPath, track: vt}
	}
	// Otherwise it is a real file/dir in dir.
	return resolvedNode{kind: nodeKindReal, realPath: realPath}
}

// resolveInSubDir classifies base inside a virtual subdirectory: a virtual
// track, a mirrored artwork file, or nothing (falls back to the real path,
// which simply does not exist under the virtual subdir).
func (v *VFS) resolveInSubDir(subState *DirState, base, realPath string) resolvedNode {
	if vt, ok := subState.TracksByName[base]; ok {
		return resolvedNode{kind: nodeKindTrack, realPath: realPath, track: vt}
	}
	if mirrorPath, ok := subState.MirroredFiles[base]; ok {
		return resolvedNode{kind: nodeKindReal, realPath: mirrorPath}
	}
	return resolvedNode{kind: nodeKindReal, realPath: realPath}
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
	defer func() {
		stat.Ino = inodeFromPath(cleanPath)
	}()
	if cleanPath == "/" || cleanPath == "." {
		return v.statRealPath(v.sourceRoot, stat)
	}

	node := v.resolve(cleanPath, "Getattr")
	switch node.kind {
	case nodeKindHidden:
		return -fuse.ENOENT

	case nodeKindVirtualDir:
		parentRealDir := filepath.Dir(node.realPath)
		var st syscall.Stat_t
		if err := lstatSyscall(parentRealDir, &st); err != nil {
			return -fuse.ENOENT
		}
		copyStat(stat, &st)
		stat.Mode = syscall.S_IFDIR | 0555
		return 0

	case nodeKindTrack:
		// Virtual track entry
		var st syscall.Stat_t
		if err := lstatSyscall(node.track.Slice.SourceAudioPath, &st); err != nil {
			return -fuse.ENOENT
		}

		copyStat(stat, &st)
		size := node.track.EstimatedSize

		// 1. If a valid matching file handle is open, use exact file size from open descriptor
		var foundExact bool
		if fh != 0 && fh != ^uint64(0) {
			v.mu.Lock()
			h, ok := v.openFiles[fh]
			v.mu.Unlock()
			if ok && h != nil && h.cutterKey == node.track.CutterKey {
				if fi, err := h.file.Stat(); err == nil {
					size = fi.Size()
					foundExact = true
				}
			}
		}

		// 2. If fh didn't yield exact size (e.g. fh was 0, invalid, different track, or released), check cache
		if !foundExact && v.slicer != nil {
			if exactSize, ok := v.slicer.GetExisting(node.track.CutterKey); ok {
				size = exactSize
			}
		}

		stat.Size = size
		stat.Mode = syscall.S_IFREG | 0444
		return 0

	default:
		// Real file / directory (or mirrored file).
		return v.statRealPath(node.realPath, stat)
	}
}

func (v *VFS) statRealPath(realPath string, stat *fuse.Stat_t) int {
	var st syscall.Stat_t
	if err := lstatSyscall(realPath, &st); err != nil {
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
	switch node.kind {
	case nodeKindHidden:
		return -fuse.ENOENT, ^uint64(0)
	case nodeKindVirtualDir:
		return 0, 0
	case nodeKindTrack:
		return -fuse.ENOTDIR, ^uint64(0)
	}

	var st syscall.Stat_t
	if err := lstatSyscall(node.realPath, &st); err != nil {
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
		switch node.kind {
		case nodeKindHidden:
			return -fuse.ENOENT
		case nodeKindTrack:
			return -fuse.ENOTDIR
		case nodeKindVirtualDir:
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
	entries, err := readDir(realDir)
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

	node := v.resolve(cleanNormPath(path), "Open")
	switch node.kind {
	case nodeKindHidden:
		return -fuse.ENOENT, ^uint64(0)
	case nodeKindVirtualDir:
		return -fuse.EISDIR, ^uint64(0)
	case nodeKindTrack:
		return v.openTrack(node)
	default:
		return v.openReal(node.realPath)
	}
}

// openTrack slices the virtual audio track on demand and registers an open handle.
// Returns ENOSYS if no slicer is configured, EIO on slicing errors, ENODEV if the
// filesystem is being torn down.
func (v *VFS) openTrack(node resolvedNode) (int, uint64) {
	if v.slicer == nil {
		v.logger.Warn("vfs: cannot slice audio track: no audio slicer configured (is ffmpeg installed?)", "track", node.track.FileName)
		return -fuse.ENOSYS, ^uint64(0)
	}

	tempPath, err := v.slicer.Acquire(v.ctx, node.track.CutterKey, node.track.Slice)
	if err != nil {
		// During teardown Acquire fails with "cutter cache manager is closed"
		// (or ctx cancellation); logging those at Error level only adds noise
		// while everything is shutting down anyway.
		if v.ctx.Err() == nil && err != context.Canceled {
			v.logger.Error("vfs: failed to slice audio track", "track", node.track.FileName, "error", err)
		}
		return -fuse.EIO, ^uint64(0)
	}

	f, err := os.Open(tempPath)
	if err != nil {
		v.slicer.Release(node.track.CutterKey)
		v.logger.Error("vfs: failed to open sliced audio temp file", "path", tempPath, "error", err)
		return -fuse.EIO, ^uint64(0)
	}

	return v.registerHandle(f, node.track.CutterKey)
}

// openReal opens a plain file from the source tree, returning ENOENT/EACCES/EISDIR as appropriate.
// The NFC/NFD retry comes from openRealFile's normalization fallback.
func (v *VFS) openReal(realPath string) (int, uint64) {
	f, err := openRealFile(realPath)
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

	return v.registerHandle(f, "")
}

type fileHandle struct {
	// file is written exactly once, under v.mu, before the handle is
	// published in openFiles; Release deletes the entry under v.mu and
	// closes the file outside the lock. The deletion only keeps the handle
	// out of reach for new calls: a Read that already fetched the handle
	// can still be inside ReadAt while Close runs, and that error is
	// surfaced as EIO by the Read mapping — not EBADF like the pre-refactor
	// atomic.Swap guard. Callers must not assume a released handle is idle.
	file      *os.File
	cutterKey string // non-empty if acquired via cutter
}

// registerHandle assigns the next handle id to f and stores it in the open-files table.
// Sliced-track handles carry their cutter key so Release can decrement the
// refcount; once the filesystem context is cancelled these are refused with
// ENODEV and the lease is returned to the slicer. Plain file handles are still
// registered during shutdown — the handle table remains usable for them.
func (v *VFS) registerHandle(f *os.File, cutterKey string) (int, uint64) {
	v.mu.Lock()

	if v.ctx.Err() != nil && cutterKey != "" {
		v.mu.Unlock()
		_ = f.Close()
		v.slicer.Release(cutterKey)
		return -fuse.ENODEV, ^uint64(0)
	}
	v.nextHandle++
	fh := v.nextHandle

	v.openFiles[fh] = &fileHandle{file: f, cutterKey: cutterKey}

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

	if ok && h != nil && h.file != nil {
		_ = h.file.Close()
		if h.cutterKey != "" && v.slicer != nil {
			v.slicer.Release(h.cutterKey)
		}
	}

	return 0
}

func (v *VFS) Destroy() {
	if v.cancel != nil {
		v.cancel()
	}

	v.mu.Lock()
	for fh, h := range v.openFiles {
		delete(v.openFiles, fh)
		if h != nil && h.file != nil {
			_ = h.file.Close()
		}
	}
	v.mu.Unlock()

	if v.slicer != nil {
		_ = v.slicer.Close()
	}
}
