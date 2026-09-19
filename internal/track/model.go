// Package track defines the neutral domain types for a single audio track slice.
// It is intentionally free of any filesystem or audio-codec dependencies,
// so both the vfs and cutter packages can import it without creating a cycle.
package track

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strconv"
	"time"
)

// Slice contains all audio offsets and metadata needed to cut a single track
// out of a monolithic audio file. It is the shared language between the
// virtual filesystem (which builds it) and the cutter (which executes it).
type Slice struct {
	SourceAudioPath string
	SourceModTime   time.Time
	SourceSize      int64
	Start           float64 // Start offset in seconds
	End             float64 // End offset in seconds (0 means until EOF)
	ArtworkPath     string  // Optional path to cover image to embed

	// Output targets derived from the mount's quality cap and the probed
	// source format. Zeros mean "keep the source format untouched".
	TargetSampleRate int // Resample to this rate (Hz) when the source is above the cap
	TargetBits       int // Encode at this bit depth when the source is above the cap

	Tags map[string]string // Key-value metadata tags (e.g. title, artist, album, track, date, genre, disc)
}

// Tag returns the value of the metadata tag with the given key,
// or an empty string if not present.
func (s Slice) Tag(key string) string {
	if s.Tags == nil {
		return ""
	}
	return s.Tags[key]
}

// Key computes a deterministic, unique cache key for this slice,
// derived from all inputs: source audio identity, time offsets, artwork path,
// and all metadata tags in sorted key order.
// The key is a 16-character hex prefix of SHA-256.
func (s Slice) Key() string {
	hasher := sha256.New()
	var buf [256]byte
	b := buf[:0]

	b = append(b, s.SourceAudioPath...)
	b = append(b, ':')
	b = strconv.AppendInt(b, s.SourceModTime.UnixNano(), 10)
	b = append(b, ':')
	b = strconv.AppendInt(b, s.SourceSize, 10)
	b = append(b, ':')
	b = strconv.AppendFloat(b, s.Start, 'f', 4, 64)
	b = append(b, ':')
	b = strconv.AppendFloat(b, s.End, 'f', 4, 64)
	b = append(b, ':')
	b = append(b, s.ArtworkPath...)
	b = append(b, ":t"...)
	b = strconv.AppendInt(b, int64(s.TargetSampleRate), 10)
	b = append(b, ":b"...)
	b = strconv.AppendInt(b, int64(s.TargetBits), 10)
	hasher.Write(b)

	if len(s.Tags) > 0 {
		for _, k := range slices.Sorted(maps.Keys(s.Tags)) {
			if v := s.Tags[k]; v != "" {
				b = buf[:0]
				b = append(b, ':')
				b = append(b, k...)
				b = append(b, '=')
				b = append(b, v...)
				hasher.Write(b)
			}
		}
	}

	var sum [sha256.Size]byte
	digest := hasher.Sum(sum[:0])
	return hex.EncodeToString(digest[:8])
}
