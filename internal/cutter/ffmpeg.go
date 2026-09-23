package cutter

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"time"

	"github.com/AngerLab/gotrackfs/internal/track"
)

// Cutter defines the interface for slicing an audio track into an output file.
type Cutter interface {
	Cut(ctx context.Context, req track.Slice, outputPath string) error
}

// FFmpeg invokes the external ffmpeg binary to slice audio and apply metadata.
type FFmpeg struct {
	binPath string
	logger  *slog.Logger
	sem     chan struct{}
	timeout time.Duration
}

// NewFFmpeg creates a new FFmpeg cutter. If binPath is empty, it searches for "ffmpeg" in PATH.
func NewFFmpeg(binPath string, logger *slog.Logger) (*FFmpeg, error) {
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
	return &FFmpeg{
		binPath: binPath,
		logger:  logger,
		sem:     make(chan struct{}, 2), // Default: max 2 concurrent ffmpeg cuts
		timeout: 60 * time.Second,       // Default: 60s timeout per cut
	}, nil
}

// SetMaxConcurrency configures the maximum concurrent ffmpeg cut operations.
func (c *FFmpeg) SetMaxConcurrency(n int) {
	if n <= 0 {
		n = 1
	}
	c.sem = make(chan struct{}, n)
}

// BuildArgs constructs the CLI arguments for ffmpeg.
func (c *FFmpeg) BuildArgs(req track.Slice, outputPath string) []string {
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

	// Output codec: FLAC with fast compression and fixed standard block size (4096).
	// Specifying -frame_size 4096 avoids degenerate block sizes (e.g. 160 frames/packet)
	// when aresample is used alongside embedded artwork (attached_pic), which breaks
	// Apple CoreAudio / QuickTime (error 1718449215 / fmt?).
	args = append(args, "-c:a", "flac", "-frame_size", "4096", "-compression_level", "1")

	// Quality cap targets (computed by the VFS at build time).
	// Absent targets mean the source is kept as-is.
	switch {
	case req.TargetBits > 0 && req.TargetBits <= 16:
		// Requantizing to 16-bit (or shallower) is the only lossy step of a
		// quality cap, so push the depth conversion through swresample with
		// f-weighted noise shaping: the quantization error becomes
		// decorrelated noise pushed up to frequencies the ear ignores,
		// instead of signal-correlated distortion (crackle in fades,
		// harmonics on quiet tones). swr also covers any rate conversion in
		// the same stage; for 192->96 material its default profile is more
		// than adequate.
		args = append(args, "-af", "aresample=resampler=swr:dither_method=f_weighted")
	case req.TargetSampleRate > 0:
		// Rate-only cap (or rate cap with bit depth > 16): high-precision 64-tap swr resampler, depth untouched.
		args = append(args, "-af", "aresample=resampler=swr:filter_size=64:phase_shift=12:cutoff=0.949")
	}
	if req.TargetSampleRate > 0 {
		args = append(args, "-ar", strconv.Itoa(req.TargetSampleRate))
	}
	if req.TargetBits > 0 {
		if req.TargetBits <= 16 {
			args = append(args, "-sample_fmt", "s16")
		} else {
			// FLAC stores 17-24 bits in an s32 container; the encoder writes
			// the true depth via bits_per_raw_sample. Depths above 16 come
			// from 24-bit masters, where the cap is a mask the encoder
			// applies deterministically; swresample has no s20 format to
			// dither into, so those stay undithered.
			args = append(args, "-sample_fmt", "s32")
		}
		args = append(args, "-bits_per_raw_sample", strconv.Itoa(req.TargetBits))
	}

	// Metadata tags (sorted deterministically)
	if len(req.Tags) > 0 {
		for _, k := range slices.Sorted(maps.Keys(req.Tags)) {
			if v := req.Tags[k]; v != "" {
				args = append(args, "-metadata", fmt.Sprintf("%s=%s", k, v))
			}
		}
	}

	args = append(args, outputPath)
	return args
}

// execCut runs ffmpeg with args constructed from req and returns the combined output.
func (c *FFmpeg) execCut(ctx context.Context, req track.Slice, outputPath string) ([]byte, error) {
	args := c.BuildArgs(req, outputPath)
	cmd := exec.CommandContext(ctx, c.binPath, args...)
	return cmd.CombinedOutput()
}

// Cut executes ffmpeg to generate the sliced track.
// If embedding artwork fails (e.g. corrupt or unsupported image format),
// it logs a warning and gracefully retries cutting audio without artwork.
func (c *FFmpeg) Cut(ctx context.Context, req track.Slice, outputPath string) error {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	if c.sem != nil {
		select {
		case c.sem <- struct{}{}:
			defer func() { <-c.sem }()
		case <-ctx.Done():
			return fmt.Errorf("ffmpeg cut waiting for slot: %w", ctx.Err())
		}
	}

	c.logger.Debug("ffmpeg: starting track cut", "source", req.SourceAudioPath, "start", req.Start, "end", req.End, "output", outputPath)

	output, err := c.execCut(ctx, req, outputPath)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		_ = os.Remove(outputPath)
		return ctx.Err()
	}

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
		fallbackOutput, fallbackErr := c.execCut(ctx, fallbackReq, outputPath)
		if fallbackErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			_ = os.Remove(outputPath)
			return ctx.Err()
		}

		c.logger.Error("ffmpeg cut failed (both with and without artwork)",
			"source", req.SourceAudioPath,
			"error", fallbackErr,
			"stderr", string(fallbackOutput),
		)
		return fmt.Errorf("ffmpeg cut failed (with artwork: %v; without artwork: %w; output: %s)", err, fallbackErr, string(fallbackOutput))
	}

	c.logger.Error("ffmpeg cut failed", "error", err, "stderr", string(output), "source", req.SourceAudioPath)
	return fmt.Errorf("ffmpeg cut failed: %w (output: %s)", err, string(output))
}
