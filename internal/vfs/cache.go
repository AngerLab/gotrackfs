package vfs

import (
	"log/slog"
	"os"
	"sync"

	"golang.org/x/text/unicode/norm"
)

// AlbumCache caches parsed directory states to prevent re-parsing on every FUSE call.
type AlbumCache struct {
	mu     sync.RWMutex
	dirs   map[string]*cachedDir
	logger *slog.Logger
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
func (c *AlbumCache) GetDirState(dirPath string) (*DirState, error) {
	dirPath = norm.NFC.String(dirPath)
	dirFi, err := os.Stat(dirPath)
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	cached, ok := c.dirs[dirPath]
	c.mu.RUnlock()

	if ok && cached != nil && cached.facts != nil && cached.facts.dirModTime.Equal(dirFi.ModTime()) {
		if cached.state == nil && len(cached.facts.cueModTimes) == 0 {
			// Directory has no CUE files and directory contents have not changed.
			return nil, nil
		}
		if isFilesCacheValid(cached.facts) {
			return cached.state, nil
		}
	}

	state, facts, err := buildDirState(dirPath, dirFi, c.logger)
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
}

// isFilesCacheValid checks if known CUE and audio files still match their cached mtime and size.
func isFilesCacheValid(facts *dirFacts) bool {
	for cp, oldMtime := range facts.cueModTimes {
		fi, err := os.Stat(cp)
		if err != nil || !fi.ModTime().Equal(oldMtime) || fi.Size() != facts.cueSizes[cp] {
			return false
		}
	}
	for audioPath, oldMtime := range facts.audioModTimes {
		fi, err := os.Stat(audioPath)
		if err != nil || !fi.ModTime().Equal(oldMtime) || fi.Size() != facts.audioSizes[audioPath] {
			return false
		}
	}
	return true
}
