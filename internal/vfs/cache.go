package vfs

import (
	"log/slog"
	"sync"

	"github.com/AngerLab/gotrackfs/internal/track"

	"golang.org/x/sync/singleflight"
	"golang.org/x/text/unicode/norm"
)

// AlbumCache caches parsed directory states to prevent re-parsing on every FUSE call.
type AlbumCache struct {
	mu     sync.RWMutex
	dirs   map[string]*cachedDir
	sfg    singleflight.Group
	logger *slog.Logger

	// maxQuality caps the output format of sliced tracks (zero = keep source).
	// Set once at mount time via vfs.New before any lookup.
	maxQuality track.Quality
}

type cachedDir struct {
	facts *dirFacts
	state *DirState
}

// NewAlbumCache creates a new empty AlbumCache with structured logging.
func NewAlbumCache(logger *slog.Logger) *AlbumCache {
	if logger == nil {
		logger = slog.Default()
	}
	return &AlbumCache{
		dirs:   make(map[string]*cachedDir),
		logger: logger,
	}
}

// GetDirState returns the DirState for dirPath, or parses it if needed.
// Compares dirModTime to invalidate cache without stat-ing individual files,
// and uses singleflight to ensure concurrent callers for the same directory share a single parse operation.
func (c *AlbumCache) GetDirState(dirPath string) (*DirState, error) {
	dirPath = norm.NFC.String(dirPath)

	dirFi, err := statPath(dirPath)
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	cached, ok := c.dirs[dirPath]
	c.mu.RUnlock()

	// If directory mtime hasn't changed, return cached state without re-parsing or stat-ing individual files.
	if ok && cached != nil && cached.facts != nil && cached.facts.dirModTime.Equal(dirFi.ModTime()) {
		return cached.state, nil
	}

	// Stampede elimination: parse using singleflight so only one goroutine builds state.
	res, err, _ := c.sfg.Do(dirPath, func() (any, error) {
		// Re-check cache under read lock (another singleflight runner might have just finished)
		c.mu.RLock()
		cached, ok := c.dirs[dirPath]
		c.mu.RUnlock()
		if ok && cached != nil && cached.facts != nil && cached.facts.dirModTime.Equal(dirFi.ModTime()) {
			return cached.state, nil
		}

		state, facts, err := buildDirState(dirPath, dirFi, c.logger, c.maxQuality)
		if err != nil {
			return nil, err
		}

		c.mu.Lock()
		c.dirs[dirPath] = &cachedDir{
			facts: facts,
			state: state,
		}
		c.mu.Unlock()

		return state, nil
	})

	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return res.(*DirState), nil
}
