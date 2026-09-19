package track

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Quality is an optional output quality cap applied to sliced tracks.
// A zero field means "unlimited for that dimension"; a zero Quality caps nothing,
// and cuts stay bit-identical in format to the source (original rate and depth).
//
// The cap is per dimension: a source strictly above the cap in one dimension
// is lowered to it, sources at or below the cap are left untouched.
type Quality struct {
	Bits       int // Max bits per sample (0 = unlimited)
	SampleRate int // Max sample rate in Hz (0 = unlimited)
}

// Unset reports whether the quality cap imposes no limits.
func (q Quality) Unset() bool { return q.Bits == 0 && q.SampleRate == 0 }

// String renders the cap in "bits/rate" notation ("24/96"), used for logs.
func (q Quality) String() string {
	bits := "*"
	if q.Bits > 0 {
		bits = strconv.Itoa(q.Bits)
	}
	rate := "*"
	if q.SampleRate > 0 {
		rate = strconv.Itoa(q.SampleRate)
	}
	return bits + "/" + rate
}

// ParseBits validates a max-bits cap (only 16 or 24 allowed, 0 = unlimited).
func ParseBits(b int) (int, error) {
	if b == 0 || b == 16 || b == 24 {
		return b, nil
	}
	return 0, fmt.Errorf("bit depth %d is not supported (FFmpeg FLAC encoder only supports 16 and 24)", b)
}

// ParseRate parses a sample-rate cap string (e.g. "44.1", "48", "88.2", "96", "176.4", "192", or in Hz like "44100", "96000").
// Empty string or "0" means unlimited (returns 0, nil).
func ParseRate(spec string) (int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "0" {
		return 0, nil
	}
	rate, err := strconv.ParseFloat(spec, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid sample rate %q: %w", spec, err)
	}
	if rate > 0 && rate < 1000 { // kHz notation like 44.1, 48, 96, 192
		rate *= 1000
	}
	rateHz := int(math.Round(rate))
	if rateHz < 1000 || rateHz > 1_048_575 { // FLAC 20-bit sample-rate field limit
		return 0, fmt.Errorf("sample rate %s is out of range (1 kHz .. 1048575 Hz)", spec)
	}
	return rateHz, nil
}

// Plan computes per-track ffmpeg targets for a source of the given format.
// Returned zeros mean "keep the source value". A cap is strictly an upper bound:
// if the source format is unknown (zero srcRate/srcBits), the source is left untouched
// to prevent accidental upsampling.
func (q Quality) Plan(srcRate, srcBits int) (targetRate, targetBits int) {
	if q.SampleRate > 0 && srcRate > 0 && srcRate > q.SampleRate {
		targetRate = q.SampleRate
	}
	if q.Bits > 0 && srcBits > 0 && srcBits > q.Bits {
		targetBits = q.Bits
	}
	return targetRate, targetBits
}
