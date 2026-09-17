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
	FileName  string // Virtual filename presented in FUSE (e.g. "01. Intro.flac")

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
	TracksByName    map[string]*VirtualTrack
}

// AlbumCache caches parsed album metadata to prevent re-parsing on every FUSE call.
type AlbumCache struct {
	mu     sync.RWMutex
	albums map[string]*cachedAlbum // key: directory path
}

type cachedAlbum struct {
	cueModTime time.Time
	cueSize    int64
	album      *Album // nil if directory has no monolithic CUE
}

func NewAlbumCache() *AlbumCache {
	return &AlbumCache{
		albums: make(map[string]*cachedAlbum),
	}
}

// GetAlbum returns the cached Album for dirPath, or parses it if needed.
func (c *AlbumCache) GetAlbum(dirPath string) (*Album, error) {
	c.mu.RLock()
	cached, ok := c.albums[dirPath]
	c.mu.RUnlock()

	// Find any .cue file in this directory
	cuePath, err := findCueFile(dirPath)
	if err != nil || cuePath == "" {
		c.mu.Lock()
		c.albums[dirPath] = &cachedAlbum{}
		c.mu.Unlock()
		return nil, nil
	}

	fi, err := os.Stat(cuePath)
	if err != nil {
		return nil, err
	}

	if ok && cached != nil && cached.cueModTime.Equal(fi.ModTime()) && cached.cueSize == fi.Size() {
		return cached.album, nil
	}

	// Parse CUE file
	sheet, err := cue.ParseFile(cuePath)
	if err != nil {
		c.mu.Lock()
		c.albums[dirPath] = &cachedAlbum{cueModTime: fi.ModTime(), cueSize: fi.Size(), album: nil}
		c.mu.Unlock()
		return nil, fmt.Errorf("parse cue %s: %w", cuePath, err)
	}

	// Check if this is a monolithic album (or has tracks)
	tracks := sheet.AllTracks()
	if len(tracks) == 0 {
		c.mu.Lock()
		c.albums[dirPath] = &cachedAlbum{cueModTime: fi.ModTime(), cueSize: fi.Size(), album: nil}
		c.mu.Unlock()
		return nil, nil
	}

	// If CUE references multiple audio files (already split per-track), don't virtualize
	if len(sheet.Files) > 1 {
		c.mu.Lock()
		c.albums[dirPath] = &cachedAlbum{cueModTime: fi.ModTime(), cueSize: fi.Size(), album: nil}
		c.mu.Unlock()
		return nil, nil
	}

	// Resolve the real monolithic audio file
	declaredFile := ""
	if len(sheet.Files) == 1 {
		declaredFile = sheet.Files[0].Name
	}
	audioPath := resolveAudioFile(dirPath, cuePath, declaredFile)
	if audioPath == "" {
		// Audio file not found
		c.mu.Lock()
		c.albums[dirPath] = &cachedAlbum{cueModTime: fi.ModTime(), cueSize: fi.Size(), album: nil}
		c.mu.Unlock()
		return nil, nil
	}

	audioFi, err := os.Stat(audioPath)
	if err != nil {
		return nil, err
	}
	totalAudioSize := audioFi.Size()

	// Build VirtualTracks
	album := &Album{
		CuePath:         cuePath,
		SourceAudioPath: audioPath,
		Sheet:           sheet,
		Tracks:          make([]VirtualTrack, len(tracks)),
		TracksByName:    make(map[string]*VirtualTrack),
	}

	ext := filepath.Ext(audioPath)
	if ext == "" {
		ext = ".flac"
	}

	// Calculate rough sizes
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

		vName := formatTrackFilename(tr.Num, tr.Performer, sheet.Performer, title, ext)

		// Estimate size
		estimatedSize := int64(0)
		if tr.End > tr.Start && totalKnownDuration > 0 {
			duration := tr.End - tr.Start
			estimatedSize = int64(float64(totalAudioSize) * (duration / totalKnownDuration))
		} else {
			// Fallback: equal division
			estimatedSize = totalAudioSize / int64(len(tracks))
		}
		if estimatedSize <= 0 {
			estimatedSize = 1024 * 1024 // 1MB minimum fallback
		}

		vt := VirtualTrack{
			Num:             tr.Num,
			Title:           title,
			Performer:       tr.Performer,
			FileName:        vName,
			SourceAudioPath: audioPath,
			Start:           tr.Start,
			End:             tr.End,
			EstimatedSize:   estimatedSize,
		}

		album.Tracks[i] = vt
		album.TracksByName[vName] = &album.Tracks[i]
	}

	c.mu.Lock()
	c.albums[dirPath] = &cachedAlbum{
		cueModTime: fi.ModTime(),
		cueSize:    fi.Size(),
		album:      album,
	}
	c.mu.Unlock()

	return album, nil
}

func findCueFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".cue") {
			return filepath.Join(dir, entry.Name()), nil
		}
	}
	return "", nil
}

func resolveAudioFile(dir, cuePath, declaredName string) string {
	// 1. Declared name in CUE
	if declaredName != "" {
		p := filepath.Join(dir, declaredName)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		p = filepath.Join(dir, filepath.Base(declaredName))
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}

		// Try same stem with audio extensions
		stem := strings.TrimSuffix(filepath.Base(declaredName), filepath.Ext(declaredName))
		for _, ext := range []string{".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3", ".FLAC", ".WAV"} {
			cand := filepath.Join(dir, stem+ext)
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				return cand
			}
		}
	}

	// 2. Same basename as CUE file
	cueBase := filepath.Base(cuePath)
	cueStem := strings.TrimSuffix(cueBase, filepath.Ext(cueBase))
	for _, ext := range []string{".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3", ".FLAC", ".WAV"} {
		cand := filepath.Join(dir, cueStem+ext)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}

	// 3. Look for any audio file in directory if only 1 exists
	entries, err := os.ReadDir(dir)
	if err == nil {
		var audioCandidates []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			switch ext {
			case ".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3":
				audioCandidates = append(audioCandidates, filepath.Join(dir, e.Name()))
			}
		}
		if len(audioCandidates) == 1 {
			return audioCandidates[0]
		}
	}

	return ""
}

func formatTrackFilename(num int, trackArtist, albumArtist, title, ext string) string {
	cleanTitle := sanitizeFilename(title)

	// If track artist differs from album artist, format as: "01. Artist - Title.flac"
	if trackArtist != "" && albumArtist != "" && !strings.EqualFold(trackArtist, albumArtist) {
		cleanArtist := sanitizeFilename(trackArtist)
		return fmt.Sprintf("%02d. %s - %s%s", num, cleanArtist, cleanTitle, ext)
	}

	return fmt.Sprintf("%02d. %s%s", num, cleanTitle, ext)
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
