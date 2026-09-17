package vfs

import (
	"os"
	"path/filepath"
	"strings"
)

// findAllCueFiles returns all .cue files in the specified directory.
func findAllCueFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var cueFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".cue") {
			cueFiles = append(cueFiles, filepath.Join(dir, entry.Name()))
		}
	}
	return cueFiles, nil
}

// resolveAudioFileForCue resolves the matching monolithic audio file for a CUE sheet.
// It checks declared filename, matching stem with audio extensions, and single unclaimed audio.
func resolveAudioFileForCue(dir, cuePath, declaredName string, claimed map[string]bool) string {
	// 1. Declared name in CUE
	if declaredName != "" {
		p := filepath.Join(dir, declaredName)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && !claimed[p] {
			return p
		}
		p = filepath.Join(dir, filepath.Base(declaredName))
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && !claimed[p] {
			return p
		}

		// Stem + audio extensions
		stem := strings.TrimSuffix(filepath.Base(declaredName), filepath.Ext(declaredName))
		for _, ext := range []string{".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3", ".FLAC", ".WAV"} {
			cand := filepath.Join(dir, stem+ext)
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && !claimed[cand] {
				return cand
			}
		}
	}

	// 2. Same stem as CUE file
	cueBase := filepath.Base(cuePath)
	cueStem := strings.TrimSuffix(cueBase, filepath.Ext(cueBase))
	for _, ext := range []string{".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3", ".FLAC", ".WAV"} {
		cand := filepath.Join(dir, cueStem+ext)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && !claimed[cand] {
			return cand
		}
	}

	// 3. If only one unclaimed audio file exists in directory
	entries, err := os.ReadDir(dir)
	if err == nil {
		var unclaimed []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			switch ext {
			case ".flac", ".wav", ".ape", ".wv", ".m4a", ".mp3":
				cand := filepath.Join(dir, e.Name())
				if !claimed[cand] {
					unclaimed = append(unclaimed, cand)
				}
			}
		}
		if len(unclaimed) == 1 {
			return unclaimed[0]
		}
	}

	return ""
}
