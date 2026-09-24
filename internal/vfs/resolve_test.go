package vfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AngerLab/gotrackfs/internal/hostfs"
)

// The resolver tests run against hostfs.NewMem(): the exact pebble-style
// payoff. No temp dirs, no real files, no cleanup — the directory shape is
// declared in three lines.

// resolveWith drives resolveAudioFileForCue through a minimal albumBuilder
// (the resolver lives on the builder because it shares its filesystem and
// directory scope).
func resolveWith(m hostfs.FS, dir, cuePath, declared string, claimed map[string]bool) string {
	return (&albumBuilder{fs: m, dirPath: dir}).resolveAudioFileForCue(cuePath, declared, claimed)
}

// TestResolveAudio_LowercaseExtensionPriority locks in the historical probe
// order: lowercase stems (.flac .wav ...) are tried before uppercase .FLAC/.WAV.
func TestResolveAudio_LowercaseExtensionPriority(t *testing.T) {
	m := hostfs.NewMem().
		AddFile("album.wav", wavBytes()).
		AddFile("album.FLAC", nil)

	got := resolveWith(m, "/", "/album.cue", "album.flac", map[string]bool{})
	if want := filepath.Join("/", "album.wav"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (lowercase .wav must win over .FLAC)", got, want)
	}
}

// TestResolveAudio_UppercaseFLACFallback ensures uppercase extensions are still
// found when no lowercase candidate exists.
func TestResolveAudio_UppercaseFLACFallback(t *testing.T) {
	m := hostfs.NewMem().AddFile("album.FLAC", nil)

	got := resolveWith(m, "/", "/album.cue", "album.flac", map[string]bool{})
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

	got := resolveWith(m, "/", "/album.cue", "album.flac", map[string]bool{})
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

	got := resolveWith(m, "/", "/album.cue", "Album - 01.flac", map[string]bool{})
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
	got := resolveWith(m, "/", "/album.cue", "album.flac", claimed)
	if want := "/other.flac"; got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (claimed candidate must be skipped)", got, want)
	}
}

// TestResolveAudio_WaveMonolithIsResolved locks the .wave feature: on main a
// sole album.wave next to album.cue was never probed (the sheet was skipped);
// now .wave is a first-class stem candidate.
func TestResolveAudio_WaveMonolithIsResolved(t *testing.T) {
	m := hostfs.NewMem().AddFile("album.wave", wavBytes())

	got := resolveWith(m, "/", "/album.cue", "album.flac", map[string]bool{})
	if want := filepath.Join("/", "album.wave"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q", got, want)
	}
}

// TestResolveAudio_WavBeatsWave pins the probe position of .wave: it is
// tried right after .wav (same container family), so an explicit .wav wins.
func TestResolveAudio_WavBeatsWave(t *testing.T) {
	m := hostfs.NewMem().
		AddFile("album.wav", wavBytes()).
		AddFile("album.wave", wavBytes())

	got := resolveWith(m, "/", "/album.cue", "album.flac", map[string]bool{})
	if want := filepath.Join("/", "album.wav"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (.wav must beat .wave)", got, want)
	}
}

// TestResolveAudio_WaveBeatsApe pins that .wave slots before .ape in the
// probe order (on main .ape won, because .wave was never tried).
func TestResolveAudio_WaveBeatsApe(t *testing.T) {
	m := hostfs.NewMem().
		AddFile("album.wave", wavBytes()).
		AddFile("album.ape", nil)

	got := resolveWith(m, "/", "/album.cue", "album.flac", map[string]bool{})
	if want := filepath.Join("/", "album.wave"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (.wave must beat .ape)", got, want)
	}
}

// TestResolveAudio_WaveCountsAsUnclaimedAudio locks the step-3 fallback:
// when neither the declared name nor the cue stem matches anything, a sole
// .wave file must count as an unclaimed audio candidate (main excluded it).
func TestResolveAudio_WaveCountsAsUnclaimedAudio(t *testing.T) {
	m := hostfs.NewMem().AddFile("other.wave", wavBytes())

	got := resolveWith(m, "/", "/album.cue", "", map[string]bool{})
	if want := filepath.Join("/", "other.wave"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (single unclaimed .wave)", got, want)
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

// TestResolveAudio_HostFSCaseSemantics pins resolveAudioFileForCue against a
// REAL host filesystem, whatever its case behavior is:
//
//   - case-sensitive host (Linux ext4): probe order is honored — album.wav
//     beats album.FLAC, because Stat("album.flac") misses and .wav is tried
//     next;
//   - case-insensitive host (APFS, NTFS): the first lookup already folds —
//     Stat("album.flac") matches album.FLAC, so .wav never gets a turn and
//     the returned path is the literal "album.flac" (which the host resolves
//     to album.FLAC).
//
// The probe-order contract therefore holds only on case-sensitive
// filesystems; production on macOS inherits the host's folding. This is the
// exact scenario that failed on APFS before the resolver moved to hostfs.FS,
// and it must keep passing on both Mac and Linux.
func TestResolveAudio_HostFSCaseSemantics(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "album.wav"), wavBytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "album.FLAC"), []byte("fLaC chunk"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := resolveWith(hostfs.Default(), dir, filepath.Join(dir, "album.cue"), "album.flac", map[string]bool{})

	if hostCaseSensitive(dir) {
		if want := filepath.Join(dir, "album.wav"); got != want {
			t.Errorf("resolveAudioFileForCue = %q, want %q (case-sensitive host: probe order honored, .wav must beat .FLAC)", got, want)
		}
	} else {
		if want := filepath.Join(dir, "album.flac"); got != want {
			t.Errorf("resolveAudioFileForCue = %q, want %q (case-insensitive host: the .flac lookup already case-folds onto album.FLAC)", got, want)
		}
	}
}

// hostCaseSensitive reports whether dir lives on a case-sensitive filesystem
// by creating a probe file and looking it up under a different case.
// Best-effort: on write failure it assumes sensitive rather than failing.
func hostCaseSensitive(dir string) bool {
	probe := filepath.Join(dir, "caseprobe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		return true
	}
	_, err := os.Stat(filepath.Join(dir, "CASEPROBE"))
	return os.IsNotExist(err)
}
