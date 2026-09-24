package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/AngerLab/gotrackfs/internal/cutter"
	"github.com/AngerLab/gotrackfs/internal/track"
	"github.com/AngerLab/gotrackfs/internal/vfs"

	flag "github.com/spf13/pflag"
	"github.com/winfsp/cgofuse/fuse"
)

func main() {
	var (
		debug      bool
		keepAlbum  bool
		allowOther bool
		cacheTTL   time.Duration

		maxBits int
		maxRate string
	)
	flag.BoolVar(&debug, "debug", false, "Enable FUSE and VFS debug logging")
	flag.BoolVar(&keepAlbum, "keep-album", false, "Keep monolithic audio file visible alongside virtual tracks")
	flag.BoolVar(&allowOther, "allow-other", false, "Allow other users to access the mount (requires user_allow_other in /etc/fuse.conf)")
	flag.DurationVar(&cacheTTL, "cache-ttl", 5*time.Minute, "Cache time-to-live for sliced tracks after last close (e.g. 5m, 30s)")
	flag.IntVar(&maxBits, "max-bits", 0, "Cap bit depth of sliced tracks (16 or 24; default: 0 = keep source)")
	flag.StringVar(&maxRate, "max-rate", "", "Cap sample rate of sliced tracks in kHz (e.g. 44.1, 48, 96, 192) or Hz (default: unlimited)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <source_dir> <mount_point>\n\nOptions:\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) != 2 {
		flag.Usage()
		os.Exit(2)
	}

	if cacheTTL <= 0 {
		fmt.Fprintf(os.Stderr, "error: --cache-ttl must be positive (got %v)\n", cacheTTL)
		os.Exit(2)
	}

	bits, err := track.ValidateBits(maxBits)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: --max-bits: %v\n", err)
		os.Exit(2)
	}

	rate, err := track.ParseRate(maxRate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: --max-rate: %v\n", err)
		os.Exit(2)
	}
	maxQuality := track.Quality{Bits: bits, SampleRate: rate}

	sourceDir := args[0]
	mountPoint := args[1]

	logLevel := slog.LevelInfo
	if debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)
	if !maxQuality.Unset() {
		logger.Info("output quality cap enabled", "max_bits", maxQuality.Bits, "max_rate_hz", maxQuality.SampleRate, "note", "tracks at or below the cap keep their original format")
	}

	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		log.Fatalf("resolve source dir: %v", err)
	}

	absMount, err := filepath.Abs(mountPoint)
	if err != nil {
		log.Fatalf("resolve mount point: %v", err)
	}

	if _, err := os.Stat(absSource); os.IsNotExist(err) {
		log.Fatalf("source directory does not exist: %s", absSource)
	}
	if _, err := os.Stat(absMount); os.IsNotExist(err) {
		log.Fatalf("mount point directory does not exist: %s", absMount)
	}

	// A nil slicer disables virtual track playback (Open returns ENOSYS).
	slicer := newSlicer(cacheTTL, logger)
	if slicer != nil {
		defer slicer.Close()
	}

	fs := vfs.New(vfs.Options{
		SourceRoot: absSource,
		KeepAlbum:  keepAlbum,
		Logger:     logger,
		Slicer:     slicer,
		MaxQuality: maxQuality,
	})
	host := fuse.NewFileSystemHost(fs)

	var fuseOpts []string
	if debug {
		fuseOpts = append(fuseOpts, "-d")
	}
	if allowOther {
		fuseOpts = append(fuseOpts, "-o", "allow_other")
	}

	// Handle graceful shutdown on Ctrl+C / SIGTERM
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Println("\nUnmounting FUSE filesystem...")
		host.Unmount()
	}()

	fmt.Printf("Mounting gotrackfs:\n"+
		"  Source:      %s\n"+
		"  Mount Point: %s\n"+
		"  Max Bits:    %s\n"+
		"  Max Rate:    %s\n"+
		"  Cache TTL:   %v\n"+
		"  Keep Album:  %t\n"+
		"Press Ctrl+C to unmount.\n",
		absSource, absMount, maxQuality.FormatBits(), maxQuality.FormatRate(), cacheTTL, keepAlbum)

	if !host.Mount(absMount, fuseOpts) {
		log.Fatalf("failed to mount FUSE filesystem at %s", absMount)
	}
}

// newSlicer initializes the audio cache manager backed by ffmpeg.
// It returns nil (disabling virtual track playback) when ffmpeg is missing
// or the cache manager cannot be constructed.
func newSlicer(ttl time.Duration, logger *slog.Logger) *cutter.TrackCacheManager {
	ffmpegCutter, err := cutter.NewFFmpeg("", logger)
	if err != nil {
		logger.Warn("ffmpeg not found in PATH: audio slicing is disabled (install ffmpeg to enable virtual track playback)", "error", err)
		return nil
	}
	manager, err := cutter.NewManager(cutter.Options{
		Cutter: ffmpegCutter,
		TTL:    ttl,
		Logger: logger,
	})
	if err != nil {
		logger.Warn("failed to initialize audio cache manager: audio slicing is disabled", "error", err)
		return nil
	}
	return manager
}
