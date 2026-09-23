package vfs

import "testing"

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
