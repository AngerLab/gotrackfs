package vfs

import (
	"testing"

	"github.com/AngerLab/gotrackfs/internal/cue"
)

func TestUniqueName_InsertsSuffixBeforeExtension(t *testing.T) {
	taken := map[string]bool{"01. Title.flac": true}
	name, collided := uniqueName("01. Title.flac", func(c string) bool { return taken[c] })
	if want := "01. Title (2).flac"; name != want || !collided {
		t.Errorf("uniqueName = %q (collided=%v), want %q", name, collided, want)
	}
}

func TestUniqueNamePlain_AppendsSuffixToWholeName(t *testing.T) {
	taken := map[string]bool{"CD1.5": true}
	name, collided := uniqueNamePlain("CD1.5", func(c string) bool { return taken[c] })
	if want := "CD1.5 (2)"; name != want || !collided {
		t.Errorf("uniqueNamePlain = %q (collided=%v), want %q", name, collided, want)
	}
}

func TestUniqueName_FreeBaseIsReturnedAsIs(t *testing.T) {
	name, collided := uniqueName("CD1", func(string) bool { return false })
	if name != "CD1" || collided {
		t.Errorf("uniqueName = %q (collided=%v), want %q untouched", name, collided, "CD1")
	}
}

// TestUniqueName_CollisionChain locks the full walk: when the base and its
// first suffixed variants are all taken, the chain extends to (3), (4), ...
func TestUniqueName_CollisionChain(t *testing.T) {
	taken := map[string]bool{
		"01. Title.flac":     true,
		"01. Title (2).flac": true,
		"01. Title (3).flac": true,
	}
	name, collided := uniqueName("01. Title.flac", func(c string) bool { return taken[c] })
	if want := "01. Title (4).flac"; name != want || !collided {
		t.Errorf("uniqueName = %q (collided=%v), want %q", name, collided, want)
	}
}

// TestUniqueNamePlain_CollisionChain is the whole-name variant of the chain
// walk: suffixes accumulate on the full name, not before an extension.
func TestUniqueNamePlain_CollisionChain(t *testing.T) {
	taken := map[string]bool{
		"CD1":     true,
		"CD1 (2)": true,
		"CD1 (3)": true,
	}
	name, collided := uniqueNamePlain("CD1", func(c string) bool { return taken[c] })
	if want := "CD1 (4)"; name != want || !collided {
		t.Errorf("uniqueNamePlain = %q (collided=%v), want %q", name, collided, want)
	}
}

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
