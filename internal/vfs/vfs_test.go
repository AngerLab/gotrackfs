package vfs

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"gotrackfs/internal/cutter"

	"github.com/winfsp/cgofuse/fuse"
	"golang.org/x/text/unicode/norm"
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

	// Multi-album folder must present virtual subdirectories CD1 and CD2
	assertContains(t, entries, "CD1")
	assertContains(t, entries, "CD2")

	// Tracks should not pollute the parent directory
	assertNotContains(t, entries, "01. In the Flesh.flac")
	assertNotContains(t, entries, "1-01. In the Flesh.flac")

	// Verify CD1 virtual directory
	var cd1Stat fuse.Stat_t
	if code := v.Getattr("/TheWall/CD1", &cd1Stat, 0); code != 0 {
		t.Fatalf("Getattr CD1 failed: %d", code)
	}
	if (cd1Stat.Mode & syscall.S_IFMT) != syscall.S_IFDIR {
		t.Errorf("CD1 mode is not directory: %o", cd1Stat.Mode)
	}

	var cd1Entries []string
	code = v.Readdir("/TheWall/CD1", func(name string, stat *fuse.Stat_t, ofst int64) bool {
		cd1Entries = append(cd1Entries, name)
		return true
	}, 0, 0)
	if code != 0 {
		t.Fatalf("Readdir /TheWall/CD1 failed: %d", code)
	}

	assertContains(t, cd1Entries, "01. In the Flesh.flac")
	assertContains(t, cd1Entries, "02. The Thin Ice.flac")
	// Parent artwork must be mirrored into virtual subdirectories
	assertContains(t, cd1Entries, "cover.jpg")

	// Verify CD2 virtual directory
	var cd2Entries []string
	code = v.Readdir("/TheWall/CD2", func(name string, stat *fuse.Stat_t, ofst int64) bool {
		cd2Entries = append(cd2Entries, name)
		return true
	}, 0, 0)
	if code != 0 {
		t.Fatalf("Readdir /TheWall/CD2 failed: %d", code)
	}

	assertContains(t, cd2Entries, "01. Hey You.flac")
	assertContains(t, cd2Entries, "02. Comfortably Numb.flac")
	assertContains(t, cd2Entries, "cover.jpg")

	// Getattr on tracks inside virtual subdirectories
	var st fuse.Stat_t
	if code := v.Getattr("/TheWall/CD1/01. In the Flesh.flac", &st, 0); code != 0 {
		t.Errorf("Getattr CD1 track failed: %d", code)
	}
	if code := v.Getattr("/TheWall/CD2/01. Hey You.flac", &st, 0); code != 0 {
		t.Errorf("Getattr CD2 track failed: %d", code)
	}

	// Read mirrored artwork inside virtual subdirectory
	openCode, fh := v.Open("/TheWall/CD1/cover.jpg", fuse.O_RDONLY)
	if openCode != 0 {
		t.Fatalf("Open mirrored cover.jpg failed: %d", openCode)
	}
	buf := make([]byte, 10)
	n := v.Read("/TheWall/CD1/cover.jpg", buf, 0, fh)
	if n != 5 || string(buf[:n]) != "cover" {
		t.Errorf("expected 'cover', got %q", string(buf[:n]))
	}
	v.Release("/TheWall/CD1/cover.jpg", fh)

	// Opening virtual directory as a file must return EISDIR
	if code, _ := v.Open("/TheWall/CD1", fuse.O_RDONLY); code != -fuse.EISDIR {
		t.Errorf("Open on virtual dir expected EISDIR (-%d), got %d", fuse.EISDIR, code)
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

	cache := NewAlbumCache(nil)

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

func TestVFS_DebugAndLogger(t *testing.T) {
	tmpDir := t.TempDir()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	v := New(Options{
		SourceRoot: tmpDir,
		Debug:      true,
		Logger:     logger,
	})

	if v.logger == nil {
		t.Fatalf("expected logger to be initialized")
	}

	var st fuse.Stat_t
	code := v.Getattr("/", &st, 0)
	if code != 0 {
		t.Fatalf("Getattr(/) failed: %d", code)
	}
}

func TestVFS_CollisionAvoidance(t *testing.T) {
	tmpDir := t.TempDir()
	albumDir := filepath.Join(tmpDir, "CollisionAlbum")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatal(err)
	}

	cueContent := `TITLE "Collisions"
FILE "audio.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Same Title"
    INDEX 01 00:00:00
  TRACK 01 AUDIO
    TITLE "Same Title"
    INDEX 01 01:00:00
  TRACK 02 AUDIO
    TITLE "Real File Shadow"
    INDEX 01 02:00:00`

	if err := os.WriteFile(filepath.Join(albumDir, "album.cue"), []byte(cueContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "audio.flac"), make([]byte, 1024*1024), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a real file on disk that would be shadowed by TRACK 02
	realFilePath := filepath.Join(albumDir, "02. Real File Shadow.flac")
	if err := os.WriteFile(realFilePath, []byte("REAL_CONTENT"), 0644); err != nil {
		t.Fatal(err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	v := New(Options{
		SourceRoot: tmpDir,
		Logger:     logger,
	})

	var entries []string
	code := v.Readdir("/CollisionAlbum", func(name string, stat *fuse.Stat_t, ofst int64) bool {
		entries = append(entries, name)
		return true
	}, 0, 0)
	if code != 0 {
		t.Fatalf("Readdir failed: %d", code)
	}

	// 1. Duplicate track in CUE got suffix (2)
	assertContains(t, entries, "01. Same Title.flac")
	assertContains(t, entries, "01. Same Title (2).flac")

	// 2. Real file on disk is preserved, and virtual track got suffix (2)
	assertContains(t, entries, "02. Real File Shadow.flac")
	assertContains(t, entries, "02. Real File Shadow (2).flac")

	// 3. Verify reading the real file returns real content, not ENOSYS
	openCode, fh := v.Open("/CollisionAlbum/02. Real File Shadow.flac", fuse.O_RDONLY)
	if openCode != 0 {
		t.Fatalf("Open real file failed: %d", openCode)
	}
	buf := make([]byte, 20)
	n := v.Read("/CollisionAlbum/02. Real File Shadow.flac", buf, 0, fh)
	if string(buf[:n]) != "REAL_CONTENT" {
		t.Errorf("expected 'REAL_CONTENT', got %q", string(buf[:n]))
	}
	v.Release("/CollisionAlbum/02. Real File Shadow.flac", fh)

	// 4. Verify virtual track with suffix (2) returns ENOSYS on Open (virtual track)
	openVirtCode, _ := v.Open("/CollisionAlbum/02. Real File Shadow (2).flac", fuse.O_RDONLY)
	if openVirtCode != -fuse.ENOSYS {
		t.Errorf("expected ENOSYS for virtual track, got %d", openVirtCode)
	}

	// 5. Verify warnings were logged for both collisions
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "track filename collision") {
		t.Errorf("expected collision warnings in log, got: %s", logOutput)
	}
}

func TestVFS_DirectoryAndMonolithCollisions(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Multi-album folder with a REAL existing CD1 directory on disk
	multiDir := filepath.Join(tmpDir, "MultiWithRealCD1")
	if err := os.MkdirAll(filepath.Join(multiDir, "CD1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(multiDir, "CD1", "scan.jpg"), []byte("SCAN"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(multiDir, "CD1.flac"), make([]byte, 1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(multiDir, "CD2.flac"), make([]byte, 1024), 0644); err != nil {
		t.Fatal(err)
	}
	cue1 := `TITLE "Multi CD1"
FILE "CD1.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 1"
    INDEX 01 00:00:00`
	cue2 := `TITLE "Multi CD2"
FILE "CD2.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Track 2"
    INDEX 01 00:00:00`
	if err := os.WriteFile(filepath.Join(multiDir, "CD1.cue"), []byte(cue1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(multiDir, "CD2.cue"), []byte(cue2), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Single-album where track name collides with hidden monolithic filename
	singleDir := filepath.Join(tmpDir, "MonolithCollision")
	if err := os.MkdirAll(singleDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Monolith audio file is literally named "01. Intro.flac"
	if err := os.WriteFile(filepath.Join(singleDir, "01. Intro.flac"), make([]byte, 1024), 0644); err != nil {
		t.Fatal(err)
	}
	singleCue := `TITLE "Single"
FILE "01. Intro.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Intro"
    INDEX 01 00:00:00`
	if err := os.WriteFile(filepath.Join(singleDir, "album.cue"), []byte(singleCue), 0644); err != nil {
		t.Fatal(err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	v := New(Options{
		SourceRoot: tmpDir,
		Logger:     logger,
	})

	// Check multi-album directory
	var multiEntries []string
	code := v.Readdir("/MultiWithRealCD1", func(name string, stat *fuse.Stat_t, ofst int64) bool {
		multiEntries = append(multiEntries, name)
		return true
	}, 0, 0)
	if code != 0 {
		t.Fatalf("Readdir MultiWithRealCD1 failed: %d", code)
	}

	// Real CD1 directory on disk must be preserved!
	assertContains(t, multiEntries, "CD1")
	// Virtual CD1 directory must get suffix (2) to avoid shadowing!
	assertContains(t, multiEntries, "CD1 (2)")
	// Virtual CD2 directory remains CD2
	assertContains(t, multiEntries, "CD2")

	// Verify real CD1 contains real scan.jpg
	var realCD1Entries []string
	code = v.Readdir("/MultiWithRealCD1/CD1", func(name string, stat *fuse.Stat_t, ofst int64) bool {
		realCD1Entries = append(realCD1Entries, name)
		return true
	}, 0, 0)
	if code != 0 {
		t.Fatalf("Readdir /MultiWithRealCD1/CD1 failed: %d", code)
	}
	assertContains(t, realCD1Entries, "scan.jpg")

	// Verify virtual CD1 (2) contains virtual track
	var virtCD1Entries []string
	code = v.Readdir("/MultiWithRealCD1/CD1 (2)", func(name string, stat *fuse.Stat_t, ofst int64) bool {
		virtCD1Entries = append(virtCD1Entries, name)
		return true
	}, 0, 0)
	if code != 0 {
		t.Fatalf("Readdir /MultiWithRealCD1/CD1 (2) failed: %d", code)
	}
	assertContains(t, virtCD1Entries, "01. Track 1.flac")

	// Check single-album directory
	var singleEntries []string
	code = v.Readdir("/MonolithCollision", func(name string, stat *fuse.Stat_t, ofst int64) bool {
		singleEntries = append(singleEntries, name)
		return true
	}, 0, 0)
	if code != 0 {
		t.Fatalf("Readdir MonolithCollision failed: %d", code)
	}

	// Hidden monolith "01. Intro.flac" must NOT be listed
	assertNotContains(t, singleEntries, "01. Intro.flac")
	// Virtual track must have received suffix "(2)" and be listed!
	assertContains(t, singleEntries, "01. Intro (2).flac")

	// Getattr on virtual track must succeed
	var st fuse.Stat_t
	if code := v.Getattr("/MonolithCollision/01. Intro (2).flac", &st, 0); code != 0 {
		t.Errorf("Getattr on virtual track with suffix failed: %d", code)
	}
	// Getattr on hidden monolith must return ENOENT
	if code := v.Getattr("/MonolithCollision/01. Intro.flac", &st, 0); code != -fuse.ENOENT {
		t.Errorf("expected ENOENT for hidden monolith, got %d", code)
	}
}

type testMockCutter struct {
	cutCount int
	lastReq  cutter.TrackRequest
}

func (m *testMockCutter) Cut(ctx context.Context, req cutter.TrackRequest, outputPath string) error {
	m.cutCount++
	m.lastReq = req
	return os.WriteFile(outputPath, []byte("REAL_SLICED_AUDIO_BYTES_FROM_CUTTER"), 0644)
}

func TestVFS_AudioSlicingWithCutter(t *testing.T) {
	tmpDir := t.TempDir()
	albumDir := filepath.Join(tmpDir, "TestAlbum")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatal(err)
	}

	cueContent := `TITLE "Test Album"
PERFORMER "Test Artist"
FILE "audio.flac" WAVE
  TRACK 01 AUDIO
    TITLE "First Track"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Second Track"
    INDEX 01 02:00:00`

	if err := os.WriteFile(filepath.Join(albumDir, "album.cue"), []byte(cueContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "audio.flac"), make([]byte, 1024*1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "cover.jpg"), []byte("COVER_ART"), 0644); err != nil {
		t.Fatal(err)
	}

	mock := &testMockCutter{}
	cutterMgr, err := cutter.NewManager(cutter.Options{
		Cutter: mock,
		TTL:    1 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	v := New(Options{
		SourceRoot: tmpDir,
		Slicer:     cutterMgr,
	})
	defer v.Destroy()

	// 1. Initial Getattr reports estimated size
	var st fuse.Stat_t
	code := v.Getattr("/TestAlbum/01. First Track.flac", &st, 0)
	if code != 0 {
		t.Fatalf("Getattr before slice failed: %d", code)
	}
	if st.Size <= 0 {
		t.Errorf("expected positive estimated size, got %d", st.Size)
	}

	// 2. Open virtual track - does NOT slice eagerly (lazy slicing on Read!)
	openCode, fh := v.Open("/TestAlbum/01. First Track.flac", fuse.O_RDONLY)
	if openCode != 0 {
		t.Fatalf("Open virtual track failed: %d", openCode)
	}
	if mock.cutCount != 0 {
		t.Fatalf("expected cutter NOT to be called on Open, got %d", mock.cutCount)
	}

	// 3. Read reads sliced audio data - triggers slice lazily
	buf := make([]byte, 100)
	n := v.Read("/TestAlbum/01. First Track.flac", buf, 0, fh)
	expectedData := "REAL_SLICED_AUDIO_BYTES_FROM_CUTTER"
	if n != len(expectedData) || string(buf[:n]) != expectedData {
		t.Errorf("expected %q, got %q", expectedData, string(buf[:n]))
	}

	if mock.cutCount != 1 {
		t.Fatalf("expected cutter to be called 1 time on Read, got %d", mock.cutCount)
	}
	if mock.lastReq.Tag("title") != "First Track" {
		t.Errorf("expected Title 'First Track', got %q", mock.lastReq.Tag("title"))
	}
	if mock.lastReq.Tag("album") != "Test Album" {
		t.Errorf("expected Album 'Test Album', got %q", mock.lastReq.Tag("album"))
	}
	if mock.lastReq.ArtworkPath != filepath.Join(albumDir, "cover.jpg") {
		t.Errorf("expected ArtworkPath to cover.jpg, got %q", mock.lastReq.ArtworkPath)
	}

	// 4. Getattr now reports exact size of sliced file!
	code = v.Getattr("/TestAlbum/01. First Track.flac", &st, 0)
	if code != 0 {
		t.Fatalf("Getattr after slice failed: %d", code)
	}
	if st.Size != int64(len(expectedData)) {
		t.Errorf("expected exact size %d, got %d", len(expectedData), st.Size)
	}

	// 5. Concurrent open of the same track reuses existing cache (cutter not called again)
	openCode2, fh2 := v.Open("/TestAlbum/01. First Track.flac", fuse.O_RDONLY)
	if openCode2 != 0 {
		t.Fatalf("second Open failed: %d", openCode2)
	}
	if mock.cutCount != 1 {
		t.Errorf("expected cutCount to remain 1, got %d", mock.cutCount)
	}

	// Release both handles
	v.Release("/TestAlbum/01. First Track.flac", fh)
	v.Release("/TestAlbum/01. First Track.flac", fh2)
}

func TestVFS_UnicodeNormalization(t *testing.T) {
	tmpDir := t.TempDir()
	albumDir := filepath.Join(tmpDir, "Сплин 2002 Акустика")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cueContent := `TITLE "Акустика"
PERFORMER "Сплин"
FILE "audio.flac" WAVE
  TRACK 01 AUDIO
    TITLE "За стеной"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Бонни и Клайд"
    INDEX 01 03:00:00
`
	if err := os.WriteFile(filepath.Join(albumDir, "album.cue"), []byte(cueContent), 0644); err != nil {
		t.Fatalf("write cue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "audio.flac"), make([]byte, 1024), 0644); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	cutterMgr, err := cutter.NewManager(cutter.Options{
		Cutter: &testMockCutter{},
		TTL:    1 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	v := New(Options{
		SourceRoot: tmpDir,
		Slicer:     cutterMgr,
	})
	defer v.Destroy()

	// 1. Readdir on albumDir
	var names []string
	code := v.Readdir("/Сплин 2002 Акустика", func(name string, stat *fuse.Stat_t, ofst int64) bool {
		names = append(names, name)
		return true
	}, 0, 0)
	if code != 0 {
		t.Fatalf("Readdir returned %d, expected 0", code)
	}

	// 2. Lookup using macOS NFD paths
	// In NFD, 'й' is decomposed into U+0438 (и) + U+0306 (кратка)
	nfdTrack1 := norm.NFD.String("/Сплин 2002 Акустика/01. За стеной.flac")
	nfdTrack2 := norm.NFD.String("/Сплин 2002 Акустика/02. Бонни и Клайд.flac")

	// Ensure our test string is actually decomposed
	if nfdTrack1 == "/Сплин 2002 Акустика/01. За стеной.flac" {
		t.Fatal("norm.NFD did not decompose unicode string")
	}

	var st fuse.Stat_t
	code = v.Getattr(nfdTrack1, &st, 0)
	if code != 0 {
		t.Errorf("Getattr(nfdTrack1) returned %d, expected 0 (ENOENT bug reproduced)", code)
	}

	code = v.Getattr(nfdTrack2, &st, 0)
	if code != 0 {
		t.Errorf("Getattr(nfdTrack2) returned %d, expected 0", code)
	}

	// 3. Open and Read using NFD path
	openCode, fh := v.Open(nfdTrack1, fuse.O_RDONLY)
	if openCode != 0 {
		t.Fatalf("Open(nfdTrack1) returned %d, expected 0", openCode)
	}

	buf := make([]byte, 16)
	readBytes := v.Read(nfdTrack1, buf, 0, fh)
	if readBytes <= 0 {
		t.Errorf("Read(nfdTrack1) returned %d bytes, expected > 0", readBytes)
	}

	relCode := v.Release(nfdTrack1, fh)
	if relCode != 0 {
		t.Errorf("Release(nfdTrack1) returned %d, expected 0", relCode)
	}
}
