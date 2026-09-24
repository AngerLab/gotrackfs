package audio

import "strings"

// AudioExtensions is the canonical set of recognized audio container
// extensions. It is a membership set, kept lowercase because matching is
// case-insensitive (IsAudioExt uses EqualFold), so uppercase variants such
// as .FLAC need no explicit entries. Resolution priority is not a concern
// of this package: the resolver's probe order lives in vfs (resolve.go).
//
// .wave is deliberately included: IsWAVExt has always accepted it (the
// prober parses WAV containers), so membership now matches what the prober
// handles. On main, .wave files were never resolved as cue sources — the
// resolver only probes and counts extensions from this list.
var AudioExtensions = []string{".flac", ".wav", ".wave", ".ape", ".wv", ".m4a", ".mp3"}

// IsAudioExt reports whether ext (e.g. ".Flac") is a recognized audio extension.
func IsAudioExt(ext string) bool {
	return hasExt(AudioExtensions, ext)
}

// IsFLACExt reports whether ext is a FLAC extension (".flac", case-insensitive).
func IsFLACExt(ext string) bool {
	return strings.EqualFold(ext, ".flac")
}

// IsWAVExt reports whether ext is a WAV extension (".wav" or ".wave", case-insensitive).
func IsWAVExt(ext string) bool {
	return strings.EqualFold(ext, ".wav") || strings.EqualFold(ext, ".wave")
}

func hasExt(list []string, ext string) bool {
	for _, e := range list {
		if strings.EqualFold(ext, e) {
			return true
		}
	}
	return false
}
