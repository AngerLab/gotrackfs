package audio

import "strings"

// AudioExtensions is the canonical list of audio container extensions the
// resolver and the prober agree on (lowercase; matching is case-insensitive).
// It is the single source of truth: resolveAudioFileForCue and Probe both
// derive their behaviour from it instead of maintaining divergent copies.
var AudioExtensions = []string{".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3"}

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
