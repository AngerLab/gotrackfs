package vfs

import (
	"bytes"
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AngerLab/gotrackfs/internal/track"
)

func TestBuildDirState_Pure(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Empty directory
	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	state, facts, err := buildDirState(tmpDir, dirFi, nil, track.Quality{})
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

	state, facts, err = buildDirState(tmpDir, dirFi, nil, track.Quality{})
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
	if facts.dirModTime.IsZero() {
		t.Errorf("expected non-zero dirModTime in facts")
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

	state, _, err := buildDirState(tmpDir, dirFi, logger, track.Quality{})
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

// makeFlacHeader builds a minimal file with a valid FLAC STREAMINFO block.
func makeFlacHeader(rate, chans, bps uint64) []byte {
	var buf bytes.Buffer
	buf.WriteString("fLaC")
	buf.Write([]byte{0x80, 0x00, 0x00, 34})
	var streaminfo [34]byte
	v := rate<<44 | (chans-1)<<41 | (bps-1)<<36 | 1000
	binary.BigEndian.PutUint64(streaminfo[10:18], v)
	buf.Write(streaminfo[:])
	buf.Write(make([]byte, 1024))
	return buf.Bytes()
}

func TestBuildDirState_QualityCap(t *testing.T) {
	cueContent := `TITLE "Test Album"
FILE "audio.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00`

	tests := []struct {
		name     string
		cap      track.Quality
		srcRate  uint64
		srcBits  uint64
		wantRate int
		wantBits int
	}{
		{"no cap keeps original", track.Quality{}, 192000, 24, 0, 0},
		{"24/96 from 24/192", track.Quality{Bits: 24, SampleRate: 96000}, 192000, 24, 96000, 0},
		{"24/96 from 16/44.1 untouched", track.Quality{Bits: 24, SampleRate: 96000}, 44100, 16, 0, 0},
		{"16/44.1 from 24/192", track.Quality{Bits: 16, SampleRate: 44100}, 192000, 24, 44100, 16},
		{"broken header applies cap blindly", track.Quality{Bits: 16, SampleRate: 48000}, 0, 0, 48000, 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0o644); err != nil {
				t.Fatal(err)
			}
			audio := makeFlacHeader(tt.srcRate, 2, tt.srcBits)
			if tt.srcRate == 0 {
				audio = []byte("junk not a flac at all")
			}
			if err := os.WriteFile(filepath.Join(tmpDir, "audio.flac"), audio, 0o644); err != nil {
				t.Fatal(err)
			}

			dirFi, err := os.Stat(tmpDir)
			if err != nil {
				t.Fatal(err)
			}
			state, _, err := buildDirState(tmpDir, dirFi, nil, tt.cap)
			if err != nil {
				t.Fatal(err)
			}
			if state == nil || len(state.Albums) != 1 {
				t.Fatalf("expected 1 album, got %v", state)
			}
			sl := state.Albums[0].Tracks[0].Slice
			if sl.TargetSampleRate != tt.wantRate || sl.TargetBits != tt.wantBits {
				t.Errorf("targets = (rate %d, bits %d), want (rate %d, bits %d)",
					sl.TargetSampleRate, sl.TargetBits, tt.wantRate, tt.wantBits)
			}
		})
	}
}
