package vfs

import (
	"fmt"
	"path/filepath"
	"strings"
)

// determineDiscDirName extracts or formats the virtual subdirectory name (e.g. "CD1", "CD2") for multi-album folders.
func determineDiscDirName(album *Album, defaultDiscNum int) string {
	if album.Sheet.DiscNumber != "" {
		disc := strings.TrimSpace(album.Sheet.DiscNumber)
		discLower := strings.ToLower(disc)
		if strings.HasPrefix(discLower, "cd") || strings.HasPrefix(discLower, "disc") {
			return disc
		}
		return "CD" + disc
	}

	cueStem := strings.TrimSuffix(filepath.Base(album.CuePath), filepath.Ext(album.CuePath))
	cueStemLower := strings.ToLower(cueStem)
	for _, prefix := range []string{"cd", "disc", "disk"} {
		if strings.HasPrefix(cueStemLower, prefix) {
			trimmed := strings.TrimSpace(cueStem[len(prefix):])
			trimmed = strings.TrimLeft(trimmed, "_- ")
			if trimmed != "" {
				return "CD" + trimmed
			}
		}
	}

	return fmt.Sprintf("CD%d", defaultDiscNum)
}

// addFilenameSuffix inserts a numerical suffix before the file extension, e.g. "01. Title.flac" -> "01. Title (2).flac".
func addFilenameSuffix(filename string, suffix int) string {
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	return fmt.Sprintf("%s (%d)%s", stem, suffix, ext)
}

// formatTrackFilename generates the virtual track filename.
func formatTrackFilename(num int, trackArtist, albumArtist, title, ext string) string {
	cleanTitle := sanitizeFilename(title)

	// If track artist differs from album artist, format as: "01. Artist - Title.flac"
	if trackArtist != "" && albumArtist != "" && !strings.EqualFold(trackArtist, albumArtist) {
		cleanArtist := sanitizeFilename(trackArtist)
		return fmt.Sprintf("%02d. %s - %s%s", num, cleanArtist, cleanTitle, ext)
	}

	return fmt.Sprintf("%02d. %s%s", num, cleanTitle, ext)
}

var filenameReplacer = strings.NewReplacer(
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

// sanitizeFilename sanitizes title and artist strings for safe use as filesystem names across platforms.
func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	res := filenameReplacer.Replace(s)
	res = strings.TrimSpace(res)
	if res == "" {
		return "Track"
	}
	return res
}
