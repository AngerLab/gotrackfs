package vfs

import (
	"path/filepath"
	"testing"

	"github.com/AngerLab/gotrackfs/internal/hostfs"
)

// The resolver tests run against hostfs.NewMem(): the exact pebble-style
// payoff. No temp dirs, no real files, no cleanup — the directory shape is
// declared in three lines.

// TestResolveAudio_LowercaseExtensionPriority locks in the historical probe
// order: lowercase stems (.flac .wav ...) are tried before uppercase .FLAC/.WAV.
func TestResolveAudio_LowercaseExtensionPriority(t *testing.T) {
	m := hostfs.NewMem().
		AddFile("album.wav", wavBytes()).
		AddFile("album.FLAC", nil)

	got := resolveAudioFileForCue(m, "/", "/album.cue", "album.flac", map[string]bool{})
	if want := filepath.Join("/", "album.wav"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (lowercase .wav must win over .FLAC)", got, want)
	}
}

// TestResolveAudio_UppercaseFLACFallback ensures uppercase extensions are still
// found when no lowercase candidate exists.
func TestResolveAudio_UppercaseFLACFallback(t *testing.T) {
	m := hostfs.NewMem().AddFile("album.FLAC", nil)

	got := resolveAudioFileForCue(m, "/", "/album.cue", "album.flac", map[string]bool{})
	if want := filepath.Join("/", "album.FLAC"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q", got, want)
	}
}

// TestResolveAudio_UppercaseWAVIsLastInPriority ensures ".WAV" is probed after
// ".FLAC", matching the historical order.
func TestResolveAudio_UppercaseWAVIsLastInPriority(t *testing.T) {
	m := hostfs.NewMem().
		AddFile("album.FLAC", nil).
		AddFile("album.WAV", nil)

	got := resolveAudioFileForCue(m, "/", "/album.cue", "album.flac", map[string]bool{})
	if want := filepath.Join("/", "album.FLAC"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (.FLAC is probed before .WAV)", got, want)
	}
}

// TestResolveAudio_DeclaredNameWins ensures an exact declared filename beats
// every stem-derived candidate.
func TestResolveAudio_DeclaredNameWins(t *testing.T) {
	m := hostfs.NewMem().
		AddFile("Album - 01.flac", nil).
		AddFile("album.wav", nil)

	got := resolveAudioFileForCue(m, "/", "/album.cue", "Album - 01.flac", map[string]bool{})
	if want := filepath.Join("/", "Album - 01.flac"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (declared name wins)", got, want)
	}
}

// TestResolveAudio_ClaimedCandidatesAreSkipped checks that already-claimed
// audio files are not picked again by the next cue file.
func TestResolveAudio_ClaimedCandidatesAreSkipped(t *testing.T) {
	m := hostfs.NewMem().
		AddFile("album.flac", nil).
		AddFile("other.flac", nil)

	claimed := map[string]bool{"/album.flac": true}
	got := resolveAudioFileForCue(m, "/", "/album.cue", "album.flac", claimed)
	if want := "/other.flac"; got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (claimed candidate must be skipped)", got, want)
	}
}

// TestFindAllCueFiles lists only non-directory .cue entries.
func TestFindAllCueFiles(t *testing.T) {
	m := hostfs.NewMem().
		AddFile("a.cue", nil).
		AddFile("b.CUE", nil).
		AddFile("c.txt", nil).
		AddDir("d.cue") // a directory named d.cue must not match

	got, err := findAllCueFiles(m, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("findAllCueFiles = %v, want 2 files", got)
	}
	if got[0] != filepath.Join("/", "a.cue") || got[1] != filepath.Join("/", "b.CUE") {
		t.Errorf("findAllCueFiles = %v, want [a.cue b.CUE] in stable order", got)
	}
}

// wavBytes returns a minimal non-empty payload (the resolver only cares
// about existence, not audio validity).
func wavBytes() []byte { return []byte("RIFF....WAVEfmt ") }
