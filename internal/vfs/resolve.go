package vfs

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/AngerLab/gotrackfs/internal/audio"
)

// findAllCueFiles returns all .cue files in the specified directory.
func findAllCueFiles(dir string) ([]string, error) {
	entries, err := readDir(dir)
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

// probeOrder is the sequence in which stem-derived candidates are tried by
// the resolver: lowercase extensions first (historical priority), then the
// uppercase variants so that on-disk .FLAC/.WAV/.WAVE files resolve on
// case-sensitive filesystems. The list is derived from audio.AudioExtensions
// — the single membership truth: adding an extension there (say .opus) adds
// both its lowercase and uppercase probes here instead of silently
// diverging. (A hand-written uppercase list had already drifted — .WAVE was
// missing; deriving the variants from membership closes that class of bug.)
// Probe order itself is a resolver concern; membership and base ordering
// live in the audio package.
var probeOrder = func() []string {
	order := slices.Clone(audio.AudioExtensions)
	for _, ext := range audio.AudioExtensions {
		order = append(order, strings.ToUpper(ext))
	}
	return order
}()

// resolveAudioByStem searches dir for a file named stem + <audio extension>,
// probing in probeOrder order (lowercase stems first, then .FLAC/.WAV).
func resolveAudioByStem(dir, stem string, claimed map[string]bool) string {
	for _, ext := range probeOrder {
		p := filepath.Join(dir, stem+ext)
		if fi, err := statPath(p); err == nil && !fi.IsDir() && !claimed[p] {
			return p
		}
	}
	return ""
}

// resolveAudioFileForCue resolves the matching monolithic audio file for a CUE sheet.
// It checks declared filename, matching stem with audio extensions, and single unclaimed audio.
func (b *albumBuilder) resolveAudioFileForCue(cuePath, declaredName string, claimed map[string]bool) string {
	dir := b.dirPath
	// 1. Declared name in CUE
	if declaredName != "" {
		p := filepath.Join(dir, declaredName)
		if fi, err := statPath(p); err == nil && !fi.IsDir() && !claimed[p] {
			return p
		}
		p = filepath.Join(dir, filepath.Base(declaredName))
		if fi, err := statPath(p); err == nil && !fi.IsDir() && !claimed[p] {
			return p
		}

		// Stem + audio extensions
		stem := strings.TrimSuffix(filepath.Base(declaredName), filepath.Ext(declaredName))
		if p := resolveAudioByStem(dir, stem, claimed); p != "" {
			return p
		}
	}

	// 2. Same stem as CUE file
	cueBase := filepath.Base(cuePath)
	cueStem := strings.TrimSuffix(cueBase, filepath.Ext(cueBase))
	if p := resolveAudioByStem(dir, cueStem, claimed); p != "" {
		return p
	}

	// 3. If only one unclaimed audio file exists in directory
	entries, err := readDir(dir)
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
