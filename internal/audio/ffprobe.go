package audio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type ffprobeStream struct {
	CodecType        string `json:"codec_type"`
	SampleRate       string `json:"sample_rate"`
	Channels         int    `json:"channels"`
	BitsPerRawSample string `json:"bits_per_raw_sample"`
	BitsPerSample    int    `json:"bits_per_sample"`
	SampleFmt        string `json:"sample_fmt"`
	Duration         string `json:"duration"`
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// ffprobeRunner executes the ffprobe command with args and returns stdout.
// It can be overridden in tests to verify JSON parsing and error handling
// deterministically without requiring ffprobe to be installed.
var ffprobeRunner = func(ctx context.Context, args ...string) ([]byte, error) {
	binPath, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, fmt.Errorf("ffprobe not found in PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, binPath, args...)
	return cmd.Output()
}

func probeWithFFprobe(filePath string) (Info, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := ffprobeRunner(ctx,
		"-v", "error",
		"-show_entries", "stream=codec_type,sample_rate,bits_per_raw_sample,bits_per_sample,sample_fmt,channels,duration:format=duration",
		"-of", "json",
		filePath,
	)
	if err != nil {
		return Info{}, fmt.Errorf("ffprobe execution failed: %w", err)
	}

	return parseFFprobeJSON(out)
}

func parseFFprobeJSON(data []byte) (Info, error) {
	var probeData ffprobeOutput
	if err := json.Unmarshal(data, &probeData); err != nil {
		return Info{}, fmt.Errorf("ffprobe unmarshal: %w", err)
	}

	bestIdx := -1
	for i := range probeData.Streams {
		if probeData.Streams[i].CodecType == "audio" {
			bestIdx = i
			break
		}
	}
	if bestIdx == -1 {
		return Info{}, errors.New("ffprobe: no audio stream found")
	}

	stream := &probeData.Streams[bestIdx]

	var info Info
	info.Format.Channels = stream.Channels
	if stream.SampleRate != "" {
		if rate, err := strconv.Atoi(stream.SampleRate); err == nil {
			info.Format.SampleRate = rate
		}
	}

	if stream.BitsPerRawSample != "" && stream.BitsPerRawSample != "0" {
		if b, err := strconv.Atoi(stream.BitsPerRawSample); err == nil && b > 0 {
			info.Format.Bits = b
		}
	}
	if info.Format.Bits == 0 && stream.BitsPerSample > 0 {
		info.Format.Bits = stream.BitsPerSample
	}
	if info.Format.Bits == 0 && stream.SampleFmt != "" {
		switch {
		case strings.HasPrefix(stream.SampleFmt, "s16"):
			info.Format.Bits = 16
		case strings.HasPrefix(stream.SampleFmt, "s32"), strings.HasPrefix(stream.SampleFmt, "flt"):
			info.Format.Bits = 32
		case strings.HasPrefix(stream.SampleFmt, "dbl"):
			info.Format.Bits = 64
		case strings.HasPrefix(stream.SampleFmt, "u8"), strings.HasPrefix(stream.SampleFmt, "s8"):
			info.Format.Bits = 8
		}
	}

	// Prefer the container-level duration, fall back to the stream-level one.
	if dur := durationSeconds(probeData.Format.Duration); dur > 0 {
		info.Duration = dur
	} else if dur := durationSeconds(stream.Duration); dur > 0 {
		info.Duration = dur
	}

	return info, nil
}

// durationSeconds parses an ffprobe duration string ("123.456" or "N/A"),
// returning 0 when it is unparseable or absent.
func durationSeconds(s string) float64 {
	if s == "" || s == "N/A" {
		return 0
	}
	if dur, err := strconv.ParseFloat(s, 64); err == nil && dur > 0 {
		return dur
	}
	return 0
}
