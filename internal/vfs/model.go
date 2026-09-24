package vfs

import (
	"github.com/AngerLab/gotrackfs/internal/cue"
	"github.com/AngerLab/gotrackfs/internal/track"
)

// VirtualTrack represents a single audio track virtualized from a monolithic audio file.
type VirtualTrack struct {
	Num           int
	Title         string
	Performer     string
	FileName      string // Virtual filename presented in FUSE (e.g. "01. Intro.flac" or "1-01. Hey You.flac")
	EstimatedSize int64  // Estimated size in bytes
	CutterKey     string // Precomputed unique cache key for the slicer

	Slice track.Slice // Audio slice parameters: offsets, source facts, tags, artwork
}

// Album represents a parsed album with its virtualized tracks.
type Album struct {
	CuePath          string
	SourceAudioPaths []string // All source audio files for this album
	Sheet            *cue.Sheet
	Tracks           []VirtualTrack
}

// DirState holds the virtualized state of a single directory,
// which can contain 0, 1, or multiple albums (e.g. CD1 + CD2).
// All fields are the ones the lookup/readdir paths actually consume;
// album-level metadata lives in the collectAlbums result, not here.
type DirState struct {
	TracksByName    map[string]*VirtualTrack
	HiddenMonoliths map[string]bool      // Basenames of monolithic files to hide
	Subdirs         map[string]*DirState // Virtual subdirectories (e.g. "CD1" -> sub-DirState)
	MirroredFiles   map[string]string    // Virtual filename -> real path (e.g. "cover.jpg" -> "/path/to/cover.jpg")
}
