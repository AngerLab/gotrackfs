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

// flacBitDepths are the bit depths FLAC can encode.
var flacBitDepths = [...]int{8, 12, 16, 20, 24}

// ParseQuality parses a quality-cap spec for command-line flags.
// Accepted forms:
//
//	"24/96"     bits and max sample rate in kHz
//	"24/96000"  bits and max sample rate in Hz
//	"16/44.1"   kHz notation with a fractional rate
//	"96"        sample-rate cap only (kHz or Hz by magnitude)
//	""          no cap
//
// Bits must be a legal FLAC depth (8, 12, 16, 20, 24). Rates below 1000 are
// interpreted as kHz.
func ParseQuality(spec string) (Quality, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Quality{}, nil
	}

	var bitsSpec, rateSpec string
	if n := strings.Count(spec, "/"); n == 1 {
		parts := strings.SplitN(spec, "/", 2)
		bitsSpec = strings.TrimSpace(parts[0])
		rateSpec = strings.TrimSpace(parts[1])
		if bitsSpec == "" || rateSpec == "" {
			return Quality{}, fmt.Errorf("want \"bits/rate\" like \"24/96\", got %q", spec)
		}
	} else if n > 1 {
		return Quality{}, fmt.Errorf("want \"bits/rate\" like \"24/96\", got %q", spec)
	} else {
		rateSpec = spec
	}

	var q Quality
	if bitsSpec != "" {
		bits, err := strconv.Atoi(bitsSpec)
		if err != nil {
			return Quality{}, fmt.Errorf("invalid bit depth in %q: %w", spec, err)
		}
		if !isFlacDepth(bits) {
			return Quality{}, fmt.Errorf("bit depth %d is not a legal FLAC depth (want one of 8, 12, 16, 20, 24)", bits)
		}
		q.Bits = bits
	}

	rate, err := strconv.ParseFloat(rateSpec, 64)
	if err != nil {
		return Quality{}, fmt.Errorf("invalid sample rate in %q: %w", spec, err)
	}
	if rate > 0 && rate < 1000 { // kHz notation
		rate *= 1000
	}
	rateHz := int(math.Round(rate))
	if rateHz < 1000 || rateHz > 1_048_575 { // FLAC 20-bit sample-rate field
		return Quality{}, fmt.Errorf("sample rate %s is out of range (1 kHz .. 1048575 Hz)", rateSpec)
	}
	q.SampleRate = rateHz
	return q, nil
}

func isFlacDepth(b int) bool {
	for _, d := range flacBitDepths {
		if b == d {
			return true
		}
	}
	return false
}

// Plan computes per-track ffmpeg targets for a source of the given format.
// Returned zeros mean "keep the source value". When the source format is
// unknown (zero srcRate/srcBits), the cap is applied blindly so the output
// still respects the configured limit.
func (q Quality) Plan(srcRate, srcBits int) (targetRate, targetBits int) {
	if q.SampleRate > 0 && (srcRate == 0 || srcRate > q.SampleRate) {
		targetRate = q.SampleRate
	}
	if q.Bits > 0 {
		// Round the cap down to a legal FLAC depth (e.g. cap 22 encodes as 20).
		depth := q.Bits
		for !isFlacDepth(depth) {
			depth--
		}
		if srcBits == 0 || srcBits > depth {
			targetBits = depth
		}
	}
	return targetRate, targetBits
}
