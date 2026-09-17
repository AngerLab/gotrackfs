package vfs

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

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

	// Test 4: Getattr, Open, and Opendir on hidden monolithic audio should return ENOENT
	code = v.Getattr("/Music/Fleur/Fleur - Штормовое предупреждение.flac", &st, 0)
	if code != -fuse.ENOENT {
		t.Errorf("expected -ENOENT for hidden monolithic audio in Getattr, got %d", code)
	}
	if errc, _ := v.Open("/Music/Fleur/Fleur - Штормовое предупреждение.flac", 0); errc != -fuse.ENOENT {
		t.Errorf("expected -ENOENT for hidden monolithic audio in Open, got %d", errc)
	}
	if errc, _ := v.Opendir("/Music/Fleur/Fleur - Штормовое предупреждение.flac"); errc != -fuse.ENOENT {
		t.Errorf("expected -ENOENT for hidden monolithic audio in Opendir, got %d", errc)
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

func TestVFS_MultiAlbumInSingleDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	albumDir := filepath.Join(tmpDir, "TheWall")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cd1Cue := `REM DISCNUMBER 1
PERFORMER "Pink Floyd"
TITLE "The Wall (CD1)"
FILE "CD1.flac" WAVE
  TRACK 01 AUDIO
    TITLE "In the Flesh?"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "The Thin Ice"
    INDEX 01 03:16:00`

	cd2Cue := `REM DISCNUMBER 2
PERFORMER "Pink Floyd"
TITLE "The Wall (CD2)"
FILE "CD2.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Hey You"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Comfortably Numb"
    INDEX 01 04:40:00`

	if err := os.WriteFile(filepath.Join(albumDir, "CD1.cue"), []byte(cd1Cue), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "CD2.cue"), []byte(cd2Cue), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "CD1.flac"), make([]byte, 2*1024*1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "CD2.flac"), make([]byte, 2*1024*1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "cover.jpg"), []byte("cover"), 0644); err != nil {
		t.Fatal(err)
	}

	v := New(Options{SourceRoot: tmpDir})

	var entries []string
	code := v.Readdir("/TheWall", func(name string, stat *fuse.Stat_t, ofst int64) bool {
		entries = append(entries, name)
		return true
	}, 0, 0)
	if code != 0 {
		t.Fatalf("Readdir failed: %d", code)
	}

	// Real non-audio files must be preserved
	assertContains(t, entries, "cover.jpg")
	assertContains(t, entries, "CD1.cue")
	assertContains(t, entries, "CD2.cue")

	// Both monolithic files must be hidden
	assertNotContains(t, entries, "CD1.flac")
	assertNotContains(t, entries, "CD2.flac")

	// Tracks from CD1 and CD2 must be present with disc prefixes to avoid collisions
	assertContains(t, entries, "1-01. In the Flesh.flac")
	assertContains(t, entries, "1-02. The Thin Ice.flac")
	assertContains(t, entries, "2-01. Hey You.flac")
	assertContains(t, entries, "2-02. Comfortably Numb.flac")

	// Getattr on both discs
	var st fuse.Stat_t
	if code := v.Getattr("/TheWall/1-01. In the Flesh.flac", &st, 0); code != 0 {
		t.Errorf("Getattr CD1 track failed: %d", code)
	}
	if code := v.Getattr("/TheWall/2-01. Hey You.flac", &st, 0); code != 0 {
		t.Errorf("Getattr CD2 track failed: %d", code)
	}
}

func TestVFS_AudioModificationInvalidatesCache(t *testing.T) {
	tmpDir := t.TempDir()
	albumDir := filepath.Join(tmpDir, "SingleAlbum")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatal(err)
	}

	cueContent := `TITLE "Album"
FILE "audio.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Track 2"
    INDEX 01 02:00:00`

	cuePath := filepath.Join(albumDir, "album.cue")
	audioPath := filepath.Join(albumDir, "audio.flac")

	if err := os.WriteFile(cuePath, []byte(cueContent), 0644); err != nil {
		t.Fatal(err)
	}
	// Initial 10MB audio
	if err := os.WriteFile(audioPath, make([]byte, 10*1024*1024), 0644); err != nil {
		t.Fatal(err)
	}

	v := New(Options{SourceRoot: tmpDir})

	var st1 fuse.Stat_t
	if code := v.Getattr("/SingleAlbum/01. Track 1.flac", &st1, 0); code != 0 {
		t.Fatalf("first Getattr failed: %d", code)
	}

	// Now replace audio file with 20MB content and new mtime, without touching album.cue
	newAudioData := make([]byte, 20*1024*1024)
	if err := os.WriteFile(audioPath, newAudioData, 0644); err != nil {
		t.Fatal(err)
	}
	// Ensure mtime is different
	newMtime := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(audioPath, newMtime, newMtime)

	var st2 fuse.Stat_t
	if code := v.Getattr("/SingleAlbum/01. Track 1.flac", &st2, 0); code != 0 {
		t.Fatalf("second Getattr failed: %d", code)
	}

	if st2.Size == st1.Size {
		t.Errorf("expected estimated size to update after audio modification (old=%d, new=%d)", st1.Size, st2.Size)
	}
	if st2.Size <= st1.Size {
		t.Errorf("expected new size to be larger (old=%d, new=%d)", st1.Size, st2.Size)
	}
}

func TestAlbumCache_DirMtimeAvoidsReaddir(t *testing.T) {
	tmpDir := t.TempDir()
	emptyDir := filepath.Join(tmpDir, "NonAlbum")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatal(err)
	}

	cache := NewAlbumCache()

	// Call 1 on empty dir
	state1, err := cache.GetDirState(emptyDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state1 != nil {
		t.Fatalf("expected nil state for non-album dir, got: %v", state1)
	}

	// Verify cached
	cache.mu.RLock()
	cached, ok := cache.dirs[emptyDir]
	cache.mu.RUnlock()
	if !ok || cached == nil {
		t.Fatalf("expected empty dir to be cached")
	}
	if cached.state != nil {
		t.Fatalf("expected cached.state to be nil")
	}

	// Call 2 on empty dir should return cached nil
	state2, err := cache.GetDirState(emptyDir)
	if err != nil || state2 != nil {
		t.Fatalf("expected nil state on cache hit, got %v, err: %v", state2, err)
	}

	// Now add a CUE + audio into emptyDir (which changes directory mtime)
	cueContent := `TITLE "NewAlbum"
FILE "music.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00`
	if err := os.WriteFile(filepath.Join(emptyDir, "music.flac"), make([]byte, 1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(emptyDir, "album.cue"), []byte(cueContent), 0644); err != nil {
		t.Fatal(err)
	}
	// Force directory mtime change in case filesystem timestamp resolution is coarse
	newDirMtime := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(emptyDir, newDirMtime, newDirMtime)

	// Call 3: should detect directory mtime change and discover the new album!
	state3, err := cache.GetDirState(emptyDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state3 == nil {
		t.Fatalf("expected state after adding cue, got nil")
	}
	if len(state3.TracksByName) != 1 {
		t.Fatalf("expected 1 track, got %d", len(state3.TracksByName))
	}

	// Call 4: should return exact same state pointer from cache without re-parsing
	state4, err := cache.GetDirState(emptyDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state4 != state3 {
		t.Errorf("expected pointer equality for cached DirState, got different pointers")
	}
}

