package audio

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type ffprobeOutput struct {
	Streams []struct {
		CodecType        string `json:"codec_type"`
		SampleRate       string `json:"sample_rate"`
		Channels         int    `json:"channels"`
		BitsPerRawSample string `json:"bits_per_raw_sample"`
		BitsPerSample    int    `json:"bits_per_sample"`
		SampleFmt        string `json:"sample_fmt"`
		Duration         string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func probeWithFFprobe(filePath string) (Info, error) {
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return Info{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-show_entries", "stream=codec_type,sample_rate,bits_per_raw_sample,bits_per_sample,sample_fmt,channels,duration:format=duration",
		"-of", "json",
		filePath,
	)
	out, err := cmd.Output()
	if err != nil {
		return Info{}, err
	}

	var data ffprobeOutput
	if err := json.Unmarshal(out, &data); err != nil {
		return Info{}, err
	}

	var bestStream *struct {
		CodecType        string `json:"codec_type"`
		SampleRate       string `json:"sample_rate"`
		Channels         int    `json:"channels"`
		BitsPerRawSample string `json:"bits_per_raw_sample"`
		BitsPerSample    int    `json:"bits_per_sample"`
		SampleFmt        string `json:"sample_fmt"`
		Duration         string `json:"duration"`
	}

	for i := range data.Streams {
		if data.Streams[i].CodecType == "audio" {
			bestStream = &data.Streams[i]
			break
		}
	}
	if bestStream == nil && len(data.Streams) > 0 {
		bestStream = &data.Streams[0]
	}
	if bestStream == nil {
		return Info{}, errors.New("ffprobe: no audio stream found")
	}

	var info Info
	info.Format.Channels = bestStream.Channels
	if bestStream.SampleRate != "" {
		if rate, err := strconv.Atoi(bestStream.SampleRate); err == nil {
			info.Format.SampleRate = rate
		}
	}

	if bestStream.BitsPerRawSample != "" && bestStream.BitsPerRawSample != "0" {
		if b, err := strconv.Atoi(bestStream.BitsPerRawSample); err == nil && b > 0 {
			info.Format.Bits = b
		}
	}
	if info.Format.Bits == 0 && bestStream.BitsPerSample > 0 {
		info.Format.Bits = bestStream.BitsPerSample
	}
	if info.Format.Bits == 0 && bestStream.SampleFmt != "" {
		switch {
		case strings.HasPrefix(bestStream.SampleFmt, "s16"):
			info.Format.Bits = 16
		case strings.HasPrefix(bestStream.SampleFmt, "s32"), strings.HasPrefix(bestStream.SampleFmt, "flt"):
			info.Format.Bits = 32
		case strings.HasPrefix(bestStream.SampleFmt, "dbl"):
			info.Format.Bits = 64
		case strings.HasPrefix(bestStream.SampleFmt, "u8"):
			info.Format.Bits = 8
		}
	}

	if data.Format.Duration != "" && data.Format.Duration != "N/A" {
		if dur, err := strconv.ParseFloat(data.Format.Duration, 64); err == nil && dur > 0 {
			info.Duration = dur
		}
	}
	if info.Duration == 0 && bestStream.Duration != "" && bestStream.Duration != "N/A" {
		if dur, err := strconv.ParseFloat(bestStream.Duration, 64); err == nil && dur > 0 {
			info.Duration = dur
		}
	}

	return info, nil
}
