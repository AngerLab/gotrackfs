// Package track defines the neutral domain types for a single audio track slice.
// It is intentionally free of any filesystem or audio-codec dependencies,
// so both the vfs and cutter packages can import it without creating a cycle.
package track

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
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
	buf := fmt.Appendf(nil, "%s:%d:%d:%.4f:%.4f:%s",
		s.SourceAudioPath,
		s.SourceModTime.UnixNano(),
		s.SourceSize,
		s.Start,
		s.End,
		s.ArtworkPath,
	)
	if len(s.Tags) > 0 {
		keys := make([]string, 0, len(s.Tags))
		for k := range s.Tags {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if v := s.Tags[k]; v != "" {
				buf = fmt.Appendf(buf, ":%s=%s", k, v)
			}
		}
	}
	hasher.Write(buf)
	return hex.EncodeToString(hasher.Sum(nil))[:16]
}
