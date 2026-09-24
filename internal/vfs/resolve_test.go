package vfs

import (
	"os"
	"path/filepath"
	"testing"
)

// The resolver tests run against real temp directories: each test declares
// its files with os.WriteFile and drives the resolver exactly like the
// production mount does (statPath/readDir with the NFC/NFD fallback).

// resolveWith drives resolveAudioFileForCue through a minimal albumBuilder
// (the resolver lives on the builder because it shares the directory scope).
func resolveWith(dir, cuePath, declared string, claimed map[string]bool) string {
	return (&albumBuilder{dirPath: dir}).resolveAudioFileForCue(cuePath, declared, claimed)
}

// writeTestFiles creates the given names (empty files, or wavBytes for names
// ending in .wave/.wav) inside dir.
func writeTestFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		data := []byte(nil)
		if ext := filepath.Ext(name); ext == ".wav" || ext == ".wave" {
			data = wavBytes()
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestResolveAudio_DeclaredNameWins ensures an exact declared filename beats
// every stem-derived candidate.
func TestResolveAudio_DeclaredNameWins(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, "Album - 01.flac", "album.wav")

	got := resolveWith(dir, filepath.Join(dir, "album.cue"), "Album - 01.flac", map[string]bool{})
	if want := filepath.Join(dir, "Album - 01.flac"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (declared name wins)", got, want)
	}
}

// TestResolveAudio_ClaimedCandidatesAreSkipped checks that already-claimed
// audio files are not picked again by the next cue file.
func TestResolveAudio_ClaimedCandidatesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, "album.flac", "other.flac")

	// Claim both case forms: the claim map is byte-exact, and on a
	// case-insensitive host the .FLAC probe literal is skipped only if it is
	// claimed too. On a case-sensitive host the uppercase form never
	// stat-succeeds, so the extra claim is inert there.
	claimed := map[string]bool{
		filepath.Join(dir, "album.flac"): true,
		filepath.Join(dir, "album.FLAC"): true,
	}
	got := resolveWith(dir, filepath.Join(dir, "album.cue"), "album.flac", claimed)
	if want := filepath.Join(dir, "other.flac"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (claimed candidate must be skipped)", got, want)
	}
}

// TestResolveAudio_WaveMonolithIsResolved locks the .wave feature: on main a
// sole album.wave next to album.cue was never probed (the sheet was skipped);
// now .wave is a first-class stem candidate.
func TestResolveAudio_WaveMonolithIsResolved(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, "album.wave")

	got := resolveWith(dir, filepath.Join(dir, "album.cue"), "album.flac", map[string]bool{})
	if want := filepath.Join(dir, "album.wave"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q", got, want)
	}
}

// TestResolveAudio_WavBeatsWave pins the probe position of .wave: it is
// tried right after .wav (same container family), so an explicit .wav wins.
func TestResolveAudio_WavBeatsWave(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, "album.wav", "album.wave")

	got := resolveWith(dir, filepath.Join(dir, "album.cue"), "album.flac", map[string]bool{})
	if want := filepath.Join(dir, "album.wav"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (.wav must beat .wave)", got, want)
	}
}

// TestResolveAudio_WaveBeatsApe pins that .wave slots before .ape in the
// probe order (on main .ape won, because .wave was never tried).
func TestResolveAudio_WaveBeatsApe(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, "album.wave", "album.ape")

	got := resolveWith(dir, filepath.Join(dir, "album.cue"), "album.flac", map[string]bool{})
	if want := filepath.Join(dir, "album.wave"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (.wave must beat .ape)", got, want)
	}
}

// TestResolveAudio_WaveCountsAsUnclaimedAudio locks the step-3 fallback:
// when neither the declared name nor the cue stem matches anything, a sole
// .wave file must count as an unclaimed audio candidate (main excluded it).
func TestResolveAudio_WaveCountsAsUnclaimedAudio(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, "other.wave")

	got := resolveWith(dir, filepath.Join(dir, "album.cue"), "", map[string]bool{})
	if want := filepath.Join(dir, "other.wave"); got != want {
		t.Errorf("resolveAudioFileForCue = %q, want %q (single unclaimed .wave)", got, want)
	}
}

// TestFindAllCueFiles lists only non-directory .cue entries.
func TestFindAllCueFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, "a.cue", "b.CUE", "c.txt")
	if err := os.Mkdir(filepath.Join(dir, "d.cue"), 0o755); err != nil { // a directory named d.cue must not match
		t.Fatal(err)
	}

	got, err := findAllCueFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("findAllCueFiles = %v, want 2 files", got)
	}
	if got[0] != filepath.Join(dir, "a.cue") || got[1] != filepath.Join(dir, "b.CUE") {
		t.Errorf("findAllCueFiles = %v, want [a.cue b.CUE] in stable order", got)
	}
}

// wavBytes returns a minimal non-empty payload (the resolver only cares
// about existence, not audio validity).
func wavBytes() []byte { return []byte("RIFF....WAVEfmt ") }

// TestResolveAudio_CaseSemantics pins resolveAudioFileForCue against a
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
// //
// The probe-order contract therefore holds only on case-sensitive
// filesystems; production on macOS inherits the host's folding. The test
// runs against the real filesystem and must pass on both Mac and Linux.
func TestResolveAudio_CaseSemantics(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, "album.wav", "album.FLAC")

	got := resolveWith(dir, filepath.Join(dir, "album.cue"), "album.flac", map[string]bool{})

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

// TestResolveAudio_UppercaseWaveProbe pins the .WAVE uppercase probe: on a
// case-sensitive host an on-disk album.WAVE is only found through the
// uppercase literal; on a case-insensitive host the lowercase .wave probe
// already case-folds onto it. probeOrder derives its uppercase variants
// from AudioExtensions, so this also guards against derivation regressions.
func TestResolveAudio_UppercaseWaveProbe(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, "album.WAVE")

	got := resolveWith(dir, filepath.Join(dir, "album.cue"), "album.flac", map[string]bool{})

	if hostCaseSensitive(dir) {
		if want := filepath.Join(dir, "album.WAVE"); got != want {
			t.Errorf("resolveAudioFileForCue = %q, want %q (case-sensitive host: .WAVE uppercase probe must hit)", got, want)
		}
	} else {
		if want := filepath.Join(dir, "album.wave"); got != want {
			t.Errorf("resolveAudioFileForCue = %q, want %q (case-insensitive host: .wave probe case-folds onto on-disk album.WAVE)", got, want)
		}
	}
}
