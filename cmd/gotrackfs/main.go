package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/AngerLab/gotrackfs/internal/cutter"
	"github.com/AngerLab/gotrackfs/internal/vfs"

	"github.com/winfsp/cgofuse/fuse"
)

func main() {
	var (
		debug      bool
		keepAlbum  bool
		allowOther bool
		cacheTTL   time.Duration
	)
	flag.BoolVar(&debug, "debug", false, "Enable FUSE and VFS debug logging")
	flag.BoolVar(&keepAlbum, "keep-album", false, "Keep monolithic audio file visible alongside virtual tracks")
	flag.BoolVar(&allowOther, "allow-other", false, "Allow other users to access the mount (requires user_allow_other in /etc/fuse.conf)")
	flag.DurationVar(&cacheTTL, "cache-ttl", 5*time.Minute, "Cache time-to-live for sliced tracks after last close (e.g. 5m, 30s)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <source_dir> <mount_point>\n\nOptions:\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		flag.Usage()
		os.Exit(2)
	}

	if cacheTTL <= 0 {
		fmt.Fprintf(os.Stderr, "error: -cache-ttl must be positive (got %v)\n", cacheTTL)
		os.Exit(2)
	}

	sourceDir := args[0]
	mountPoint := args[1]

	logLevel := slog.LevelInfo
	if debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

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

	// Initialize audio cutter if ffmpeg is available
	var slicer vfs.TrackSlicer
	ffmpegCutter, err := cutter.NewFFmpegCutter("", logger)
	if err != nil {
		logger.Warn("ffmpeg not found in PATH: audio slicing is disabled (install ffmpeg to enable virtual track playback)", "error", err)
	} else {
		cutterMgr, err := cutter.NewManager(cutter.Options{
			Cutter: ffmpegCutter,
			TTL:    cacheTTL,
			Logger: logger,
		})
		if err != nil {
			logger.Warn("failed to initialize audio cache manager: audio slicing is disabled", "error", err)
		} else {
			defer cutterMgr.Close()
			slicer = cutterMgr
		}
	}

	fs := vfs.New(vfs.Options{
		SourceRoot: absSource,
		KeepAlbum:  keepAlbum,
		Debug:      debug,
		Logger:     logger,
		Slicer:     slicer,
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

	fmt.Printf("Mounting gotrackfs:\n  Source:      %s\n  Mount Point: %s\nPress Ctrl+C to unmount.\n", absSource, absMount)

	if !host.Mount(absMount, fuseOpts) {
		log.Fatalf("failed to mount FUSE filesystem at %s", absMount)
	}
}
