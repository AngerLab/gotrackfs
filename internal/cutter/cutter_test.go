package cutter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AngerLab/gotrackfs/internal/track"
)

type mockCutter struct {
	cutCount int32
	delay    time.Duration
	fail     bool
}

func (m *mockCutter) Cut(ctx context.Context, req track.Slice, outputPath string) error {
	atomic.AddInt32(&m.cutCount, 1)
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// Write dummy sliced file
	return os.WriteFile(outputPath, []byte("SLICED_AUDIO_DATA"), 0644)
}

func TestFFmpegCutter_BuildArgs(t *testing.T) {
	cutter := &FFmpegCutter{binPath: "ffmpeg"}

	req := track.Slice{
		SourceAudioPath: "/music/album.flac",
		Start:           65.5,
		End:             180.25,
		ArtworkPath:     "/music/cover.jpg",
		Tags: map[string]string{
			"title":        "Time",
			"artist":       "Pink Floyd",
			"album_artist": "Pink Floyd",
			"album":        "The Dark Side of the Moon",
			"track":        "4",
			"date":         "1973",
			"genre":        "Progressive Rock",
			"disc":         "1",
		},
	}

	args := cutter.BuildArgs(req, "/tmp/output.flac")

	assertArgContains := func(arg string) {
		t.Helper()
		if !slices.Contains(args, arg) {
			t.Errorf("expected args to contain %q, got: %v", arg, args)
		}
	}

	assertArgContains("-y")
	assertArgContains("-ss")
	assertArgContains("65.5000")
	assertArgContains("-t")
	assertArgContains("114.7500") // 180.25 - 65.5 = 114.75
	assertArgContains("/music/album.flac")
	assertArgContains("/music/cover.jpg")
	assertArgContains("attached_pic")
	assertArgContains("-c:a")
	assertArgContains("flac")
	assertArgContains("-compression_level")
	assertArgContains("1")
	assertArgContains("-metadata")
	assertArgContains("title=Time")
	assertArgContains("track=4")
	assertArgContains("date=1973")
	assertArgContains("/tmp/output.flac")
}

func TestTrackCacheManager_LifecycleAndThunderingHerd(t *testing.T) {
	mock := &mockCutter{delay: 20 * time.Millisecond}
	mgr, err := NewManager(Options{
		Cutter: mock,
		TTL:    50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer mgr.Close()

	req := track.Slice{
		SourceAudioPath: "/music/test.flac",
		Start:           0,
		End:             10,
		Tags:            map[string]string{"title": "Track 1"},
	}

	// Test concurrent Acquire (Thundering herd)
	const concurrentCallers = 10
	var wg sync.WaitGroup
	paths := make([]string, concurrentCallers)
	errs := make([]error, concurrentCallers)

	for i := 0; i < concurrentCallers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			path, err := mgr.Acquire(context.Background(), req.Key(), req)
			paths[idx] = path
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i := 0; i < concurrentCallers; i++ {
		if errs[i] != nil {
			t.Errorf("caller %d failed: %v", i, errs[i])
		}
		if paths[i] != paths[0] {
			t.Errorf("caller %d got different path %q vs %q", i, paths[i], paths[0])
		}
	}

	// mock cutter must have been called EXACTLY ONCE
	if count := atomic.LoadInt32(&mock.cutCount); count != 1 {
		t.Errorf("expected cutter to be called 1 time, called %d times", count)
	}

	// Verify file was created
	content, err := os.ReadFile(paths[0])
	if err != nil || string(content) != "SLICED_AUDIO_DATA" {
		t.Errorf("expected 'SLICED_AUDIO_DATA', got %q (err: %v)", string(content), err)
	}

	// GetExisting must report true and non-zero size
	size, ok := mgr.GetExisting(req.Key())
	if !ok || size != int64(len("SLICED_AUDIO_DATA")) {
		t.Errorf("GetExisting expected size %d, got %d (ok=%v)", len("SLICED_AUDIO_DATA"), size, ok)
	}

	// Release 9 callers, 1 remains
	for i := 0; i < concurrentCallers-1; i++ {
		mgr.Release(req.Key())
	}

	// Wait 70ms (greater than TTL 50ms) - file must STILL exist because refCount == 1!
	time.Sleep(70 * time.Millisecond)
	if _, err := os.Stat(paths[0]); os.IsNotExist(err) {
		t.Fatalf("file deleted while refCount > 0!")
	}

	// Release last caller
	mgr.Release(req.Key())

	// Wait for TTL (50ms) to expire + margin
	time.Sleep(80 * time.Millisecond)

	// File should now be deleted from disk
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted after TTL, but it still exists")
	}

	// Key should be gone from cache
	if _, ok := mgr.GetExisting(req.Key()); ok {
		t.Errorf("expected key to be removed from manager after TTL expiration")
	}
}

func TestTrackCacheManager_CloseCleansUpTempDir(t *testing.T) {
	mock := &mockCutter{}
	mgr, err := NewManager(Options{
		Cutter: mock,
		TTL:    1 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := track.Slice{
		SourceAudioPath: "/music/test.flac",
		Start:           0,
		End:             10,
	}

	tempDir := mgr.TempDir()
	path, err := mgr.Acquire(context.Background(), req.Key(), req)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not found: %v", err)
	}

	// Close manager
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// TempDir should be gone
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Errorf("expected tempDir %s to be removed on Close", tempDir)
	}
}

func TestTrackSlice_KeyDerivesFromSourceFacts(t *testing.T) {
	now := time.Now()
	baseReq := track.Slice{
		SourceAudioPath: "/music/album.flac",
		SourceModTime:   now,
		SourceSize:      50 * 1024 * 1024,
		Start:           0,
		End:             180,
		ArtworkPath:     "/music/cover.jpg",
		Tags: map[string]string{
			"title":        "Track 1",
			"artist":       "Artist 1",
			"album_artist": "Album Artist 1",
			"album":        "Album 1",
			"track":        "1",
			"date":         "2020",
			"genre":        "Rock",
			"disc":         "1",
		},
	}

	key1 := baseReq.Key()

	// Identical request produces identical key
	identicalReq := baseReq
	if identicalReq.Key() != key1 {
		t.Errorf("expected identical key for identical request")
	}

	tests := []struct {
		name   string
		modify func(r *track.Slice)
	}{
		{"SourceModTime", func(r *track.Slice) { r.SourceModTime = now.Add(5 * time.Second) }},
		{"SourceSize", func(r *track.Slice) { r.SourceSize = 51 * 1024 * 1024 }},
		{"SourceAudioPath", func(r *track.Slice) { r.SourceAudioPath = "/music/other.flac" }},
		{"Start", func(r *track.Slice) { r.Start = 1.0 }},
		{"End", func(r *track.Slice) { r.End = 200.0 }},
		{"ArtworkPath", func(r *track.Slice) { r.ArtworkPath = "/music/other.jpg" }},
		{"Title", func(r *track.Slice) {
			r.Tags = make(map[string]string)
			for k, v := range baseReq.Tags {
				r.Tags[k] = v
			}
			r.Tags["title"] = "Track 2"
		}},
		{"Artist", func(r *track.Slice) {
			r.Tags = make(map[string]string)
			for k, v := range baseReq.Tags {
				r.Tags[k] = v
			}
			r.Tags["artist"] = "Artist 2"
		}},
		{"NewTagComposer", func(r *track.Slice) {
			r.Tags = make(map[string]string)
			for k, v := range baseReq.Tags {
				r.Tags[k] = v
			}
			r.Tags["composer"] = "Bach"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modReq := baseReq
			tt.modify(&modReq)
			if modReq.Key() == key1 {
				t.Errorf("expected key to change when %s changes", tt.name)
			}
		})
	}
}

func TestFFmpegCutter_GracefulDegradationOnCorruptArtwork(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed, skipping test")
	}

	tmpDir := t.TempDir()
	sourceAudio := filepath.Join(tmpDir, "source.flac")
	corruptCover := filepath.Join(tmpDir, "cover.jpg")
	outputAudio := filepath.Join(tmpDir, "output.flac")

	// 1. Generate 1 second valid FLAC using ffmpeg
	cmd := exec.Command(ffmpegPath, "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "flac", sourceAudio)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to generate test flac: %v (out: %s)", err, out)
	}

	// 2. Create corrupt artwork (fake text file named .jpg)
	if err := os.WriteFile(corruptCover, []byte("NOT_A_VALID_IMAGE"), 0644); err != nil {
		t.Fatal(err)
	}

	cutter, err := NewFFmpegCutter(ffmpegPath, nil)
	if err != nil {
		t.Fatal(err)
	}

	req := track.Slice{
		SourceAudioPath: sourceAudio,
		Start:           0,
		End:             0.5,
		ArtworkPath:     corruptCover,
		Tags:            map[string]string{"title": "Graceful Track"},
	}

	// 3. Cut must NOT fail! It should log a warning and succeed without artwork!
	if err := cutter.Cut(context.Background(), req, outputAudio); err != nil {
		t.Fatalf("expected Cut to succeed via graceful fallback, got: %v", err)
	}

	// Verify output exists and is non-empty
	fi, err := os.Stat(outputAudio)
	if err != nil {
		t.Fatalf("output file stat failed: %v", err)
	}
	if fi.Size() == 0 {
		t.Errorf("expected non-empty output file")
	}
}

func TestFFmpegCutter_ConcurrencyAndTimeout(t *testing.T) {
	cutter, err := NewFFmpegCutter("", nil)
	if err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}

	// Verify defaults
	if cap(cutter.sem) != 2 {
		t.Errorf("expected default sem cap 2, got %d", cap(cutter.sem))
	}
	if cutter.timeout != 60*time.Second {
		t.Errorf("expected default timeout 60s, got %v", cutter.timeout)
	}

	// Test SetMaxConcurrency
	cutter.SetMaxConcurrency(4)
	if cap(cutter.sem) != 4 {
		t.Errorf("expected sem cap 4, got %d", cap(cutter.sem))
	}

	// Test timeout expiration on cancelled context
	cutter.SetMaxConcurrency(1)
	// Fill the slot
	cutter.sem <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err = cutter.Cut(ctx, track.Slice{}, "/tmp/unused")
	if err == nil {
		t.Errorf("expected error due to timeout waiting for slot, got nil")
	}
	// Release slot
	<-cutter.sem
}

func TestTrackCacheManager_CancellationWhenAllCallersCancel(t *testing.T) {
	mock := &mockCutter{delay: 100 * time.Millisecond}
	mgr, err := NewManager(Options{
		Cutter: mock,
		TTL:    1 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer mgr.Close()

	req := track.Slice{
		SourceAudioPath: "/music/test.flac",
		Start:           0,
		End:             10,
		Tags:            map[string]string{"title": "Cancel Track"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	outputPath, err := mgr.Acquire(ctx, req.Key(), req)
	if err == nil {
		t.Fatalf("expected error from cancelled context, got path %s", outputPath)
	}
	if err != context.DeadlineExceeded && err != context.Canceled {
		t.Errorf("expected context cancellation error, got %v", err)
	}

	// Give the mock goroutine a moment to observe the cancellation and clean up
	time.Sleep(30 * time.Millisecond)

	expectedPath := filepath.Join(mgr.TempDir(), req.Key()+".flac")
	if _, err := os.Stat(expectedPath); !os.IsNotExist(err) {
		t.Errorf("expected temp file to be removed upon cancellation, but it exists: %s", expectedPath)
	}
}

func TestTrackCacheManager_PartialCancellation(t *testing.T) {
	mock := &mockCutter{delay: 60 * time.Millisecond}
	mgr, err := NewManager(Options{
		Cutter: mock,
		TTL:    1 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer mgr.Close()

	req := track.Slice{
		SourceAudioPath: "/music/test.flac",
		Start:           0,
		End:             10,
		Tags:            map[string]string{"title": "Partial Cancel Track"},
	}

	// Caller 1 cancels early
	ctx1, cancel1 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel1()

	// Caller 2 waits for full completion
	ctx2 := context.Background()

	var wg sync.WaitGroup
	var path1, path2 string
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		path1, err1 = mgr.Acquire(ctx1, req.Key(), req)
	}()

	// Slight offset so caller 1 initializes the cut
	time.Sleep(5 * time.Millisecond)

	go func() {
		defer wg.Done()
		path2, err2 = mgr.Acquire(ctx2, req.Key(), req)
	}()

	wg.Wait()

	if err1 == nil {
		t.Errorf("caller 1 should have failed due to timeout, got path %s", path1)
	}
	if err2 != nil {
		t.Errorf("caller 2 should have succeeded despite caller 1 cancelling, got err: %v", err2)
	}
	if path2 == "" {
		t.Errorf("caller 2 should have received a valid path")
	}

	mgr.Release(req.Key())
}

type uncancelableSuccessCutter struct {
	delay time.Duration
}

func (u *uncancelableSuccessCutter) Cut(ctx context.Context, req track.Slice, outputPath string) error {
	time.Sleep(u.delay)
	return os.WriteFile(outputPath, []byte("SUCCESS_DATA"), 0644)
}

func TestTrackCacheManager_CutSucceedsWhenNoWaitersScheduledTTL(t *testing.T) {
	mock := &uncancelableSuccessCutter{delay: 40 * time.Millisecond}
	mgr, err := NewManager(Options{
		Cutter: mock,
		TTL:    50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer mgr.Close()

	req := track.Slice{
		SourceAudioPath: "/music/orphan.flac",
		Start:           0,
		End:             10,
		Tags:            map[string]string{"title": "Orphan Test"},
	}

	// Caller cancels early
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err = mgr.Acquire(ctx, req.Key(), req)
	if err == nil {
		t.Fatal("expected error from caller cancellation")
	}

	// Wait for cut goroutine to finish (40ms)
	time.Sleep(50 * time.Millisecond)

	expectedPath := filepath.Join(mgr.TempDir(), req.Key()+".flac")
	// The file should exist because cut succeeded
	if _, statErr := os.Stat(expectedPath); os.IsNotExist(statErr) {
		t.Fatalf("expected file to exist after successful cut even if waiter cancelled")
	}

	// Now wait for TTL timer (50ms + margin)
	time.Sleep(70 * time.Millisecond)

	// File MUST be deleted by TTL timer!
	if _, statErr := os.Stat(expectedPath); !os.IsNotExist(statErr) {
		t.Fatalf("file leaked in cache: still exists after TTL expiration: %s", expectedPath)
	}
}
