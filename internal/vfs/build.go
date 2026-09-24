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
	"github.com/AngerLab/gotrackfs/internal/hostfs"
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
func buildDirState(fs hostfs.FS, dirPath string, dirFi os.FileInfo, logger *slog.Logger, maxQuality track.Quality) (*DirState, *dirFacts, error) {
	if logger == nil {
		logger = slog.Default()
	}

	cueFiles, err := findAllCueFiles(fs, dirPath)
	if err != nil {
		return nil, nil, err
	}

	facts := &dirFacts{dirModTime: dirFi.ModTime()}
	if len(cueFiles) == 0 {
		return nil, facts, nil
	}

	albums := collectAlbums(fs, dirPath, cueFiles, logger, maxQuality)
	if len(albums) == 0 {
		return nil, facts, nil
	}

	dirState := baseDirState(albums)
	realNames, artwork := scanDirEntries(dirPath)
	assignArtwork(albums, findPrimaryArtwork(artwork))
	// Cutter keys must be computed after artwork: Slice.Key() hashes ArtworkPath.
	finalizeTrackCutterKeys(albums)

	if len(albums) == 1 {
		// Single album: flat layout directly in parent directory.
		dirState.TracksByName = assignTrackFilenames(albums[0], realNames, dirPath, logger)
	} else {
		// Multi-album: generate virtual subdirectories (e.g. CD1/, CD2/).
		attachAlbumSubdirs(dirState, albums, realNames, artwork, dirPath, logger)
	}

	return dirState, facts, nil
}

// collectAlbums parses every non-skipped CUE sheet in cueFiles into an Album.
// Claimed plaintext audio files are tracked so two CUE sheets never resolve
// to the same source file.
func collectAlbums(fs hostfs.FS, dirPath string, cueFiles []string, logger *slog.Logger, maxQuality track.Quality) []*Album {
	var albums []*Album
	claimedAudios := make(map[string]bool)
	for _, cuePath := range cueFiles {
		album, skip := buildAlbumFromCue(fs, dirPath, cuePath, claimedAudios, logger, maxQuality)
		if skip {
			continue
		}
		for _, p := range album.SourceAudioPaths {
			claimedAudios[p] = true
		}
		albums = append(albums, album)
	}
	return albums
}

// buildAlbumFromCue parses one CUE sheet and materializes its virtual tracks.
// skip is true when the sheet must be ignored (unparseable, empty, or already split).
func buildAlbumFromCue(fs hostfs.FS, dirPath, cuePath string, claimedAudios map[string]bool, logger *slog.Logger, maxQuality track.Quality) (*Album, bool) {
	sheet, err := cue.ParseFile(cuePath)
	if err != nil {
		logger.Warn("vfs: skipping invalid cue file", "path", cuePath, "error", err)
		return nil, true
	}

	totalTracks := sheet.TotalTracks()
	if totalTracks == 0 {
		logger.Warn("vfs: skipping cue file with no tracks", "path", cuePath)
		return nil, true
	}
	if isAlreadySplit(sheet) {
		logger.Debug("vfs: skipping multi-file cue (already split per track)",
			"path", cuePath, "files", len(sheet.Files), "tracks", totalTracks)
		return nil, true
	}

	var (
		virtualTracks []VirtualTrack
		sourcePaths   []string
	)
	for fi := range sheet.Files {
		f := &sheet.Files[fi]
		if len(f.Tracks) == 0 {
			continue
		}
		tracks, sourcePath, ok := virtualTracksForFile(fs, dirPath, cuePath, sheet, f, claimedAudios, sourcePaths, logger, maxQuality)
		if !ok {
			return nil, true
		}
		virtualTracks = append(virtualTracks, tracks...)
		sourcePaths = append(sourcePaths, sourcePath)
	}

	return &Album{
		CuePath:          cuePath,
		SourceAudioPaths: sourcePaths,
		Sheet:            sheet,
		Tracks:           virtualTracks,
	}, false
}

// virtualTracksForFile resolves the audio file declared by one FILE entry of a
// CUE sheet and builds the virtual tracks it contributes. ok is false when the
// source audio cannot be resolved or stat'ed (in which case the whole cue is skipped).
func virtualTracksForFile(fs hostfs.FS, dirPath, cuePath string, sheet *cue.Sheet, f *cue.File, claimedAudios map[string]bool, cueSourcePaths []string, logger *slog.Logger, maxQuality track.Quality) (tracks []VirtualTrack, sourcePath string, ok bool) {
	alreadyClaimed := make(map[string]bool, len(claimedAudios)+len(cueSourcePaths))
	for k, v := range claimedAudios {
		alreadyClaimed[k] = v
	}
	for _, p := range cueSourcePaths {
		alreadyClaimed[p] = true
	}

	audioPath := resolveAudioFileForCue(fs, dirPath, cuePath, f.Name, alreadyClaimed)
	if audioPath == "" {
		logger.Warn("vfs: audio file not found for cue", "cue", cuePath, "declared", f.Name)
		return nil, "", false
	}
	audioFi, err := fs.Stat(audioPath)
	if err != nil {
		logger.Warn("vfs: cannot stat audio file for cue", "cue", cuePath, "audio", audioPath, "error", err)
		return nil, "", false
	}

	audioInfo, probeErr := audio.Probe(audioPath)
	if probeErr != nil {
		logger.Debug("vfs: failed to probe audio file", "audio", audioPath, "error", probeErr)
	}
	fileAudioDuration := audioInfo.Duration

	// Quality cap: probe the source format per audio file and lower cuts that exceed it.
	targetRate, targetBits, qualityRatio := audioQualityPlan(maxQuality, audioInfo.Format, probeErr, audioPath, logger)

	totalAudioSize := audioFi.Size()
	totalKnownDuration := knownFileDuration(fileAudioDuration, f)

	tracks = make([]VirtualTrack, 0, len(f.Tracks))
	for _, tr := range f.Tracks {
		title := cueTrackTitle(tr)
		trackDuration := trackSliceDuration(tr, totalKnownDuration)
		tracks = append(tracks, VirtualTrack{
			Num:           tr.Num,
			Title:         title,
			Performer:     tr.Performer,
			EstimatedSize: estimateTrackSize(totalAudioSize, trackDuration, totalKnownDuration, len(f.Tracks), qualityRatio),
			Slice: track.Slice{
				SourceAudioPath:  audioPath,
				SourceModTime:    audioFi.ModTime(),
				SourceSize:       audioFi.Size(),
				Start:            tr.Start,
				End:              tr.End,
				TargetSampleRate: targetRate,
				TargetBits:       targetBits,
				Tags:             buildTrackTags(sheet, tr, title),
			},
		})
	}
	return tracks, audioPath, true
}

// audioQualityPlan returns the (possibly lowered) target sample rate and bit
// depth for a source format, plus the size ratio the cut track will have
// relative to the source bytes. Unset caps or failed probes yield zero
// targets; the ratio still accounts for WAV sources always being compressed
// to FLAC (~0.60).
func audioQualityPlan(maxQuality track.Quality, srcFmt audio.Format, probeErr error, audioPath string, logger *slog.Logger) (targetRate, targetBits int, qualityRatio float64) {
	qualityRatio = 1.0
	if !maxQuality.Unset() && probeErr == nil {
		targetRate, targetBits = maxQuality.Plan(srcFmt.SampleRate, srcFmt.Bits)
		if targetRate == 0 && targetBits == 0 {
			logger.Debug("vfs: source already at or below quality cap", "audio", audioPath, "cap", maxQuality.String())
		}
	}
	if srcFmt.SampleRate > 0 && targetRate > 0 {
		qualityRatio *= float64(targetRate) / float64(srcFmt.SampleRate)
	}
	if srcFmt.Bits > 0 && targetBits > 0 {
		qualityRatio *= float64(targetBits) / float64(srcFmt.Bits)
	}
	if audio.IsWAVExt(filepath.Ext(audioPath)) {
		// WAV is uncompressed PCM; FLAC encodes it down to ~60% of the raw bytes.
		qualityRatio *= wavToFlacSizeRatio
	}
	return targetRate, targetBits, qualityRatio
}

// knownFileDuration returns the best-known total duration of a CUE FILE entry,
// filling a missing End on its last track from the probed audio duration when possible.
func knownFileDuration(fileAudioDuration float64, f *cue.File) float64 {
	if fileAudioDuration > 0 {
		if len(f.Tracks) > 0 {
			lastIdx := len(f.Tracks) - 1
			if f.Tracks[lastIdx].End <= f.Tracks[lastIdx].Start && fileAudioDuration > f.Tracks[lastIdx].Start {
				f.Tracks[lastIdx].End = fileAudioDuration
			}
		}
		return fileAudioDuration
	}
	var total float64
	for _, tr := range f.Tracks {
		if tr.End > tr.Start {
			total += tr.End - tr.Start
		}
	}
	return total
}

// cueTrackTitle falls back to a zero-padded "Track NN" label when the sheet
// omits the track title.
func cueTrackTitle(tr cue.Track) string {
	if tr.Title != "" {
		return tr.Title
	}
	return fmt.Sprintf("Track %02d", tr.Num)
}

// trackSliceDuration returns the duration covered by a single track: its own
// span when indexed, otherwise the remainder of the file from its start.
func trackSliceDuration(tr cue.Track, totalKnownDuration float64) float64 {
	if tr.End > tr.Start {
		return tr.End - tr.Start
	}
	if totalKnownDuration > tr.Start {
		return totalKnownDuration - tr.Start
	}
	return 0
}

// estimateTrackSize scales the source audio size by the track's share of the
// total duration (or track count as a fallback), the quality ratio, and a
// safety margin, with a 1 MB floor.
func estimateTrackSize(totalAudioSize int64, trackDuration, totalKnownDuration float64, numTracks int, qualityRatio float64) int64 {
	var estimatedSize int64
	if trackDuration > 0 && totalKnownDuration > 0 {
		estimatedSize = int64(float64(totalAudioSize) * (trackDuration / totalKnownDuration) * qualityRatio * estimationSafetyMargin)
	} else {
		estimatedSize = int64(float64(totalAudioSize) / float64(numTracks) * qualityRatio * estimationSafetyMargin)
	}
	if estimatedSize < minEstimatedTrackFloor {
		estimatedSize = minEstimatedTrackFloor
	}
	return estimatedSize
}

// buildTrackTags assembles the FLAC Vorbis-comment tags for a virtual track
// from the sheet and track metadata.
func buildTrackTags(sheet *cue.Sheet, tr cue.Track, title string) map[string]string {
	tags := make(map[string]string, 8)
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
	return tags
}

// baseDirState builds the empty skeleton of a DirState and registers the
// source audio files that must be hidden from the virtual directory.
func baseDirState(albums []*Album) *DirState {
	dirState := &DirState{
		Albums:          albums,
		TracksByName:    make(map[string]*VirtualTrack),
		HiddenMonoliths: make(map[string]bool),
		Subdirs:         make(map[string]*DirState),
		MirroredFiles:   make(map[string]string),
	}
	for _, album := range albums {
		for _, src := range album.SourceAudioPaths {
			dirState.HiddenMonoliths[norm.NFC.String(filepath.Base(src))] = true
		}
	}
	return dirState
}

// scanDirEntries lists the real directory once and classifies entries into
// occupied names (for collision avoidance) and artwork candidates.
func scanDirEntries(dirPath string) (realNames map[string]bool, artwork map[string]string) {
	rawEntries, _ := os.ReadDir(dirPath)
	realNames = make(map[string]bool, len(rawEntries))
	artwork = make(map[string]string)
	for _, re := range rawEntries {
		name := norm.NFC.String(re.Name())
		realNames[name] = true
		if re.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(name)) {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".pdf":
			artwork[name] = filepath.Join(dirPath, re.Name())
		}
	}
	return realNames, artwork
}

// assignArtwork attaches the primary cover art to every virtual track and
// accounts for its size in the pre-slice estimates.
func assignArtwork(albums []*Album, artworkPath string) {
	var artworkSize int64
	if artworkPath != "" {
		if artFi, err := os.Stat(artworkPath); err == nil {
			artworkSize = artFi.Size()
		}
	}
	for _, album := range albums {
		for i := range album.Tracks {
			album.Tracks[i].Slice.ArtworkPath = artworkPath
			if artworkSize > 0 {
				album.Tracks[i].EstimatedSize += artworkSize
			}
		}
	}
}

// finalizeTrackCutterKeys computes the deduplication key for every virtual
// track's slice. It must run after assignArtwork: Slice.Key() hashes
// ArtworkPath, so the key is only stable once artwork is attached.
func finalizeTrackCutterKeys(albums []*Album) {
	for _, album := range albums {
		for i := range album.Tracks {
			album.Tracks[i].CutterKey = album.Tracks[i].Slice.Key()
		}
	}
}

// attachAlbumSubdirs lays out multiple albums into virtual subdirectories
// (CD1/, CD2/, ...), mirroring parent artwork into each subdirectory and
// resolving name collisions against real entries and previously added subdirs.
func attachAlbumSubdirs(dirState *DirState, albums []*Album, realNames map[string]bool, artwork map[string]string, dirPath string, logger *slog.Logger) {
	for albumIdx, album := range albums {
		subName := norm.NFC.String(determineDiscDirName(album, albumIdx+1))
		candSubName, collided := uniqueNamePlain(subName, func(c string) bool {
			_, existsSub := dirState.Subdirs[c]
			return existsSub || realNames[c]
		})
		if collided {
			logger.Warn("vfs: virtual subdir name collision, adding suffix", "dir", dirPath, "original", subName, "assigned", candSubName)
		}
		subName = candSubName

		subOccupied := make(map[string]bool, len(artwork))
		mirrored := make(map[string]string, len(artwork))
		for artName, artPath := range artwork {
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

// assignTrackFilenames computes collision-free track filenames for an album against a set of already occupied names,
// assigning numerical suffixes (e.g. "01. Title (2).flac") when conflicts arise.
// Sliced tracks are always FLAC, so virtual track files always use the .flac extension.
func assignTrackFilenames(album *Album, occupiedNames map[string]bool, context string, logger *slog.Logger) map[string]*VirtualTrack {
	const ext = ".flac"

	tracksByName := make(map[string]*VirtualTrack, len(album.Tracks))
	for i := range album.Tracks {
		vt := &album.Tracks[i]
		baseName := norm.NFC.String(formatTrackFilename(vt.Num, vt.Performer, album.Sheet.Performer, vt.Title, ext))
		candidateName, collided := uniqueName(baseName, func(c string) bool {
			return tracksByName[c] != nil || occupiedNames[c]
		})
		if collided && logger != nil {
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

// isAlreadySplit returns true if a CUE sheet defines multiple files
// and each file contains at most one indexed track (meaning the album
// is already split into individual track files).
func isAlreadySplit(sheet *cue.Sheet) bool {
	if len(sheet.Files) <= 1 {
		return false
	}
	for _, f := range sheet.Files {
		indexCount := 0
		for _, tr := range f.Tracks {
			if tr.HasIndex {
				indexCount++
			}
		}
		if indexCount > 1 {
			return false
		}
	}
	return true
}
