package vfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AngerLab/gotrackfs/internal/testutil"
)

// TestResolveAudio_LowercaseExtensionPriority locks in the historical probe
// order: lowercase stems (.flac .wav ...) are tried before uppercase .FLAC/.WAV.
func TestResolveAudio_LowercaseExtensionPriority(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "album.wav"), testutil.WavHeader(44100, 2, 16, 5.0), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "album.FLAC"), testutil.FlacHeader(44100, 2, 16), 0644); err != nil {
		t.Fatal(err)
	}

	got := resolveAudioFileForCue(tmpDir, filepath.Join(tmpDir, "album.cue"), "album.flac", map[string]bool{})
	if want := filepath.Join(tmpDir, "album.wav"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (lowercase .wav must win over .FLAC)", got, want)
	}
}

// TestResolveAudio_UppercaseFLACFallback ensures uppercase extensions are still
// found when no lowercase candidate exists.
func TestResolveAudio_UppercaseFLACFallback(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "album.FLAC"), testutil.FlacHeader(44100, 2, 16), 0644); err != nil {
		t.Fatal(err)
	}

	got := resolveAudioFileForCue(tmpDir, filepath.Join(tmpDir, "album.cue"), "album.flac", map[string]bool{})
	if want := filepath.Join(tmpDir, "album.FLAC"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q", got, want)
	}
}

// TestResolveAudio_UppercaseWAVIsLastInPriority ensures ".WAV" is probed after
// ".FLAC", matching the historical order.
func TestResolveAudio_UppercaseWAVIsLastInPriority(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "album.FLAC"), testutil.FlacHeader(44100, 2, 16), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "album.WAV"), testutil.WavHeader(44100, 2, 16, 5.0), 0644); err != nil {
		t.Fatal(err)
	}

	got := resolveAudioFileForCue(tmpDir, filepath.Join(tmpDir, "album.cue"), "album.flac", map[string]bool{})
	if want := filepath.Join(tmpDir, "album.FLAC"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (.FLAC is probed before .WAV)", got, want)
	}
}
