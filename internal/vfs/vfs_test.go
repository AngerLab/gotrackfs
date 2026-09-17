package vfs

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/winfsp/cgofuse/fuse"
)

func TestVFS_VirtualTrackListingAndAttributes(t *testing.T) {
	// Create test structure in temp dir
	tmpDir := t.TempDir()
	albumDir := filepath.Join(tmpDir, "Music", "Fleur")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatalf("mkdir albumDir: %v", err)
	}

	// Copy real fleur.cue from testdata
	cueSrc := filepath.Join("..", "..", "testdata", "fleur.cue")
	cueBytes, err := os.ReadFile(cueSrc)
	if err != nil {
		t.Fatalf("read fleur.cue: %v", err)
	}
	cueDst := filepath.Join(albumDir, "fleur.cue")
	if err := os.WriteFile(cueDst, cueBytes, 0644); err != nil {
		t.Fatalf("write fleur.cue: %v", err)
	}

	// Create dummy monolithic audio file (11MB)
	audioDst := filepath.Join(albumDir, "Fleur - Штормовое предупреждение.flac")
	dummyData := make([]byte, 11*1024*1024)
	if err := os.WriteFile(audioDst, dummyData, 0644); err != nil {
		t.Fatalf("write dummy audio: %v", err)
	}

	// Create cover.jpg
	coverDst := filepath.Join(albumDir, "cover.jpg")
	if err := os.WriteFile(coverDst, []byte("fake-jpeg"), 0644); err != nil {
		t.Fatalf("write cover: %v", err)
	}

	// Initialize VFS without mounting
	v := New(Options{
		SourceRoot: tmpDir,
		KeepAlbum:  false,
	})

	// Test 1: Readdir on intermediate folder /Music
	var musicEntries []string
	code := v.Readdir("/Music", func(name string, stat *fuse.Stat_t, ofst int64) bool {
		musicEntries = append(musicEntries, name)
		return true
	}, 0, 0)
	if code != 0 {
		t.Fatalf("Readdir(/Music) failed with code %d", code)
	}
	assertContains(t, musicEntries, "Fleur")

	// Test 2: Readdir on /Music/Fleur
	var albumEntries []string
	code = v.Readdir("/Music/Fleur", func(name string, stat *fuse.Stat_t, ofst int64) bool {
		albumEntries = append(albumEntries, name)
		return true
	}, 0, 0)
	if code != 0 {
		t.Fatalf("Readdir(/Music/Fleur) failed with code %d", code)
	}

	// Should contain real non-audio files
	assertContains(t, albumEntries, "cover.jpg")
	assertContains(t, albumEntries, "fleur.cue")

	// Should HIDE the monolithic audio file
	assertNotContains(t, albumEntries, "Fleur - Штормовое предупреждение.flac")

	// Should contain virtual tracks
	assertContains(t, albumEntries, "01. Интро.flac")
	assertContains(t, albumEntries, "02. Железо поёт.flac")
	assertContains(t, albumEntries, "11. Черта.flac")

	// Total tracks in fleur.cue is 11, plus ".", "..", "cover.jpg", "fleur.cue" -> 15 entries
	if len(albumEntries) != 15 {
		t.Errorf("expected 15 entries in album dir, got %d: %v", len(albumEntries), albumEntries)
	}

	// Test 3: Getattr on virtual track
	var st fuse.Stat_t
	code = v.Getattr("/Music/Fleur/01. Интро.flac", &st, 0)
	if code != 0 {
		t.Fatalf("Getattr(/Music/Fleur/01. Интро.flac) failed with code %d", code)
	}
	if (st.Mode & syscall.S_IFMT) != syscall.S_IFREG {
		t.Errorf("expected regular file mode, got %o", st.Mode)
	}
	if st.Size <= 0 {
		t.Errorf("expected positive estimated size, got %d", st.Size)
	}

	// Test 4: Getattr on hidden monolithic audio should return ENOENT
	code = v.Getattr("/Music/Fleur/Fleur - Штормовое предупреждение.flac", &st, 0)
	if code != -fuse.ENOENT {
		t.Errorf("expected -ENOENT for hidden monolithic audio, got %d", code)
	}

	// Test 5: Getattr on real file cover.jpg
	code = v.Getattr("/Music/Fleur/cover.jpg", &st, 0)
	if code != 0 {
		t.Fatalf("Getattr(/Music/Fleur/cover.jpg) failed with code %d", code)
	}
	if st.Size != int64(len("fake-jpeg")) {
		t.Errorf("expected size %d, got %d", len("fake-jpeg"), st.Size)
	}
}

func assertContains(t *testing.T, slice []string, target string) {
	t.Helper()
	for _, s := range slice {
		if s == target {
			return
		}
	}
	t.Errorf("expected slice to contain %q, but got: %v", target, slice)
}

func assertNotContains(t *testing.T, slice []string, target string) {
	t.Helper()
	for _, s := range slice {
		if s == target {
			t.Errorf("expected slice NOT to contain %q, but found it in: %v", target, slice)
			return
		}
	}
}
