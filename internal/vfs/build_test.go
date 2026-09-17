package vfs

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildDirState_Pure(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Empty directory
	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	state, facts, err := buildDirState(tmpDir, dirFi, nil)
	if err != nil {
		t.Fatalf("unexpected error on empty dir: %v", err)
	}
	if state != nil {
		t.Errorf("expected nil state for empty dir, got: %v", state)
	}
	if facts == nil || !facts.dirModTime.Equal(dirFi.ModTime()) {
		t.Errorf("expected facts with dirModTime, got: %v", facts)
	}

	// 2. Directory with CUE and audio
	cueContent := `TITLE "Test Album"
PERFORMER "Test Artist"
FILE "audio.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Track 2"
    INDEX 01 03:00:00`

	if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "audio.flac"), make([]byte, 1024*1024), 0644); err != nil {
		t.Fatal(err)
	}

	dirFi, err = os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	state, facts, err = buildDirState(tmpDir, dirFi, nil)
	if err != nil {
		t.Fatalf("unexpected error building state: %v", err)
	}
	if state == nil {
		t.Fatalf("expected non-nil DirState")
	}
	if len(state.Albums) != 1 {
		t.Errorf("expected 1 album, got %d", len(state.Albums))
	}
	if len(state.TracksByName) != 2 {
		t.Errorf("expected 2 tracks, got %d", len(state.TracksByName))
	}
	if !state.HiddenMonoliths["audio.flac"] {
		t.Errorf("expected audio.flac to be hidden")
	}
	if len(facts.cueModTimes) != 1 || len(facts.audioModTimes) != 1 {
		t.Errorf("expected 1 cue and 1 audio fact, got cue=%d audio=%d", len(facts.cueModTimes), len(facts.audioModTimes))
	}
}

func TestBuildDirState_LogsWarningsOnBrokenFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Unreadable CUE file to trigger ParseFile read error
	corruptPath := filepath.Join(tmpDir, "corrupt.cue")
	if err := os.WriteFile(corruptPath, []byte("some content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(corruptPath, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(corruptPath, 0644)

	// 2. Empty CUE with no tracks
	if err := os.WriteFile(filepath.Join(tmpDir, "empty.cue"), []byte("TITLE \"Empty\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Missing audio CUE
	missingAudioCue := `TITLE "Missing Audio"
FILE "nonexistent.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00`
	if err := os.WriteFile(filepath.Join(tmpDir, "missing.cue"), []byte(missingAudioCue), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	state, _, err := buildDirState(tmpDir, dirFi, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != nil {
		t.Errorf("expected nil state when all CUEs are broken or missing audio")
	}

	logs := buf.String()
	if !strings.Contains(logs, "skipping invalid cue file") {
		t.Errorf("expected log warning for corrupt cue, got: %s", logs)
	}
	if !strings.Contains(logs, "skipping cue file with no tracks") {
		t.Errorf("expected log warning for empty cue, got: %s", logs)
	}
	if !strings.Contains(logs, "audio file not found for cue") {
		t.Errorf("expected log warning for missing audio, got: %s", logs)
	}
}
