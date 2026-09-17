package vfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildDirState_Pure(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Empty directory
	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	state, facts, err := buildDirState(tmpDir, dirFi)
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

	state, facts, err = buildDirState(tmpDir, dirFi)
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
