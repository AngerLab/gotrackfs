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

// FormatBits returns a human-readable description of the bit depth cap (e.g. "24-bit" or "unlimited").
func (q Quality) FormatBits() string {
	if q.Bits <= 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d-bit", q.Bits)
}

// FormatRate returns a human-readable description of the sample rate cap (e.g. "96 kHz (96000 Hz)" or "unlimited").
func (q Quality) FormatRate() string {
	if q.SampleRate <= 0 {
		return "unlimited"
	}
	if q.SampleRate%1000 == 0 {
		return fmt.Sprintf("%d kHz (%d Hz)", q.SampleRate/1000, q.SampleRate)
	}
	return fmt.Sprintf("%.1f kHz (%d Hz)", float64(q.SampleRate)/1000, q.SampleRate)
}

// ValidateBits validates a max-bits cap (only 16 or 24 allowed, 0 = unlimited).
func ValidateBits(b int) (int, error) {
	if b == 0 || b == 16 || b == 24 {
		return b, nil
	}
	return 0, fmt.Errorf("bit depth %d is not supported (FFmpeg FLAC encoder only supports 16 and 24)", b)
}

// maxImplicitKHz is the largest bare number (no unit suffix) still interpreted as kHz.
// Values above it are treated as plain Hz, e.g. "96" -> 96000 but "96000" -> 96000.
const maxImplicitKHz = 384

// ParseRate parses a sample-rate cap string (e.g. "44.1", "48", "96", "44.1k", "96kHz", "44100", "96000").
// Empty string or "0" means unlimited (returns 0, nil).
//
// Values <= maxImplicitKHz or containing a decimal point without an explicit unit suffix are
// interpreted as kHz (e.g. "96" -> 96000 Hz, "44.1" -> 44100 Hz), whereas values above it
// without suffix are interpreted as Hz (e.g. "96000").
func ParseRate(spec string) (int, error) {
	raw := strings.TrimSpace(spec)
	if raw == "" || raw == "0" {
		return 0, nil
	}
	s := strings.ToLower(raw)
	multiplier := 1.0
	switch {
	case strings.HasSuffix(s, "khz"):
		multiplier = 1000
		s = strings.TrimSuffix(s, "khz")
	case strings.HasSuffix(s, "k"):
		multiplier = 1000
		s = strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "hz"):
		s = strings.TrimSuffix(s, "hz")
	}
	s = strings.TrimSpace(s)
	rate, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid sample rate %q: %w", raw, err)
	}
	if rate <= 0 {
		return 0, fmt.Errorf("sample rate %q must be positive", raw)
	}
	// If no explicit unit suffix was provided and value is in kHz range (< 1000 with decimal or standard kHz value), treat as kHz.
	if multiplier == 1.0 && (strings.Contains(s, ".") || rate <= maxImplicitKHz) {
		multiplier = 1000
	}
	rateHz := int(math.Round(rate * multiplier))
	if rateHz < 8000 || rateHz > 1_048_575 { // FLAC 20-bit sample-rate field limit
		return 0, fmt.Errorf("sample rate %q is out of supported range (8 kHz .. 1048575 Hz)", raw)
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
