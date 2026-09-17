package vfs

import (
	"testing"

	"gotrackfs/internal/cue"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Normal Title", "Normal Title"},
		{"AC/DC", "AC-DC"},
		{"What? Why*", "What Why"},
		{"Echoes: Part 1", "Echoes - Part 1"},
		{"<Hidden> \"Quotes\" | Pipe", "Hidden Quotes - Pipe"},
		{"   ", "Track"},
		{"", "Track"},
	}

	for _, tc := range tests {
		got := sanitizeFilename(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeFilename(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestDetermineDiscDirName(t *testing.T) {
	// Case 1: DISCNUMBER in sheet
	album1 := &Album{
		CuePath: "/music/album.cue",
		Sheet:   &cue.Sheet{DiscNumber: "2"},
	}
	if name := determineDiscDirName(album1, 1); name != "CD2" {
		t.Errorf("expected 'CD2', got %q", name)
	}

	// Case 2: Disc in filename
	album2 := &Album{
		CuePath: "/music/CD 03.cue",
		Sheet:   &cue.Sheet{},
	}
	if name := determineDiscDirName(album2, 1); name != "CD03" {
		t.Errorf("expected 'CD03', got %q", name)
	}

	// Case 3: Default fallback
	album3 := &Album{
		CuePath: "/music/album.cue",
		Sheet:   &cue.Sheet{},
	}
	if name := determineDiscDirName(album3, 4); name != "CD4" {
		t.Errorf("expected 'CD4', got %q", name)
	}
}

func TestFormatTrackFilename(t *testing.T) {
	// Same artist as album
	name1 := formatTrackFilename(1, "Pink Floyd", "Pink Floyd", "Time", ".flac")
	if name1 != "01. Time.flac" {
		t.Errorf("expected '01. Time.flac', got %q", name1)
	}

	// Various artists (soundtrack / compilation)
	name2 := formatTrackFilename(5, "David Bowie", "Various Artists", "Heroes", ".flac")
	if name2 != "05. David Bowie - Heroes.flac" {
		t.Errorf("expected '05. David Bowie - Heroes.flac', got %q", name2)
	}
}
