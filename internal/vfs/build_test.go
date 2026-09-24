package vfs

import (
	"bytes"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AngerLab/gotrackfs/internal/audio"
	"github.com/AngerLab/gotrackfs/internal/testutil"

	"github.com/AngerLab/gotrackfs/internal/track"
	"golang.org/x/text/unicode/norm"
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

func TestBuildDirState_ArtistInheritsAlbumPerformer(t *testing.T) {
	tmpDir := t.TempDir()

	// PERFORMER only at disc level, tracks have no PERFORMER (valid per CUE spec)
	cueContent := `PERFORMER "Woodkid"
TITLE "S16"
FILE "audio.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Goliath"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Shift"
    INDEX 01 03:00:00
  TRACK 03 AUDIO
    PERFORMER "Someone Else"
    TITLE "Guest Track"
    INDEX 01 06:00:00
`
	if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "audio.flac"), make([]byte, 1024*1024), 0644); err != nil {
		t.Fatal(err)
	}

	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	state, _, err := buildDirState(tmpDir, dirFi, nil, track.Quality{})
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || len(state.TracksByName) != 3 {
		t.Fatalf("expected 3 tracks, got %+v", state)
	}

	expectArtist := map[int]string{
		1: "Woodkid",      // inherited from album PERFORMER
		2: "Woodkid",      // inherited from album PERFORMER
		3: "Someone Else", // track-level PERFORMER wins
	}
	for _, vt := range state.TracksByName {
		want := expectArtist[vt.Num]
		if got := vt.Slice.Tags["artist"]; got != want {
			t.Errorf("track %02d: artist = %q, want %q", vt.Num, got, want)
		}
		if got := vt.Slice.Tags["album_artist"]; got != "Woodkid" {
			t.Errorf("track %02d: album_artist = %q, want %q", vt.Num, got, "Woodkid")
		}
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
		{"broken header does not upsample (keeps original)", track.Quality{Bits: 16, SampleRate: 48000}, 0, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0o644); err != nil {
				t.Fatal(err)
			}
			audio := testutil.FlacHeader(tt.srcRate, 2, tt.srcBits)
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
			if state == nil || len(state.TracksByName) != 1 {
				t.Fatalf("expected 1 track, got %v", state)
			}
			sl := trackByNum(state, 1).Slice
			if sl.TargetSampleRate != tt.wantRate || sl.TargetBits != tt.wantBits {
				t.Errorf("targets = (rate %d, bits %d), want (rate %d, bits %d)",
					sl.TargetSampleRate, sl.TargetBits, tt.wantRate, tt.wantBits)
			}
		})
	}
}

func TestBuildDirState_EstimatedSizeScaling(t *testing.T) {
	cueContent := `TITLE "Hi-Res Album"
FILE "audio.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Track 2"
    INDEX 01 01:00:00`

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 20MB dummy audio file with 192kHz / 24-bit FLAC header, 120s duration
	totalSamples := uint64(120 * 192000)
	header := testutil.FlacHeaderWithSamples(192000, 2, 24, totalSamples)

	audio := append(header, make([]byte, 20*1024*1024-len(header))...)
	if err := os.WriteFile(filepath.Join(tmpDir, "audio.flac"), audio, 0o644); err != nil {
		t.Fatal(err)
	}

	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Without cap
	stateNoCap, _, err := buildDirState(tmpDir, dirFi, nil, track.Quality{})
	if err != nil {
		t.Fatal(err)
	}
	estNoCap := trackByNum(stateNoCap, 1).EstimatedSize

	// 2. With 16-bit / 44.1kHz cap
	cap16_44 := track.Quality{Bits: 16, SampleRate: 44100}
	stateCap, _, err := buildDirState(tmpDir, dirFi, nil, cap16_44)
	if err != nil {
		t.Fatal(err)
	}
	estCap := trackByNum(stateCap, 1).EstimatedSize

	// Expected ratio is (44100 / 192000) * (16 / 24) = 0.2296875 * 0.666667 = ~0.153125
	ratio := float64(estCap) / float64(estNoCap)
	expectedRatio := (44100.0 / 192000.0) * (16.0 / 24.0)

	if ratio < expectedRatio*0.95 || ratio > expectedRatio*1.05 {
		t.Errorf("estimated size ratio = %f, expected ~%f (noCap=%d, cap=%d)",
			ratio, expectedRatio, estNoCap, estCap)
	}
}

func TestAudioQualityPlan_WavRatioWithoutCap(t *testing.T) {
	srcFmt := audio.Format{SampleRate: 96000, Bits: 24}

	// Without a quality cap the targets stay zero, but the WAV->FLAC size
	// ratio must still apply (EstimatedSize drives Getattr before any cut).
	targetRate, targetBits, wavRatio := audioQualityPlan(track.Quality{}, srcFmt, nil, filepath.FromSlash("/tmp/x.wav"), nil)
	if targetRate != 0 || targetBits != 0 {
		t.Errorf("no cap: targets = (%d, %d), want (0, 0)", targetRate, targetBits)
	}
	if math.Abs(wavRatio-wavToFlacSizeRatio) > 0.001 {
		t.Errorf("no cap WAV: ratio = %f, want %f", wavRatio, wavToFlacSizeRatio)
	}

	_, _, flacRatio := audioQualityPlan(track.Quality{}, srcFmt, nil, filepath.FromSlash("/tmp/x.flac"), nil)
	if flacRatio != 1.0 {
		t.Errorf("no cap FLAC: ratio = %f, want 1.0", flacRatio)
	}

	// With a cap the WAV factor composes with the quality downscale.
	capQ := track.Quality{Bits: 16, SampleRate: 44100}
	targetRate, targetBits, cappedRatio := audioQualityPlan(capQ, srcFmt, nil, filepath.FromSlash("/tmp/x.wav"), slog.Default())
	if targetRate != 44100 || targetBits != 16 {
		t.Errorf("cap: targets = (%d, %d), want (44100, 16)", targetRate, targetBits)
	}
	want := wavToFlacSizeRatio * (44100.0 / 96000.0) * (16.0 / 24.0)
	if math.Abs(cappedRatio-want) > 0.001 {
		t.Errorf("cap WAV: ratio = %f, want %f", cappedRatio, want)
	}
}

func TestBuildDirState_NonFlacAudioProducesFlacTracks(t *testing.T) {
	for _, ext := range []string{".wv", ".ape", ".wav", ".mp3", ".m4a"} {
		t.Run(ext, func(t *testing.T) {
			tmpDir := t.TempDir()
			cueContent := `TITLE "Album"
PERFORMER "Artist"
FILE "audio` + ext + `" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00`

			if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tmpDir, "audio"+ext), make([]byte, 1024*1024), 0644); err != nil {
				t.Fatal(err)
			}

			dirFi, err := os.Stat(tmpDir)
			if err != nil {
				t.Fatal(err)
			}

			state, _, err := buildDirState(tmpDir, dirFi, nil, track.Quality{})
			if err != nil {
				t.Fatal(err)
			}

			if _, ok := state.TracksByName["01. Track 1.flac"]; !ok {
				t.Errorf("for source %s, expected track name '01. Track 1.flac', got tracks: %+v", ext, state.TracksByName)
			}
		})
	}
}

func TestBuildDirState_MultiFileCueVinylRip(t *testing.T) {
	tmpDir := t.TempDir()

	cueContent := `PERFORMER "Johnny Cash"
TITLE "American Recordings"
FILE "side_a.flac" FLAC
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Track 2"
    INDEX 01 03:00:00
FILE "side_b.flac" FLAC
  TRACK 03 AUDIO
    TITLE "Track 3"
    INDEX 01 00:00:00
  TRACK 04 AUDIO
    TITLE "Track 4"
    INDEX 01 04:00:00
`
	if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0644); err != nil {
		t.Fatal(err)
	}

	sideAPath := filepath.Join(tmpDir, "side_a.flac")
	sideBPath := filepath.Join(tmpDir, "side_b.flac")
	if err := os.WriteFile(sideAPath, make([]byte, 1024*1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sideBPath, make([]byte, 1024*1024), 0644); err != nil {
		t.Fatal(err)
	}

	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	state, facts, err := buildDirState(tmpDir, dirFi, nil, track.Quality{})
	if err != nil {
		t.Fatalf("unexpected error building state: %v", err)
	}
	if state == nil {
		t.Fatalf("expected non-nil DirState for multi-file CUE with multi-track files")
	}
	if len(state.TracksByName) != 4 {
		t.Fatalf("expected 4 tracks, got %d", len(state.TracksByName))
	}

	// Tracks 1 & 2 should point to side_a.flac
	for _, num := range []int{1, 2} {
		tr := trackByNum(state, num)
		if tr == nil || tr.Slice.SourceAudioPath != sideAPath {
			t.Errorf("track %d SourceAudioPath = %v, want %s", num, tr, sideAPath)
		}
	}

	// Tracks 3 & 4 should point to side_b.flac
	for _, num := range []int{3, 4} {
		tr := trackByNum(state, num)
		if tr == nil || tr.Slice.SourceAudioPath != sideBPath {
			t.Errorf("track %d SourceAudioPath = %v, want %s", num, tr, sideBPath)
		}
	}

	// Both side monoliths should be hidden
	if !state.HiddenMonoliths["side_a.flac"] {
		t.Errorf("expected side_a.flac to be hidden")
	}
	if !state.HiddenMonoliths["side_b.flac"] {
		t.Errorf("expected side_b.flac to be hidden")
	}

	// Verify the set of source audio paths across all tracks. Order and the
	// album-level SourceAudioPaths list are not part of the DirState contract —
	// production consumes the paths as a set (HiddenMonoliths) and per-track
	// (Slice.SourceAudioPath), so that is what the test locks in.
	sources := map[string]bool{}
	for num := 1; num <= 4; num++ {
		if tr := trackByNum(state, num); tr != nil {
			sources[tr.Slice.SourceAudioPath] = true
		}
	}
	if len(sources) != 2 || !sources[sideAPath] || !sources[sideBPath] {
		t.Errorf("unexpected source audio paths across tracks: %+v", sources)
	}

	if facts == nil || facts.dirModTime.IsZero() {
		t.Errorf("expected valid facts")
	}
}

func TestBuildDirState_MultiFileCueAlreadySplitPerTrack(t *testing.T) {
	tmpDir := t.TempDir()

	// Multi-file CUE where every file has only 1 track
	cueContent := `TITLE "Split Album"
PERFORMER "Artist"
FILE "track01.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00
FILE "track02.flac" WAVE
  TRACK 02 AUDIO
    TITLE "Track 2"
    INDEX 01 00:00:00
`
	if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "track01.flac"), make([]byte, 1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "track02.flac"), make([]byte, 1024), 0644); err != nil {
		t.Fatal(err)
	}

	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	state, facts, err := buildDirState(tmpDir, dirFi, nil, track.Quality{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != nil {
		t.Errorf("expected nil state for already split multi-file CUE, got: %v", state)
	}
	if facts == nil || facts.dirModTime.IsZero() {
		t.Errorf("expected valid facts")
	}
}

func TestBuildDirState_MultiFileCue_MissingSideSkipsAllAndDoesNotLeakClaim(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Multi-file CUE where side B is missing
	brokenCue := `TITLE "Broken Multi"
FILE "side_a.flac" FLAC
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Track 2"
    INDEX 01 02:00:00
FILE "missing_side_b.flac" FLAC
  TRACK 03 AUDIO
    TITLE "Track 3"
    INDEX 01 00:00:00
  TRACK 04 AUDIO
    TITLE "Track 4"
    INDEX 01 02:00:00
`
	if err := os.WriteFile(filepath.Join(tmpDir, "01_broken.cue"), []byte(brokenCue), 0644); err != nil {
		t.Fatal(err)
	}
	sideAPath := filepath.Join(tmpDir, "side_a.flac")
	if err := os.WriteFile(sideAPath, make([]byte, 1024*1024), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Second valid CUE that re-uses side_a.flac to prove claimedAudios was not contaminated
	validCue := `TITLE "Valid Album"
FILE "side_a.flac" FLAC
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00
`
	if err := os.WriteFile(filepath.Join(tmpDir, "02_valid.cue"), []byte(validCue), 0644); err != nil {
		t.Fatal(err)
	}

	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	state, _, err := buildDirState(tmpDir, dirFi, logger, track.Quality{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == nil {
		t.Fatalf("expected non-nil state from valid cue")
	}
	if len(state.TracksByName) != 1 {
		t.Fatalf("expected exactly 1 virtual track (only 02_valid.cue can build), got %d", len(state.TracksByName))
	}
	if !state.HiddenMonoliths["side_a.flac"] {
		t.Errorf("expected side_a.flac to be hidden (claimed by 02_valid.cue)")
	}

	logs := buf.String()
	if !strings.Contains(logs, "audio file not found for cue") || !strings.Contains(logs, "missing_side_b.flac") {
		t.Errorf("expected warning for missing_side_b.flac, got: %s", logs)
	}
}

func TestBuildDirState_MultiFileCue_ProbedDurationAndMixedQuality(t *testing.T) {
	tmpDir := t.TempDir()

	cueContent := `PERFORMER "Mixed Artist"
TITLE "Mixed Album"
FILE "side_a.wav" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Track 2"
    INDEX 01 00:50:00
FILE "side_b.flac" FLAC
  TRACK 03 AUDIO
    TITLE "Track 3"
    INDEX 01 00:00:00
  TRACK 04 AUDIO
    TITLE "Track 4"
    INDEX 01 01:10:00
`
	if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Side A: 48kHz, 2 channels, 24-bit WAV, 100 seconds duration
	sideAWav := testutil.WavHeader(48000, 2, 24, 100.0)
	sideAPath := filepath.Join(tmpDir, "side_a.wav")
	if err := os.WriteFile(sideAPath, sideAWav, 0644); err != nil {
		t.Fatal(err)
	}

	// Side B: 96kHz, 2 channels, 24-bit FLAC, 200 seconds duration (96000 * 200 samples)
	sideBFlac := testutil.FlacHeaderWithSamples(96000, 2, 24, 96000*200)
	sideBPath := filepath.Join(tmpDir, "side_b.flac")
	if err := os.WriteFile(sideBPath, sideBFlac, 0644); err != nil {
		t.Fatal(err)
	}

	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Cap to 44.1kHz / 16-bit
	cap16_44 := track.Quality{Bits: 16, SampleRate: 44100}
	state, _, err := buildDirState(tmpDir, dirFi, nil, cap16_44)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == nil || len(state.TracksByName) != 4 {
		t.Fatalf("expected 4 tracks, got: %+v", state)
	}

	// Verify Track 2 (last track of Side A) has End set to probed duration of Side A (100.0s)
	tr2 := trackByNum(state, 2)
	if tr2 == nil {
		t.Fatalf("expected track 2, got: %+v", state.TracksByName)
	}
	if tr2.Slice.End != 100.0 {
		t.Errorf("track 2 End = %f, want 100.0 (probed WAV duration)", tr2.Slice.End)
	}
	if tr2.Slice.SourceAudioPath != sideAPath {
		t.Errorf("track 2 source = %s, want %s", tr2.Slice.SourceAudioPath, sideAPath)
	}
	if tr2.Slice.TargetSampleRate != 44100 || tr2.Slice.TargetBits != 16 {
		t.Errorf("track 2 targets = (%d, %d), want (44100, 16)", tr2.Slice.TargetSampleRate, tr2.Slice.TargetBits)
	}

	// Verify Track 4 (last track of Side B) has End set to probed duration of Side B (200.0s)
	tr4 := trackByNum(state, 4)
	if tr4 == nil {
		t.Fatalf("expected track 4, got: %+v", state.TracksByName)
	}
	if tr4.Slice.End != 200.0 {
		t.Errorf("track 4 End = %f, want 200.0 (probed FLAC duration)", tr4.Slice.End)
	}
	if tr4.Slice.SourceAudioPath != sideBPath {
		t.Errorf("track 4 source = %s, want %s", tr4.Slice.SourceAudioPath, sideBPath)
	}
	if tr4.Slice.TargetSampleRate != 44100 || tr4.Slice.TargetBits != 16 {
		t.Errorf("track 4 targets = (%d, %d), want (44100, 16)", tr4.Slice.TargetSampleRate, tr4.Slice.TargetBits)
	}

	// Verify hidden monoliths
	if !state.HiddenMonoliths["side_a.wav"] {
		t.Errorf("expected side_a.wav to be hidden")
	}
	if !state.HiddenMonoliths["side_b.flac"] {
		t.Errorf("expected side_b.flac to be hidden")
	}
}

// TestRealignSourcePathCase pins the mechanism: probe literals that differ
// in case from the on-disk name get rewritten to the on-disk name, exact
// matches stay untouched, and track slices follow the album-level paths.
func TestRealignSourcePathCase(t *testing.T) {
	tmpDir := t.TempDir()
	for _, name := range []string{"ALBUM.FLAC", "album.cue", "other.flac"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	albums := []*Album{
		{
			// "album.flac" probe literal, on disk it is "ALBUM.FLAC".
			SourceAudioPaths: []string{filepath.Join(tmpDir, "album.flac")},
			Tracks: []VirtualTrack{
				{Slice: track.Slice{SourceAudioPath: filepath.Join(tmpDir, "album.flac")}},
				{Slice: track.Slice{SourceAudioPath: filepath.Join(tmpDir, "ALBUM.FLAC")}}, // already on-disk
			},
		},
		{
			// Exact match: must be a no-op.
			SourceAudioPaths: []string{filepath.Join(tmpDir, "other.flac")},
			Tracks:           []VirtualTrack{{Slice: track.Slice{SourceAudioPath: filepath.Join(tmpDir, "other.flac")}}},
		},
	}

	(&albumBuilder{dirPath: tmpDir}).realignSourcePathCase(albums)

	if got := albums[0].SourceAudioPaths[0]; got != filepath.Join(tmpDir, "ALBUM.FLAC") {
		t.Errorf("SourceAudioPaths[0] = %q, want %q (realigned to on-disk name)", got, filepath.Join(tmpDir, "ALBUM.FLAC"))
	}
	if got := albums[0].Tracks[0].Slice.SourceAudioPath; got != filepath.Join(tmpDir, "ALBUM.FLAC") {
		t.Errorf("Tracks[0] source = %q, want %q", got, filepath.Join(tmpDir, "ALBUM.FLAC"))
	}
	if got := albums[0].Tracks[1].Slice.SourceAudioPath; got != filepath.Join(tmpDir, "ALBUM.FLAC") {
		t.Errorf("Tracks[1] source = %q, want %q (exact on-disk name untouched)", got, filepath.Join(tmpDir, "ALBUM.FLAC"))
	}
	if got := albums[1].SourceAudioPaths[0]; got != filepath.Join(tmpDir, "other.flac") {
		t.Errorf("exact-match source = %q, want %q (no-op)", got, filepath.Join(tmpDir, "other.flac"))
	}
}

// TestRealignSourcePathCase_NFDPreservedByteExact locks the NFD half of
// realignment: when the cue declares the NFC form but the file is stored in
// NFD bytes (byte-exact host), the realigned source path must be the
// byte-exact readdir name — not the NFC-normalized equivalent. audio.Probe
// and ffmpeg open the raw path with no NFC/NFD retry, so an NFC output
// would break slicing exactly like the old case bug broke case-sensitive
// hosts. The assertion is host-independent: after realignment, a direct
// os.Stat (no fallback) must find the file.
func TestRealignSourcePathCase_NFDPreservedByteExact(t *testing.T) {
	tmpDir := t.TempDir()

	nfdName := norm.NFD.String("Épisode 1") + ".flac"
	if err := os.WriteFile(filepath.Join(tmpDir, nfdName), testutil.FlacHeader(44100, 2, 16), 0o644); err != nil {
		t.Fatal(err)
	}
	cueContent := `TITLE "NFD Album"
PERFORMER "Artist"
FILE "Épisode 1.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00`
	if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0o644); err != nil {
		t.Fatal(err)
	}

	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := buildDirState(tmpDir, dirFi, nil, track.Quality{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == nil || len(state.TracksByName) != 1 {
		t.Fatalf("expected 1 track, got %+v", state)
	}

	want := filepath.Join(tmpDir, nfdName)
	for _, vt := range state.TracksByName {
		got := vt.Slice.SourceAudioPath
		if got != want {
			t.Errorf("SourceAudioPath = %q, want byte-exact on-disk %q", got, want)
		}
		if _, err := os.Stat(got); err != nil {
			t.Errorf("realigned path %q must open via plain os.Stat (NFC form would fail on byte-exact hosts): %v", got, err)
		}
	}
}

// TestBuildDirState_HiddenMonolithKeyMatchesReadDirName locks the APFS
// monolith leak end-to-end: the resolver resolves the declared "album.flac"
// onto on-disk "ALBUM.FLAC" (case folding on a case-insensitive host). The
// hidden-monolith key must be the name readdir reports, or the exact-string
// listing lookup misses and the audio file leaks into the virtual directory.
// On a case-sensitive host the same property must hold via the unclaimed
// scan (which returns the real name), so the test is host-independent.
func TestBuildDirState_HiddenMonolithKeyMatchesReadDirName(t *testing.T) {
	tmpDir := t.TempDir()
	cueContent := `TITLE "Case Album"
PERFORMER "Artist"
FILE "album.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00`
	if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "ALBUM.FLAC"), []byte("not a real flac"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := buildDirState(tmpDir, dirFi, nil, track.Quality{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == nil || len(state.TracksByName) != 1 {
		t.Fatalf("expected 1 track, got %+v", state)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	onDisk := ""
	for _, e := range entries {
		if e.Name() != "album.cue" {
			onDisk = e.Name()
		}
	}
	if onDisk == "" {
		t.Fatal("expected the audio file in the directory listing")
	}
	if !state.HiddenMonoliths[onDisk] {
		t.Errorf("HiddenMonoliths[%q] = false, want true (key must be the on-disk name)", onDisk)
	}
	// readdir names are matched byte-for-byte, so the probe literal must not
	// be the key when it differs from the on-disk name.
	if state.HiddenMonoliths["album.flac"] {
		t.Errorf("HiddenMonoliths has probe literal %q, want it realigned to %q", "album.flac", onDisk)
	}
}

// TestBuildDirState_DiskPipeline drives the entire build pipeline against a
// real temporary directory: cue parsing, audio probing, size estimation,
// monolith hiding and artwork mirroring all run end-to-end on the disk the
// way the production mount does.
func TestBuildDirState_DiskPipeline(t *testing.T) {
	cueContent := `TITLE "Test Album"
PERFORMER "Test Artist"
FILE "audio.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00`
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "album.cue"), []byte(cueContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "audio.flac"), testutil.FlacHeader(44100, 2, 16), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "cover.jpg"), []byte("fake-jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	state, facts, err := buildDirState(tmpDir, dirFi, nil, track.Quality{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil DirState")
	}
	if len(state.TracksByName) != 1 {
		t.Fatalf("expected 1 track, got %d", len(state.TracksByName))
	}
	for _, vt := range state.TracksByName {
		if vt.EstimatedSize <= 0 {
			t.Errorf("track %q EstimatedSize = %d, want > 0 (probe must run on disk)", vt.FileName, vt.EstimatedSize)
		}
		wantSrc := filepath.Join(tmpDir, "audio.flac")
		if vt.Slice.SourceAudioPath != wantSrc {
			t.Errorf("source = %q, want %q", vt.Slice.SourceAudioPath, wantSrc)
		}
		// Flat layout embeds artwork into tracks rather than mirroring it.
		wantArt := filepath.Join(tmpDir, "cover.jpg")
		if vt.Slice.ArtworkPath != wantArt {
			t.Errorf("artwork = %q, want %q (artwork lookup must run on disk)", vt.Slice.ArtworkPath, wantArt)
		}
	}
	if !state.HiddenMonoliths["audio.flac"] {
		t.Errorf("expected audio.flac to be hidden")
	}
	if facts == nil || facts.dirModTime.IsZero() {
		t.Errorf("expected valid facts")
	}
}

// trackByNum returns the virtual track with the given track number from the
// flat track table. TracksByName is a map (lookups in production go by
// filename), so the tests address tracks by number instead of by index into
// an unreachable Album.Tracks slice.
func trackByNum(state *DirState, num int) *VirtualTrack {
	for _, vt := range state.TracksByName {
		if vt.Num == num {
			return vt
		}
	}
	return nil
}

// TestBuildDirState_MultiAlbumSubdirCollisionSuffixesWholeName locks the
// virtual-subdirectory collision scheme: a second album with the same
// fractional disc number must get a whole-name suffix ("CD1.5 (2)"), not an
// extension-split one ("CD1 (2).5").
func TestBuildDirState_MultiAlbumSubdirCollisionSuffixesWholeName(t *testing.T) {
	tmpDir := t.TempDir()
	mkAlbum := func(name, file, disc string) {
		cue := "REM DISCNUMBER " + disc + "\n" +
			"TITLE \"Album " + name + "\"\n" +
			"PERFORMER \"Artist\"\n" +
			"FILE \"" + file + "\" WAVE\n" +
			"  TRACK 01 AUDIO\n" +
			"    TITLE \"Track " + name + "\"\n" +
			"    INDEX 01 00:00:00\n"
		if err := os.WriteFile(filepath.Join(tmpDir, name+".cue"), []byte(cue), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, file), make([]byte, 1024*1024), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkAlbum("side_a", "a.flac", "1.5")
	mkAlbum("side_b", "b.flac", "1.5")

	dirFi, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := buildDirState(tmpDir, dirFi, nil, track.Quality{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil DirState")
	}
	if len(state.TracksByName) != 0 {
		t.Errorf("expected flat TracksByName to be empty (multi-album => subdirs), got %d", len(state.TracksByName))
	}
	if len(state.Subdirs) != 2 {
		t.Fatalf("expected 2 subdirs, got %d", len(state.Subdirs))
	}
	first := state.Subdirs["CD1.5"]
	if first == nil || len(first.TracksByName) != 1 {
		t.Errorf("CD1.5 subdir missing or wrong track count: %+v", first)
	}
	second := state.Subdirs["CD1.5 (2)"]
	if second == nil || len(second.TracksByName) != 1 {
		t.Errorf("CD1.5 (2) subdir missing or wrong track count: %+v", second)
	}
	if !state.HiddenMonoliths["a.flac"] || !state.HiddenMonoliths["b.flac"] {
		t.Errorf("expected both monoliths hidden, got %v", state.HiddenMonoliths)
	}
}
