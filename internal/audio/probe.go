package audio

import (
	"errors"
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
// If the container is not FLAC/WAV or direct header parsing fails, it falls back to ffprobe.
func Probe(filePath string) (Info, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".flac":
		flacInfo, err := probeFLACInfo(filePath)
		if err == nil && flacInfo.Format.SampleRate > 0 {
			var dur float64
			if flacInfo.TotalSamples > 0 {
				dur = float64(flacInfo.TotalSamples) / float64(flacInfo.Format.SampleRate)
			}
			return Info{
				Duration: dur,
				Format:   flacInfo.Format,
			}, nil
		}
	case ".wav", ".wave":
		wavInfo, err := probeWAVInfo(filePath)
		if err == nil && wavInfo.HasFmt && wavInfo.Format.SampleRate > 0 {
			var dur float64
			if wavInfo.ByteRate > 0 && wavInfo.DataSize > 0 {
				dur = float64(wavInfo.DataSize) / float64(wavInfo.ByteRate)
			}
			return Info{
				Duration: dur,
				Format:   wavInfo.Format,
			}, nil
		}
	}

	info, err := probeWithFFprobe(filePath)
	if err == nil {
		return info, nil
	}
	return Info{}, ErrNotSupported
}
