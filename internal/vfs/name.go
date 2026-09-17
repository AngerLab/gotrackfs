package vfs

import (
	"fmt"
	"path/filepath"
	"strings"
)

// determineDiscPrefix extracts or formats the disc number prefix (e.g. "1-", "2-") for multi-disc folders.
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

// formatTrackFilename generates the virtual track filename.
func formatTrackFilename(num int, trackArtist, albumArtist, title, discPrefix, ext string) string {
	cleanTitle := sanitizeFilename(title)

	// If track artist differs from album artist, format as: "[discPrefix]01. Artist - Title.flac"
	if trackArtist != "" && albumArtist != "" && !strings.EqualFold(trackArtist, albumArtist) {
		cleanArtist := sanitizeFilename(trackArtist)
		return fmt.Sprintf("%s%02d. %s - %s%s", discPrefix, num, cleanArtist, cleanTitle, ext)
	}

	return fmt.Sprintf("%s%02d. %s%s", discPrefix, num, cleanTitle, ext)
}

// sanitizeFilename sanitizes title and artist strings for safe use as filesystem names across platforms.
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
