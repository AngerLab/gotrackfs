package cutter

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AngerLab/gotrackfs/internal/track"
)

// Options holds configuration for TrackCacheManager.
type Options struct {
	Cutter  Cutter
	TempDir string        // If empty, a subdirectory in os.TempDir() is created at construction time
	TTL     time.Duration // Time-to-live after refCount reaches 0. Default: 60s
	Logger  *slog.Logger
}

// EnsureDefaults fills in zero-value fields with production-ready defaults.
func (o *Options) EnsureDefaults() {
	if o.TTL <= 0 {
		o.TTL = 60 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

type trackEntry struct {
	path          string
	done          chan struct{}
	err           error
	activeWaiters int
	refCount      int
	lastUsed      time.Time
	timer         *time.Timer
	cancelCut     context.CancelFunc
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
	opts.EnsureDefaults()

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

	return &TrackCacheManager{
		cutter:  opts.Cutter,
		tempDir: tempDir,
		ttl:     opts.TTL,
		logger:  opts.Logger,
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
// If all waiting callers cancel their context, the underlying cut operation is aborted.
func (m *TrackCacheManager) Acquire(ctx context.Context, key string, req track.Slice) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if key == "" {
		key = req.Key()
	}

	entry, isReady, err := m.getOrCreateEntry(key, req)
	if err != nil {
		return "", err
	}
	if isReady {
		return entry.path, nil
	}

	return m.waitForEntry(ctx, key, entry)
}

func (m *TrackCacheManager) getOrCreateEntry(key string, req track.Slice) (*trackEntry, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, false, fmt.Errorf("cutter cache manager is closed")
	}

	entry, exists := m.entries[key]
	if !exists {
		outputPath := filepath.Join(m.tempDir, fmt.Sprintf("%s.flac", key))
		cutCtx, cutCancel := context.WithCancel(context.Background())
		entry = &trackEntry{
			path:          outputPath,
			done:          make(chan struct{}),
			activeWaiters: 1,
			cancelCut:     cutCancel,
		}
		m.entries[key] = entry
		go m.runCut(cutCtx, key, req, entry, outputPath)
		return entry, false, nil
	}

	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}

	select {
	case <-entry.done:
		if entry.err != nil {
			return nil, false, entry.err
		}
		entry.refCount++
		return entry, true, nil
	default:
		entry.activeWaiters++
		return entry, false, nil
	}
}

func (m *TrackCacheManager) runCut(ctx context.Context, key string, req track.Slice, entry *trackEntry, outputPath string) {
	err := m.cutter.Cut(ctx, req, outputPath)

	m.mu.Lock()
	defer m.mu.Unlock()

	entry.err = err
	if err != nil {
		delete(m.entries, key)
		_ = os.Remove(outputPath)
	} else if entry.refCount == 0 && entry.activeWaiters <= 0 {
		m.scheduleTTLTimerLocked(key, entry)
	}
	close(entry.done)
}

func (m *TrackCacheManager) waitForEntry(ctx context.Context, key string, entry *trackEntry) (string, error) {
	select {
	case <-entry.done:
		m.mu.Lock()
		defer m.mu.Unlock()

		entry.activeWaiters--
		if entry.err != nil {
			return "", entry.err
		}
		if entry.timer != nil {
			entry.timer.Stop()
			entry.timer = nil
		}
		entry.refCount++
		return entry.path, nil

	case <-ctx.Done():
		m.mu.Lock()
		defer m.mu.Unlock()

		m.cancelWaiterLocked(key, entry)
		return "", ctx.Err()
	}
}

func (m *TrackCacheManager) cancelWaiterLocked(key string, entry *trackEntry) {
	entry.activeWaiters--
	if entry.activeWaiters <= 0 {
		if entry.cancelCut != nil {
			entry.cancelCut()
			entry.cancelCut = nil
		}
		select {
		case <-entry.done:
			if entry.err == nil && entry.refCount == 0 {
				m.scheduleTTLTimerLocked(key, entry)
			}
		default:
		}
	}
}

func (m *TrackCacheManager) scheduleTTLTimerLocked(key string, entry *trackEntry) {
	if entry.timer != nil {
		return
	}
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
		m.scheduleTTLTimerLocked(key, entry)
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
			entry.timer = nil
		}
		if entry.cancelCut != nil {
			entry.cancelCut()
			entry.cancelCut = nil
		}
	}
	m.entries = make(map[string]*trackEntry)
	m.mu.Unlock()

	return os.RemoveAll(m.tempDir)
}
