package main

// Album represents a single release: one CUE sheet + monolithic audio image (FLAC, APE, WAV, etc.)
type Album struct {
	Title      string  // Album title (TITLE)
	Artist     string  // Album artist (PERFORMER)
	Year       string  // Release year or date (DATE)
	Genre      string  // Music genre (GENRE)
	CoverPath  string  // Absolute path to cover.jpg / folder.jpg on disk (empty if none)
	SourcePath string  // Path to the monolithic audio file (e.g. image.flac)
	Duration   float64 // Total duration of the audio image in seconds (probed via ffprobe)
	Tracks     []Track // Slice of individual tracks parsed from CUE
}

// Track represents an individual virtual audio track.
type Track struct {
	Num      int     // Track number (1, 2, 3...)
	Title    string  // Track title
	Artist   string  // Track artist if different from Album.Artist (empty if inherited)
	Start    float64 // Start offset in seconds
	End      float64 // End offset in seconds
	Size     uint64  // Estimated size of rendered virtual FLAC file in bytes
	HasIndex bool    // True if a valid INDEX 01 was found
}

// EffectiveArtist returns the track-specific artist, falling back to the album artist.
func (t *Track) EffectiveArtist(a *Album) string {
	if t.Artist != "" {
		return t.Artist
	}
	return a.Artist
}
