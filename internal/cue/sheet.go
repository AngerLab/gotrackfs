package cue

// Sheet represents parsed CUE sheet metadata and tracks.
type Sheet struct {
	Catalog    string   // Media catalog / barcode number
	CdTextFile string   // Associated CD-Text file
	Title      string   // Album title
	Performer  string   // Album artist
	Songwriter string   // Album songwriter
	Date       string   // Release date/year
	Genre      string   // Music genre
	DiscNumber string   // Disc number (from REM DISCNUMBER)
	TotalDiscs string   // Total discs (from REM TOTALDISCS)
	Comments   []string // Raw comments and unhandled REM lines
	Files      []File   // Audio files declared in CUE
}

// File represents a FILE entry in a CUE sheet.
type File struct {
	Name   string  // Filename or relative path as specified in CUE
	Type   string  // File format (WAVE, MP3, AIFF, etc.)
	Tracks []Track // Tracks belonging to this audio file
}

// Track represents a single audio track in a CUE sheet.
type Track struct {
	Num        int      // Track number (1-99)
	DataType   string   // Track data type (typically AUDIO)
	Title      string   // Track title
	Performer  string   // Track artist
	Songwriter string   // Track songwriter
	Isrc       string   // International Standard Recording Code
	Flags      []string // Flags (DCP, 4CH, PRE, SCMS)
	Start      float64  // Start offset in seconds (from INDEX 01)
	End        float64  // End offset in seconds (next track start or total duration)
	PreGap     float64  // Pre-gap offset in seconds (from INDEX 00 or PREGAP)
	HasIndex   bool     // True if a valid INDEX 01 was encountered
}

// TotalTracks returns the total number of tracks across all files in the sheet.
func (s *Sheet) TotalTracks() int {
	total := 0
	for _, f := range s.Files {
		total += len(f.Tracks)
	}
	return total
}

// AllTracks flattens all tracks across files into a single slice.
func (s *Sheet) AllTracks() []Track {
	var tracks []Track
	for _, f := range s.Files {
		tracks = append(tracks, f.Tracks...)
	}
	return tracks
}
