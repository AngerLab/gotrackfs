package audio

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/AngerLab/gotrackfs/internal/hostfs"
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
	return ProbeFS(hostfs.Default(), filePath)
}

// ProbeFS is Probe against an injected filesystem: container headers are read
// through fs (MemFS in tests) so the whole build pipeline can run without a
// disk. The ffprobe fallback still executes against the real path and
// therefore cannot help on MemFS — direct header parsing covers FLAC/WAV.
func ProbeFS(fs hostfs.FS, filePath string) (Info, error) {
	f, err := fs.Open(filePath)
	if err != nil {
		return Info{}, fmt.Errorf("probe: open %s: %w", filePath, err)
	}
	defer f.Close()

	info, directErr := probeDirect(f, filepath.Ext(filePath))
	if directErr == nil {
		return info, nil
	}

	ffprobeInfo, ffprobeErr := probeWithFFprobe(filePath)
	if ffprobeErr == nil {
		return ffprobeInfo, nil
	}

	return Info{}, fmt.Errorf("%w (direct: %v; ffprobe: %v)", ErrNotSupported, directErr, ffprobeErr)
}

// probeDirect parses container headers from r. It covers FLAC and WAV in
// pure Go; everything else is unsupported here and left to the ffprobe
// fallback in ProbeFS.
func probeDirect(r io.ReadSeeker, ext string) (Info, error) {
	switch {
	case IsFLACExt(ext):
		flacInfo, err := probeFLACInfo(r)
		if err == nil && flacInfo.Format.SampleRate > 0 && flacInfo.TotalSamples > 0 {
			dur := float64(flacInfo.TotalSamples) / float64(flacInfo.Format.SampleRate)
			return Info{
				Duration: dur,
				Format:   flacInfo.Format,
			}, nil
		}
		if err != nil {
			return Info{}, fmt.Errorf("flac parser: %w", err)
		} else if flacInfo.Format.SampleRate == 0 {
			return Info{}, errors.New("flac parser: zero sample rate")
		}
		return Info{}, errors.New("flac parser: zero total samples (streaming FLAC)")

	case IsWAVExt(ext):
		wavInfo, err := probeWAVInfo(r)
		if err == nil && wavInfo.HasFmt && wavInfo.Format.SampleRate > 0 && wavInfo.ByteRate > 0 && wavInfo.DataSize > 0 {
			dur := float64(wavInfo.DataSize) / float64(wavInfo.ByteRate)
			return Info{
				Duration: dur,
				Format:   wavInfo.Format,
			}, nil
		}
		if err != nil {
			return Info{}, fmt.Errorf("wav parser: %w", err)
		} else if !wavInfo.HasFmt || wavInfo.Format.SampleRate == 0 {
			return Info{}, errors.New("wav parser: missing or invalid fmt chunk")
		}
		return Info{}, errors.New("wav parser: data chunk missing or zero size")

	default:
		return Info{}, fmt.Errorf("unsupported container extension %q", ext)
	}
}
