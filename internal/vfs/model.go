package vfs

import "gotrackfs/internal/cue"

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
