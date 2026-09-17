package vfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gotrackfs/internal/cue"
)

// VirtualTrack represents a single audio track virtualized from a monolithic audio file.
type VirtualTrack struct {
	Num       int
	Title     string
	Performer string
	FileName  string // Virtual filename presented in FUSE (e.g. "01. Intro.flac" or "1-01. Hey You.flac")

	SourceAudioPath string  // Real path to the monolithic audio file
	Start           float64 // Start offset in seconds
	End             float64 // End offset in seconds (0 for last track if total duration unknown)
	EstimatedSize   int64   // Estimated size in bytes
}

// Album represents a parsed album with its virtualized tracks.
type Album struct {
	CuePath         string
	SourceAudioPath string
	Sheet           *cue.Sheet
	Tracks          []VirtualTrack
}

// DirState holds the virtualized state of a single directory,
// which can contain 0, 1, or multiple albums (e.g. CD1 + CD2).
type DirState struct {
	Albums          []*Album
	TracksByName    map[string]*VirtualTrack
	HiddenMonoliths map[string]bool // Basenames of monolithic files to hide
}

// AlbumCache caches parsed directory states to prevent re-parsing on every FUSE call.
type AlbumCache struct {
	mu   sync.RWMutex
	dirs map[string]*cachedDir
}

type cachedDir struct {
	cueModTimes   map[string]time.Time // cuePath -> modTime
	cueSizes      map[string]int64     // cuePath -> size
	audioModTimes map[string]time.Time // audioPath -> modTime
	audioSizes    map[string]int64     // audioPath -> size
	state         *DirState
}

func NewAlbumCache() *AlbumCache {
	return &AlbumCache{
		dirs: make(map[string]*cachedDir),
	}
}

// GetDirState returns the DirState for dirPath, or parses it if needed.
func (c *AlbumCache) GetDirState(dirPath string) (*DirState, error) {
	// Find all .cue files in this directory
	cueFiles, err := findAllCueFiles(dirPath)
	if err != nil || len(cueFiles) == 0 {
		return nil, nil
	}

	// Read current mtimes and sizes of all .cue files
	currentModTimes := make(map[string]time.Time, len(cueFiles))
	currentSizes := make(map[string]int64, len(cueFiles))
	for _, cp := range cueFiles {
		fi, err := os.Stat(cp)
		if err != nil {
			return nil, err
		}
		currentModTimes[cp] = fi.ModTime()
		currentSizes[cp] = fi.Size()
	}

	c.mu.RLock()
	cached, ok := c.dirs[dirPath]
	c.mu.RUnlock()

	if ok && cached != nil && isCacheValid(cached, currentModTimes, currentSizes) {
		return cached.state, nil
	}

	// Parse all CUE files and build DirState
	var albums []*Album
	claimedAudios := make(map[string]bool)

	for _, cuePath := range cueFiles {
		sheet, err := cue.ParseFile(cuePath)
		if err != nil {
			continue
		}

		tracks := sheet.AllTracks()
		if len(tracks) == 0 {
			continue
		}

		// Multi-file CUEs (already split per track) are left untouched
		if len(sheet.Files) > 1 {
			continue
		}

		declaredFile := ""
		if len(sheet.Files) == 1 {
			declaredFile = sheet.Files[0].Name
		}

		audioPath := resolveAudioFileForCue(dirPath, cuePath, declaredFile, claimedAudios)
		if audioPath == "" {
			continue
		}

		audioFi, err := os.Stat(audioPath)
		if err != nil {
			continue
		}
		claimedAudios[audioPath] = true

		album := &Album{
			CuePath:         cuePath,
			SourceAudioPath: audioPath,
			Sheet:           sheet,
			Tracks:          make([]VirtualTrack, len(tracks)),
		}

		totalAudioSize := audioFi.Size()
		ext := filepath.Ext(audioPath)
		if ext == "" {
			ext = ".flac"
		}

		totalKnownDuration := 0.0
		for _, tr := range tracks {
			if tr.End > tr.Start {
				totalKnownDuration += (tr.End - tr.Start)
			}
		}

		for i, tr := range tracks {
			title := tr.Title
			if title == "" {
				title = fmt.Sprintf("Track %02d", tr.Num)
			}

			estimatedSize := int64(0)
			if tr.End > tr.Start && totalKnownDuration > 0 {
				duration := tr.End - tr.Start
				estimatedSize = int64(float64(totalAudioSize) * (duration / totalKnownDuration))
			} else {
				estimatedSize = totalAudioSize / int64(len(tracks))
			}
			if estimatedSize <= 0 {
				estimatedSize = 1024 * 1024
			}

			album.Tracks[i] = VirtualTrack{
				Num:             tr.Num,
				Title:           title,
				Performer:       tr.Performer,
				SourceAudioPath: audioPath,
				Start:           tr.Start,
				End:             tr.End,
				EstimatedSize:   estimatedSize,
			}
		}

		albums = append(albums, album)
	}

	if len(albums) == 0 {
		c.mu.Lock()
		c.dirs[dirPath] = &cachedDir{
			cueModTimes: currentModTimes,
			cueSizes:    currentSizes,
			state:       nil,
		}
		c.mu.Unlock()
		return nil, nil
	}

	dirState := &DirState{
		Albums:          albums,
		TracksByName:    make(map[string]*VirtualTrack),
		HiddenMonoliths: make(map[string]bool),
	}

	// TODO: Support virtual subdirectories mode (e.g. via --split-discs flag).
	// When enabled for multi-album folders (len(albums) > 1), instead of flattening
	// with disc prefixes (1-01..., 2-01...), generate virtual subdirectories (CD1/, CD2/)
	// and mirror cover.jpg into each.
	isMultiAlbum := len(albums) > 1

	for albumIdx, album := range albums {
		dirState.HiddenMonoliths[filepath.Base(album.SourceAudioPath)] = true

		discPrefix := ""
		if isMultiAlbum {
			discPrefix = determineDiscPrefix(album, albumIdx+1)
		}

		ext := filepath.Ext(album.SourceAudioPath)
		if ext == "" {
			ext = ".flac"
		}

		for i := range album.Tracks {
			vt := &album.Tracks[i]
			vt.FileName = formatTrackFilename(vt.Num, vt.Performer, album.Sheet.Performer, vt.Title, discPrefix, ext)
			dirState.TracksByName[vt.FileName] = vt
		}
	}

	audioModTimes := make(map[string]time.Time, len(albums))
	audioSizes := make(map[string]int64, len(albums))
	for _, album := range albums {
		if fi, err := os.Stat(album.SourceAudioPath); err == nil {
			audioModTimes[album.SourceAudioPath] = fi.ModTime()
			audioSizes[album.SourceAudioPath] = fi.Size()
		}
	}

	c.mu.Lock()
	c.dirs[dirPath] = &cachedDir{
		cueModTimes:   currentModTimes,
		cueSizes:      currentSizes,
		audioModTimes: audioModTimes,
		audioSizes:    audioSizes,
		state:         dirState,
	}
	c.mu.Unlock()

	return dirState, nil
}

func isCacheValid(cached *cachedDir, currentModTimes map[string]time.Time, currentSizes map[string]int64) bool {
	if len(cached.cueModTimes) != len(currentModTimes) {
		return false
	}
	for cp, mtime := range currentModTimes {
		oldMtime, ok := cached.cueModTimes[cp]
		if !ok || !oldMtime.Equal(mtime) {
			return false
		}
		if cached.cueSizes[cp] != currentSizes[cp] {
			return false
		}
	}
	// Check underlying audio files for mtime or size changes
	for audioPath, oldMtime := range cached.audioModTimes {
		fi, err := os.Stat(audioPath)
		if err != nil || !fi.ModTime().Equal(oldMtime) || fi.Size() != cached.audioSizes[audioPath] {
			return false
		}
	}
	return true
}

func findAllCueFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var cueFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".cue") {
			cueFiles = append(cueFiles, filepath.Join(dir, entry.Name()))
		}
	}
	return cueFiles, nil
}

func resolveAudioFileForCue(dir, cuePath, declaredName string, claimed map[string]bool) string {
	// 1. Declared name in CUE
	if declaredName != "" {
		p := filepath.Join(dir, declaredName)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && !claimed[p] {
			return p
		}
		p = filepath.Join(dir, filepath.Base(declaredName))
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && !claimed[p] {
			return p
		}

		// Stem + audio extensions
		stem := strings.TrimSuffix(filepath.Base(declaredName), filepath.Ext(declaredName))
		for _, ext := range []string{".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3", ".FLAC", ".WAV"} {
			cand := filepath.Join(dir, stem+ext)
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && !claimed[cand] {
				return cand
			}
		}
	}

	// 2. Same stem as CUE file
	cueBase := filepath.Base(cuePath)
	cueStem := strings.TrimSuffix(cueBase, filepath.Ext(cueBase))
	for _, ext := range []string{".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3", ".FLAC", ".WAV"} {
		cand := filepath.Join(dir, cueStem+ext)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && !claimed[cand] {
			return cand
		}
	}

	// 3. If only one unclaimed audio file exists in directory
	entries, err := os.ReadDir(dir)
	if err == nil {
		var unclaimed []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			switch ext {
			case ".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3":
				cand := filepath.Join(dir, e.Name())
				if !claimed[cand] {
					unclaimed = append(unclaimed, cand)
				}
			}
		}
		if len(unclaimed) == 1 {
			return unclaimed[0]
		}
	}

	return ""
}

func determineDiscPrefix(album *Album, defaultDiscNum int) string {
	if album.Sheet.DiscNumber != "" {
		return strings.TrimSpace(album.Sheet.DiscNumber) + "-"
	}

	// Try extracting from cue filename (e.g. "CD1.cue" -> "1-", "Disc 2.cue" -> "2-")
	cueStem := strings.TrimSuffix(filepath.Base(album.CuePath), filepath.Ext(album.CuePath))
	cueStemLower := strings.ToLower(cueStem)
	for _, prefix := range []string{"cd", "disc", "disk"} {
		if strings.HasPrefix(cueStemLower, prefix) {
			trimmed := strings.TrimSpace(cueStem[len(prefix):])
			trimmed = strings.TrimLeft(trimmed, "_- ")
			if trimmed != "" {
				return trimmed + "-"
			}
		}
	}

	return fmt.Sprintf("%d-", defaultDiscNum)
}

func formatTrackFilename(num int, trackArtist, albumArtist, title, discPrefix, ext string) string {
	cleanTitle := sanitizeFilename(title)

	// If track artist differs from album artist, format as: "[discPrefix]01. Artist - Title.flac"
	if trackArtist != "" && albumArtist != "" && !strings.EqualFold(trackArtist, albumArtist) {
		cleanArtist := sanitizeFilename(trackArtist)
		return fmt.Sprintf("%s%02d. %s - %s%s", discPrefix, num, cleanArtist, cleanTitle, ext)
	}

	return fmt.Sprintf("%s%02d. %s%s", discPrefix, num, cleanTitle, ext)
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", " -",
		"*", "",
		"?", "",
		"\"", "",
		"<", "",
		">", "",
		"|", "-",
	)
	res := replacer.Replace(s)
	res = strings.TrimSpace(res)
	if res == "" {
		return "Track"
	}
	return res
}
