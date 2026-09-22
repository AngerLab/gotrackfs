package audio

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrNotSupported is returned when the audio format is not supported or duration cannot be determined.
var ErrNotSupported = errors.New("audio: unsupported format or unreadable duration")

// Format holds basic source-audio properties discovered by reading container
// headers (FLAC STREAMINFO or WAV fmt chunk) or ffprobe.
// Zero values mean "unknown".
type Format struct {
	SampleRate int // Hz
	Bits       int // bits per sample
	Channels   int // channel count
}

// Info holds basic source-audio metadata (duration and format) discovered
// by reading container headers or ffprobe.
type Info struct {
	Duration float64 // seconds (0 if unknown)
	Format   Format
}

// Probe reads container headers (FLAC STREAMINFO or WAV fmt/data chunks) once in pure Go.
// If the container is not FLAC/WAV, direct header parsing fails, or WAV duration cannot be determined,
// it falls back to ffprobe. On total failure, it wraps ErrNotSupported with underlying diagnostic causes.
func Probe(filePath string) (Info, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	var directErr error

	switch ext {
	case ".flac":
		flacInfo, err := probeFLACInfo(filePath)
		if err == nil && flacInfo.Format.SampleRate > 0 && flacInfo.TotalSamples > 0 {
			dur := float64(flacInfo.TotalSamples) / float64(flacInfo.Format.SampleRate)
			return Info{
				Duration: dur,
				Format:   flacInfo.Format,
			}, nil
		}
		if err != nil {
			directErr = fmt.Errorf("flac parser: %w", err)
		} else if flacInfo.Format.SampleRate == 0 {
			directErr = errors.New("flac parser: zero sample rate")
		} else {
			directErr = errors.New("flac parser: zero total samples (streaming FLAC)")
		}

	case ".wav", ".wave":
		wavInfo, err := probeWAVInfo(filePath)
		if err == nil && wavInfo.HasFmt && wavInfo.Format.SampleRate > 0 && wavInfo.ByteRate > 0 && wavInfo.DataSize > 0 {
			dur := float64(wavInfo.DataSize) / float64(wavInfo.ByteRate)
			return Info{
				Duration: dur,
				Format:   wavInfo.Format,
			}, nil
		}
		if err != nil {
			directErr = fmt.Errorf("wav parser: %w", err)
		} else if !wavInfo.HasFmt || wavInfo.Format.SampleRate == 0 {
			directErr = errors.New("wav parser: missing or invalid fmt chunk")
		} else {
			directErr = errors.New("wav parser: data chunk missing or zero size")
		}

	default:
		directErr = fmt.Errorf("unsupported container extension %q", ext)
	}

	info, ffprobeErr := probeWithFFprobe(filePath)
	if ffprobeErr == nil {
		return info, nil
	}

	return Info{}, fmt.Errorf("%w (direct: %v; ffprobe: %v)", ErrNotSupported, directErr, ffprobeErr)
}
