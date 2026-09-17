package cutter

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Options holds configuration for TrackCacheManager.
type Options struct {
	Cutter  Cutter
	TempDir string        // If empty, creates a subdirectory in os.TempDir()
	TTL     time.Duration // Time-to-live after refCount reaches 0. Default: 60s
	Logger  *slog.Logger
}

type trackEntry struct {
	path     string
	done     chan struct{}
	err      error
	refCount int
	lastUsed time.Time
	timer    *time.Timer
}

// TrackCacheManager manages cached slices of monolithic audio tracks with reference counting and TTL cleanup.
type TrackCacheManager struct {
	cutter  Cutter
	tempDir string
	ttl     time.Duration
	logger  *slog.Logger

	mu      sync.Mutex
	entries map[string]*trackEntry
	closed  bool
}

// NewManager creates and initializes a new TrackCacheManager.
func NewManager(opts Options) (*TrackCacheManager, error) {
	tempDir := opts.TempDir
	if tempDir == "" {
		var err error
		tempDir, err = os.MkdirTemp("", "gotrackfs-*")
		if err != nil {
			return nil, fmt.Errorf("create temp dir for cutter: %w", err)
		}
	} else {
		if err := os.MkdirAll(tempDir, 0755); err != nil {
			return nil, fmt.Errorf("create cutter temp dir %s: %w", tempDir, err)
		}
	}

	ttl := opts.TTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &TrackCacheManager{
		cutter:  opts.Cutter,
		tempDir: tempDir,
		ttl:     ttl,
		logger:  logger,
		entries: make(map[string]*trackEntry),
	}, nil
}

// TempDir returns the root temporary directory used for cached tracks.
func (m *TrackCacheManager) TempDir() string {
	return m.tempDir
}

// GetExisting returns the exact byte size of a cached track if it is already sliced and available on disk.
func (m *TrackCacheManager) GetExisting(key string) (int64, bool) {
	m.mu.Lock()
	entry, ok := m.entries[key]
	m.mu.Unlock()

	if !ok {
		return 0, false
	}

	select {
	case <-entry.done:
		if entry.err != nil {
			return 0, false
		}
		fi, err := os.Stat(entry.path)
		if err != nil {
			return 0, false
		}
		return fi.Size(), true
	default:
		return 0, false
	}
}

// Acquire requests a track slice with the given cache key. If the track is already cached
// or currently being cut, it waits for completion and increments the reference count.
func (m *TrackCacheManager) Acquire(ctx context.Context, key string, req TrackRequest) (string, error) {
	if key == "" {
		key = req.Key()
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", fmt.Errorf("cutter cache manager is closed")
	}

	entry, exists := m.entries[key]
	if exists {
		if entry.timer != nil {
			entry.timer.Stop()
			entry.timer = nil
		}
		entry.refCount++
		m.mu.Unlock()

		select {
		case <-entry.done:
		case <-ctx.Done():
			m.Release(key)
			return "", ctx.Err()
		}

		if entry.err != nil {
			m.Release(key)
			return "", entry.err
		}
		return entry.path, nil
	}

	outputPath := filepath.Join(m.tempDir, fmt.Sprintf("%s.flac", key))
	entry = &trackEntry{
		path:     outputPath,
		done:     make(chan struct{}),
		refCount: 1,
		lastUsed: time.Now(),
	}
	m.entries[key] = entry
	m.mu.Unlock()

	// Execute cutter without holding manager lock
	err := m.cutter.Cut(ctx, req, outputPath)

	m.mu.Lock()
	entry.err = err
	close(entry.done)

	if err != nil {
		delete(m.entries, key)
		_ = os.Remove(outputPath)
		m.mu.Unlock()
		return "", err
	}
	m.mu.Unlock()

	return outputPath, nil
}

// Release decrements the reference count for a track key. When the count drops to 0,
// a TTL timer is started to clean up the temporary file.
func (m *TrackCacheManager) Release(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[key]
	if !ok {
		return
	}

	entry.refCount--
	if entry.refCount <= 0 {
		entry.refCount = 0
		entry.lastUsed = time.Now()

		entry.timer = time.AfterFunc(m.ttl, func() {
			m.mu.Lock()
			defer m.mu.Unlock()

			if e, stillExists := m.entries[key]; stillExists && e.refCount == 0 {
				delete(m.entries, key)
				_ = os.Remove(e.path)
				m.logger.Debug("cutter: removed expired cached track", "key", key, "path", e.path)
			}
		})
	}
}

// Close terminates the manager, stops all timers, and removes all temporary files.
func (m *TrackCacheManager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	for _, entry := range m.entries {
		if entry.timer != nil {
			entry.timer.Stop()
		}
	}
	m.entries = make(map[string]*trackEntry)
	m.mu.Unlock()

	return os.RemoveAll(m.tempDir)
}
