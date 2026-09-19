package vfs

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AngerLab/gotrackfs/internal/audio"
	"github.com/AngerLab/gotrackfs/internal/cue"
	"github.com/AngerLab/gotrackfs/internal/track"

	"golang.org/x/text/unicode/norm"
)

const (
	// estimationSafetyMargin is applied to proportional track audio size to ensure
	// pre-slice Getattr reports a safe upper bound. This covers re-encoding container
	// overhead (standalone FLAC header, seek tables, CUE Vorbis comments) and compression variance.
	estimationSafetyMargin = 1.25

	// minEstimatedTrackFloor provides a reasonable minimum size floor (1 MB) for very short tracks
	// or corrupted estimates to prevent undersized buffer allocations by media players.
	minEstimatedTrackFloor = 1024 * 1024

	// wavToFlacSizeRatio approximates how much a raw PCM WAV shrinks when sliced
	// into a FLAC (lossless) track. Slices are always written as FLAC, so WAV
	// sources would otherwise be overestimated by the raw PCM size. ~0.6 is a
	// conservative average for typical music content.
	wavToFlacSizeRatio = 0.60
)

// dirFacts captures directory metadata required for cache validation.
type dirFacts struct {
	dirModTime time.Time
}

// buildDirState discovers CUE and audio files in dirPath, parses them,
// and computes the DirState along with dirFacts for caching.
// maxQuality optionally caps the sliced-track output format (zero = keep source).
func buildDirState(dirPath string, dirFi os.FileInfo, logger *slog.Logger, maxQuality track.Quality) (*DirState, *dirFacts, error) {
	if logger == nil {
		logger = slog.Default()
	}

	cueFiles, err := findAllCueFiles(dirPath)
	if err != nil {
		return nil, nil, err
	}

	if len(cueFiles) == 0 {
		return nil, &dirFacts{dirModTime: dirFi.ModTime()}, nil
	}

	var albums []*Album
	claimedAudios := make(map[string]bool)

	for _, cuePath := range cueFiles {
		sheet, err := cue.ParseFile(cuePath)
		if err != nil {
			logger.Warn("vfs: skipping invalid cue file", "path", cuePath, "error", err)
			continue
		}

		tracks := sheet.AllTracks()
		if len(tracks) == 0 {
			logger.Warn("vfs: skipping cue file with no tracks", "path", cuePath)
			continue
		}

		// Multi-file CUEs (already split per track) are left untouched
		if len(sheet.Files) > 1 {
			logger.Debug("vfs: skipping multi-file cue (already split per track)", "path", cuePath)
			continue
		}

		declaredFile := ""
		if len(sheet.Files) == 1 {
			declaredFile = sheet.Files[0].Name
		}

		audioPath := resolveAudioFileForCue(dirPath, cuePath, declaredFile, claimedAudios)
		if audioPath == "" {
			logger.Warn("vfs: audio file not found for cue", "cue", cuePath, "declared", declaredFile)
			continue
		}

		audioFi, err := os.Stat(audioPath)
		if err != nil {
			logger.Warn("vfs: cannot stat audio file for cue", "cue", cuePath, "audio", audioPath, "error", err)
			continue
		}
		claimedAudios[audioPath] = true

		audioInfo, probeErr := audio.Probe(audioPath)
		if probeErr != nil {
			logger.Debug("vfs: failed to probe audio file", "audio", audioPath, "error", probeErr)
		}
		totalAudioDuration := audioInfo.Duration

		// Quality cap: probe the source format once per album and lower cuts
		// that exceed it. Unset cap leaves every slice in the source format.
		var targetRate, targetBits int
		srcFmt := audioInfo.Format
		if !maxQuality.Unset() && probeErr == nil {
			targetRate, targetBits = maxQuality.Plan(srcFmt.SampleRate, srcFmt.Bits)
			if targetRate == 0 && targetBits == 0 {
				logger.Debug("vfs: source already at or below quality cap", "audio", audioPath, "cap", maxQuality.String())
			}
		}

		qualityRatio := 1.0
		if srcFmt.SampleRate > 0 && targetRate > 0 {
			qualityRatio *= float64(targetRate) / float64(srcFmt.SampleRate)
		}
		if srcFmt.Bits > 0 && targetBits > 0 {
			qualityRatio *= float64(targetBits) / float64(srcFmt.Bits)
		}
		if ext := strings.ToLower(filepath.Ext(audioPath)); ext == ".wav" || ext == ".wave" {
			// WAV is uncompressed PCM; FLAC encodes it down to ~60% of the raw bytes.
			qualityRatio *= wavToFlacSizeRatio
		}

		totalAudioSize := audioFi.Size()
		totalKnownDuration := totalAudioDuration
		if totalKnownDuration <= 0 {
			for _, tr := range tracks {
				if tr.End > tr.Start {
					totalKnownDuration += (tr.End - tr.Start)
				}
			}
		} else {
			// If total duration is known, populate the last track's End time if missing
			if len(tracks) > 0 {
				lastIdx := len(tracks) - 1
				if tracks[lastIdx].End <= tracks[lastIdx].Start && totalAudioDuration > tracks[lastIdx].Start {
					tracks[lastIdx].End = totalAudioDuration
				}
			}
		}

		album := &Album{
			CuePath:         cuePath,
			SourceAudioPath: audioPath,
			Sheet:           sheet,
			Tracks:          make([]VirtualTrack, len(tracks)),
		}

		for i, tr := range tracks {
			title := tr.Title
			if title == "" {
				title = fmt.Sprintf("Track %02d", tr.Num)
			}

			trackDuration := float64(0)
			if tr.End > tr.Start {
				trackDuration = tr.End - tr.Start
			} else if totalKnownDuration > tr.Start {
				trackDuration = totalKnownDuration - tr.Start
			}

			estimatedSize := int64(0)
			if trackDuration > 0 && totalKnownDuration > 0 {
				estimatedSize = int64(float64(totalAudioSize) * (trackDuration / totalKnownDuration) * qualityRatio * estimationSafetyMargin)
			} else {
				estimatedSize = int64(float64(totalAudioSize) / float64(len(tracks)) * qualityRatio * estimationSafetyMargin)
			}
			if estimatedSize < minEstimatedTrackFloor {
				estimatedSize = minEstimatedTrackFloor
			}

			tags := make(map[string]string)
			if title != "" {
				tags["title"] = title
			}
			artist := tr.Performer
			if artist == "" {
				artist = sheet.Performer
			}
			if artist != "" {
				tags["artist"] = artist
			}
			if sheet.Performer != "" {
				tags["album_artist"] = sheet.Performer
			}
			if sheet.Title != "" {
				tags["album"] = sheet.Title
			}
			if tr.Num > 0 {
				tags["track"] = strconv.Itoa(tr.Num)
			}
			if sheet.Date != "" {
				tags["date"] = sheet.Date
			}
			if sheet.Genre != "" {
				tags["genre"] = sheet.Genre
			}
			if sheet.DiscNumber != "" {
				tags["disc"] = sheet.DiscNumber
			}
			if sheet.Songwriter != "" {
				tags["composer"] = sheet.Songwriter
			} else if tr.Songwriter != "" {
				tags["composer"] = tr.Songwriter
			}

			album.Tracks[i] = VirtualTrack{
				Num:           tr.Num,
				Title:         title,
				Performer:     tr.Performer,
				EstimatedSize: estimatedSize,
				Slice: track.Slice{
					SourceAudioPath:  audioPath,
					SourceModTime:    audioFi.ModTime(),
					SourceSize:       audioFi.Size(),
					Start:            tr.Start,
					End:              tr.End,
					TargetSampleRate: targetRate,
					TargetBits:       targetBits,
					Tags:             tags,
				},
			}
		}

		albums = append(albums, album)
	}

	facts := &dirFacts{
		dirModTime: dirFi.ModTime(),
	}

	if len(albums) == 0 {
		return nil, facts, nil
	}

	dirState := &DirState{
		Albums:          albums,
		TracksByName:    make(map[string]*VirtualTrack),
		HiddenMonoliths: make(map[string]bool),
		Subdirs:         make(map[string]*DirState),
		MirroredFiles:   make(map[string]string),
	}

	for _, album := range albums {
		dirState.HiddenMonoliths[norm.NFC.String(filepath.Base(album.SourceAudioPath))] = true
	}

	rawEntries, _ := os.ReadDir(dirPath)
	realNames := make(map[string]bool, len(rawEntries))
	parentArtwork := make(map[string]string)
	for _, re := range rawEntries {
		name := norm.NFC.String(re.Name())
		realNames[name] = true
		if re.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".pdf":
			parentArtwork[name] = filepath.Join(dirPath, re.Name())
		}
	}

	primaryArtwork := findPrimaryArtwork(parentArtwork)
	var artworkSize int64
	if primaryArtwork != "" {
		if artFi, err := os.Stat(primaryArtwork); err == nil {
			artworkSize = artFi.Size()
		}
	}
	for _, album := range albums {
		for i := range album.Tracks {
			album.Tracks[i].Slice.ArtworkPath = primaryArtwork
			album.Tracks[i].CutterKey = album.Tracks[i].Slice.Key()
			if artworkSize > 0 {
				album.Tracks[i].EstimatedSize += artworkSize
			}
		}
	}

	if len(albums) == 1 {
		// Single album: flat layout directly in parent directory
		dirState.TracksByName = assignTrackFilenames(albums[0], realNames, dirPath, logger)
	} else {
		// Multi-album: generate virtual subdirectories (e.g. CD1/, CD2/)
		for albumIdx, album := range albums {
			subName := norm.NFC.String(determineDiscDirName(album, albumIdx+1))
			candSubName := subName
			subSuffix := 2
			for {
				_, existsSub := dirState.Subdirs[candSubName]
				isShadowing := realNames[candSubName]
				if !existsSub && !isShadowing {
					break
				}
				candSubName = fmt.Sprintf("%s (%d)", subName, subSuffix)
				subSuffix++
			}
			if candSubName != subName {
				logger.Warn("vfs: virtual subdir name collision, adding suffix", "dir", dirPath, "original", subName, "assigned", candSubName)
			}
			subName = candSubName

			subOccupied := make(map[string]bool, len(parentArtwork))
			mirrored := make(map[string]string, len(parentArtwork))
			for artName, artPath := range parentArtwork {
				normArt := norm.NFC.String(artName)
				mirrored[normArt] = artPath
				subOccupied[normArt] = true
			}

			subState := &DirState{
				Albums:          []*Album{album},
				HiddenMonoliths: make(map[string]bool),
				Subdirs:         make(map[string]*DirState),
				MirroredFiles:   mirrored,
				TracksByName:    assignTrackFilenames(album, subOccupied, filepath.Join(dirPath, subName), logger),
			}

			dirState.Subdirs[subName] = subState
		}
	}

	return dirState, facts, nil
}

// assignTrackFilenames computes collision-free track filenames for an album against a set of already occupied names,
// assigning numerical suffixes (e.g. "01. Title (2).flac") when conflicts arise.
func assignTrackFilenames(album *Album, occupiedNames map[string]bool, context string, logger *slog.Logger) map[string]*VirtualTrack {
	ext := filepath.Ext(album.SourceAudioPath)
	if ext == "" {
		ext = ".flac"
	}

	tracksByName := make(map[string]*VirtualTrack, len(album.Tracks))
	for i := range album.Tracks {
		vt := &album.Tracks[i]
		baseName := norm.NFC.String(formatTrackFilename(vt.Num, vt.Performer, album.Sheet.Performer, vt.Title, ext))
		candidateName := baseName
		suffix := 2
		for {
			isDuplicateTrack := tracksByName[candidateName] != nil
			isOccupied := occupiedNames[candidateName]
			if !isDuplicateTrack && !isOccupied {
				break
			}
			candidateName = addFilenameSuffix(baseName, suffix)
			suffix++
		}
		if candidateName != baseName && logger != nil {
			logger.Warn("vfs: track filename collision, adding suffix", "context", context, "original", baseName, "assigned", candidateName)
		}
		vt.FileName = candidateName
		tracksByName[candidateName] = vt
	}
	return tracksByName
}

// findPrimaryArtwork returns the path to the best candidate cover image from available artwork.
// Only valid raster images (.jpg, .jpeg, .png) are considered for embedding.
func findPrimaryArtwork(artworks map[string]string) string {
	for _, preferred := range []string{"cover.jpg", "folder.jpg", "front.jpg", "cover.png", "folder.png"} {
		if path, ok := artworks[preferred]; ok {
			return path
		}
	}
	for name, path := range artworks {
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".jpg", ".jpeg", ".png":
			return path
		}
	}
	return ""
}
