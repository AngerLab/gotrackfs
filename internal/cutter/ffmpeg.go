package cutter

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
)

// Cutter defines the interface for slicing an audio track into an output file.
type Cutter interface {
	Cut(ctx context.Context, req TrackRequest, outputPath string) error
}

// FFmpegCutter invokes the external ffmpeg binary to slice audio and apply metadata.
type FFmpegCutter struct {
	binPath string
	logger  *slog.Logger
}

// NewFFmpegCutter creates a new FFmpegCutter. If binPath is empty, it searches for "ffmpeg" in PATH.
func NewFFmpegCutter(binPath string, logger *slog.Logger) (*FFmpegCutter, error) {
	if binPath == "" {
		var err error
		binPath, err = exec.LookPath("ffmpeg")
		if err != nil {
			return nil, fmt.Errorf("ffmpeg not found in PATH: %w", err)
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &FFmpegCutter{
		binPath: binPath,
		logger:  logger,
	}, nil
}

// BuildArgs constructs the CLI arguments for ffmpeg.
func (c *FFmpegCutter) BuildArgs(req TrackRequest, outputPath string) []string {
	var args []string
	args = append(args, "-y", "-v", "error") // overwrite output, suppress non-error logs

	// -ss before -i for fast, sample-accurate input seeking
	args = append(args, "-ss", fmt.Sprintf("%.4f", req.Start))
	if req.End > req.Start {
		duration := req.End - req.Start
		args = append(args, "-t", fmt.Sprintf("%.4f", duration))
	}

	args = append(args, "-i", req.SourceAudioPath)

	if req.ArtworkPath != "" {
		args = append(args, "-i", req.ArtworkPath)
		args = append(args, "-map", "0:a", "-map", "1:v")
		args = append(args, "-c:v", "copy", "-disposition:v:0", "attached_pic")
	}

	// Output codec: FLAC
	args = append(args, "-c:a", "flac")

	// Metadata tags (sorted deterministically)
	if len(req.Tags) > 0 {
		keys := make([]string, 0, len(req.Tags))
		for k := range req.Tags {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if v := req.Tags[k]; v != "" {
				args = append(args, "-metadata", fmt.Sprintf("%s=%s", k, v))
			}
		}
	}

	args = append(args, outputPath)
	return args
}

// Cut executes ffmpeg to generate the sliced track.
// If embedding artwork fails (e.g. corrupt or unsupported image format),
// it logs a warning and gracefully retries cutting audio without artwork.
func (c *FFmpegCutter) Cut(ctx context.Context, req TrackRequest, outputPath string) error {
	args := c.BuildArgs(req, outputPath)
	c.logger.Debug("ffmpeg: starting track cut", "source", req.SourceAudioPath, "start", req.Start, "end", req.End, "output", outputPath)

	cmd := exec.CommandContext(ctx, c.binPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if req.ArtworkPath != "" {
			c.logger.Warn("ffmpeg: cut with artwork failed, retrying without artwork",
				"source", req.SourceAudioPath,
				"artwork", req.ArtworkPath,
				"error", err,
				"stderr", string(output),
			)
			_ = os.Remove(outputPath)

			fallbackReq := req
			fallbackReq.ArtworkPath = ""
			fallbackArgs := c.BuildArgs(fallbackReq, outputPath)
			fallbackCmd := exec.CommandContext(ctx, c.binPath, fallbackArgs...)
			fallbackOutput, fallbackErr := fallbackCmd.CombinedOutput()
			if fallbackErr == nil {
				return nil
			}

			c.logger.Error("ffmpeg cut failed (both with and without artwork)",
				"source", req.SourceAudioPath,
				"error", fallbackErr,
				"stderr", string(fallbackOutput),
			)
			return fmt.Errorf("ffmpeg cut failed: %w (output: %s)", fallbackErr, string(fallbackOutput))
		}

		c.logger.Error("ffmpeg cut failed", "error", err, "stderr", string(output), "source", req.SourceAudioPath)
		return fmt.Errorf("ffmpeg cut failed: %w (output: %s)", err, string(output))
	}
	return nil
}
