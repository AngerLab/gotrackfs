package vfs

import (
	"path/filepath"
	"strings"

	"github.com/AngerLab/gotrackfs/internal/audio"
	"github.com/AngerLab/gotrackfs/internal/hostfs"
)

// findAllCueFiles returns all .cue files in the specified directory.
func findAllCueFiles(fs hostfs.FS, dir string) ([]string, error) {
	entries, err := fs.List(dir)
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

// resolveAudioByStem searches dir for a file named stem + <audio extension>,
// probing in AudioExtensions order (lowercase stems first, then .FLAC/.WAV).
func resolveAudioByStem(fs hostfs.FS, dir, stem string, claimed map[string]bool) string {
	for _, ext := range audio.AudioExtensions {
		p := filepath.Join(dir, stem+ext)
		if fi, err := fs.Stat(p); err == nil && !fi.IsDir() && !claimed[p] {
			return p
		}
	}
	return ""
}

// resolveAudioFileForCue resolves the matching monolithic audio file for a CUE sheet.
// It checks declared filename, matching stem with audio extensions, and single unclaimed audio.
func resolveAudioFileForCue(fs hostfs.FS, dir, cuePath, declaredName string, claimed map[string]bool) string {
	// 1. Declared name in CUE
	if declaredName != "" {
		p := filepath.Join(dir, declaredName)
		if fi, err := fs.Stat(p); err == nil && !fi.IsDir() && !claimed[p] {
			return p
		}
		p = filepath.Join(dir, filepath.Base(declaredName))
		if fi, err := fs.Stat(p); err == nil && !fi.IsDir() && !claimed[p] {
			return p
		}

		// Stem + audio extensions
		stem := strings.TrimSuffix(filepath.Base(declaredName), filepath.Ext(declaredName))
		if p := resolveAudioByStem(fs, dir, stem, claimed); p != "" {
			return p
		}
	}

	// 2. Same stem as CUE file
	cueBase := filepath.Base(cuePath)
	cueStem := strings.TrimSuffix(cueBase, filepath.Ext(cueBase))
	if p := resolveAudioByStem(fs, dir, cueStem, claimed); p != "" {
		return p
	}

	// 3. If only one unclaimed audio file exists in directory
	entries, err := fs.List(dir)
	if err == nil {
		var unclaimed []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if !audio.IsAudioExt(filepath.Ext(e.Name())) {
				continue
			}
			cand := filepath.Join(dir, e.Name())
			if !claimed[cand] {
				unclaimed = append(unclaimed, cand)
			}
		}
		if len(unclaimed) == 1 {
			return unclaimed[0]
		}
	}

	return ""
}
