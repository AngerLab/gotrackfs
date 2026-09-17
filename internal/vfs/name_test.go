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

func TestDetermineDiscPrefix(t *testing.T) {
	// Case 1: DISCNUMBER in sheet
	album1 := &Album{
		CuePath: "/music/album.cue",
		Sheet:   &cue.Sheet{DiscNumber: "2"},
	}
	if prefix := determineDiscPrefix(album1, 1); prefix != "2-" {
		t.Errorf("expected '2-', got %q", prefix)
	}

	// Case 2: Disc in filename
	album2 := &Album{
		CuePath: "/music/CD 03.cue",
		Sheet:   &cue.Sheet{},
	}
	if prefix := determineDiscPrefix(album2, 1); prefix != "03-" {
		t.Errorf("expected '03-', got %q", prefix)
	}

	// Case 3: Default fallback
	album3 := &Album{
		CuePath: "/music/album.cue",
		Sheet:   &cue.Sheet{},
	}
	if prefix := determineDiscPrefix(album3, 4); prefix != "4-" {
		t.Errorf("expected '4-', got %q", prefix)
	}
}

func TestFormatTrackFilename(t *testing.T) {
	// Same artist as album
	name1 := formatTrackFilename(1, "Pink Floyd", "Pink Floyd", "Time", "", ".flac")
	if name1 != "01. Time.flac" {
		t.Errorf("expected '01. Time.flac', got %q", name1)
	}

	// Various artists (soundtrack / compilation)
	name2 := formatTrackFilename(5, "David Bowie", "Various Artists", "Heroes", "1-", ".flac")
	if name2 != "1-05. David Bowie - Heroes.flac" {
		t.Errorf("expected '1-05. David Bowie - Heroes.flac', got %q", name2)
	}
}
