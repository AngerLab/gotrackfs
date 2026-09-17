package vfs

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gotrackfs/internal/cue"
)

// dirFacts captures file metadata (mtimes and sizes) required for cache validation.
type dirFacts struct {
	dirModTime    time.Time
	cueModTimes   map[string]time.Time
	cueSizes      map[string]int64
	audioModTimes map[string]time.Time
	audioSizes    map[string]int64
}

// buildDirState discovers CUE and audio files in dirPath, parses them,
// and computes the DirState along with dirFacts for caching.
func buildDirState(dirPath string, dirFi os.FileInfo) (*DirState, *dirFacts, error) {
	cueFiles, err := findAllCueFiles(dirPath)
	if err != nil {
		return nil, nil, err
	}

	if len(cueFiles) == 0 {
		return nil, &dirFacts{dirModTime: dirFi.ModTime()}, nil
	}

	currentModTimes := make(map[string]time.Time, len(cueFiles))
	currentSizes := make(map[string]int64, len(cueFiles))
	for _, cp := range cueFiles {
		fi, err := os.Stat(cp)
		if err != nil {
			// If file disappeared concurrently, skip it
			continue
		}
		currentModTimes[cp] = fi.ModTime()
		currentSizes[cp] = fi.Size()
	}

	if len(currentModTimes) == 0 {
		return nil, &dirFacts{dirModTime: dirFi.ModTime()}, nil
	}

	var albums []*Album
	claimedAudios := make(map[string]bool)

	for _, cuePath := range cueFiles {
		sheet, err := cue.ParseFile(cuePath)
		if err != nil {
			continue
		}

		tracks := sheet.AllTracks()
		if len(tracks) == 0 {
			continue
		}

		// Multi-file CUEs (already split per track) are left untouched
		if len(sheet.Files) > 1 {
			continue
		}

		declaredFile := ""
		if len(sheet.Files) == 1 {
			declaredFile = sheet.Files[0].Name
		}

		audioPath := resolveAudioFileForCue(dirPath, cuePath, declaredFile, claimedAudios)
		if audioPath == "" {
			continue
		}

		audioFi, err := os.Stat(audioPath)
		if err != nil {
			continue
		}
		claimedAudios[audioPath] = true

		album := &Album{
			CuePath:         cuePath,
			SourceAudioPath: audioPath,
			Sheet:           sheet,
			Tracks:          make([]VirtualTrack, len(tracks)),
		}

		totalAudioSize := audioFi.Size()
		totalKnownDuration := 0.0
		for _, tr := range tracks {
			if tr.End > tr.Start {
				totalKnownDuration += (tr.End - tr.Start)
			}
		}

		for i, tr := range tracks {
			title := tr.Title
			if title == "" {
				title = fmt.Sprintf("Track %02d", tr.Num)
			}

			estimatedSize := int64(0)
			if tr.End > tr.Start && totalKnownDuration > 0 {
				duration := tr.End - tr.Start
				estimatedSize = int64(float64(totalAudioSize) * (duration / totalKnownDuration))
			} else {
				estimatedSize = totalAudioSize / int64(len(tracks))
			}
			if estimatedSize <= 0 {
				estimatedSize = 1024 * 1024
			}

			album.Tracks[i] = VirtualTrack{
				Num:             tr.Num,
				Title:           title,
				Performer:       tr.Performer,
				SourceAudioPath: audioPath,
				Start:           tr.Start,
				End:             tr.End,
				EstimatedSize:   estimatedSize,
			}
		}

		albums = append(albums, album)
	}

	facts := &dirFacts{
		dirModTime:  dirFi.ModTime(),
		cueModTimes: currentModTimes,
		cueSizes:    currentSizes,
	}

	if len(albums) == 0 {
		return nil, facts, nil
	}

	dirState := &DirState{
		Albums:          albums,
		TracksByName:    make(map[string]*VirtualTrack),
		HiddenMonoliths: make(map[string]bool),
	}

	// TODO: Support virtual subdirectories mode (e.g. via --split-discs flag).
	// When enabled for multi-album folders (len(albums) > 1), instead of flattening
	// with disc prefixes (1-01..., 2-01...), generate virtual subdirectories (CD1/, CD2/)
	// and mirror cover.jpg into each.
	isMultiAlbum := len(albums) > 1

	for albumIdx, album := range albums {
		dirState.HiddenMonoliths[filepath.Base(album.SourceAudioPath)] = true

		discPrefix := ""
		if isMultiAlbum {
			discPrefix = determineDiscPrefix(album, albumIdx+1)
		}

		ext := filepath.Ext(album.SourceAudioPath)
		if ext == "" {
			ext = ".flac"
		}

		for i := range album.Tracks {
			vt := &album.Tracks[i]
			vt.FileName = formatTrackFilename(vt.Num, vt.Performer, album.Sheet.Performer, vt.Title, discPrefix, ext)
			dirState.TracksByName[vt.FileName] = vt
		}
	}

	facts.audioModTimes = make(map[string]time.Time, len(albums))
	facts.audioSizes = make(map[string]int64, len(albums))
	for _, album := range albums {
		if fi, err := os.Stat(album.SourceAudioPath); err == nil {
			facts.audioModTimes[album.SourceAudioPath] = fi.ModTime()
			facts.audioSizes[album.SourceAudioPath] = fi.Size()
		}
	}

	return dirState, facts, nil
}
