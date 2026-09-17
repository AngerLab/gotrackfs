package cutter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"time"
)

// TrackRequest contains all audio offsets and metadata needed to slice a track.
type TrackRequest struct {
	SourceAudioPath string
	SourceModTime   time.Time
	SourceSize      int64
	Start           float64 // Start offset in seconds
	End             float64 // End offset in seconds (0 means until EOF)
	ArtworkPath     string  // Optional path to cover image to embed

	Tags map[string]string // Key-value metadata tags (e.g. title, artist, album, track, date, genre, disc)
}

// Tag returns the value of the metadata tag with the given key, or empty string if not present.
func (r TrackRequest) Tag(key string) string {
	if r.Tags == nil {
		return ""
	}
	return r.Tags[key]
}

// Key computes a deterministic unique cache key for this track request,
// deriving freshness directly from all inputs: source audio facts, time offsets,
// artwork, and all metadata tags in sorted key order.
func (r TrackRequest) Key() string {
	hasher := sha256.New()
	buf := fmt.Appendf(nil, "%s:%d:%d:%.4f:%.4f:%s",
		r.SourceAudioPath,
		r.SourceModTime.UnixNano(),
		r.SourceSize,
		r.Start,
		r.End,
		r.ArtworkPath,
	)
	if len(r.Tags) > 0 {
		keys := make([]string, 0, len(r.Tags))
		for k := range r.Tags {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if v := r.Tags[k]; v != "" {
				buf = fmt.Appendf(buf, ":%s=%s", k, v)
			}
		}
	}
	hasher.Write(buf)
	return hex.EncodeToString(hasher.Sum(nil))[:16]
}
