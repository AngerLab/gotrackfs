# gotrackfs

**gotrackfs** is a read-only [FUSE](https://github.com/winfsp/cgofuse) filesystem that turns
`album.cue + monolithic-audio` into individual track files, cut on demand.

Mount your music library through it, and players see `01. Intro.flac`, `02. Next.flac`…
instead of a giant `image.flac` and a `album.cue` nobody wants to parse.

```
SOURCE/Artist/Album/           VIRTUAL/Artist/Album/
├── album.flac        →        ├── 01. Title.flac      (virtual, cut on open)
├── album.cue         →        ├── 02. Track 02.flac
├── cover.jpg         →        ├── album.cue           (kept as-is)
└── log.txt           →        ├── cover.jpg
                               └── ...
```

## Features

- **Any source, FLAC output** — FLAC, WAV, APE, WV, M4A, MP3 images; every virtual
  track is a real FLAC re-encoded with tags from the CUE sheet and embedded artwork.
- **Multi-album folders** — `CD1.cue` + `CD2.cue` become virtual `CD1/` and `CD2/`
  subdirectories, artwork mirrored inside. Name collisions are resolved with
  deterministic suffixes, never silently.
- **On-demand slicing** — tracks are materialized only when opened, cached with
  reference counting + TTL, and aborted when all waiting readers go away.
  ffmpeg concurrency is bounded; every cut has a timeout.
- **Exact metadata without ffmpeg** — track durations are read directly from
  FLAC `STREAMINFO` / WAVE headers (pure Go, no external tools), so the last
  track gets exact bounds and reported sizes are honest after the first slice.
- **Cache that doesn't lie** — directory state is invalidated by filesystem facts
  (directory mtime), the slicer cache is keyed by *all* cut inputs including the
  source file's mtime/size. Concurrent callers share one parse / one cut (singleflight).
- **Read-only and safe** — write attempts are refused; the source tree is never modified.
- **Unicode-safe paths** — NFC/NFD normalization handled transparently
  (macOS ↔ Windows ↔ Linux interop), synthetic stable inode numbers.
- **`--keep-album`** — keep the monolithic audio file visible alongside virtual tracks.
- **`--max-quality 24/96`** — optionally cap sliced output quality; tracks at or
  below the cap keep the source format, cuts down to 16 bit get noise-shaped dither.

## Requirements

- [ffmpeg](https://ffmpeg.org) in `PATH` (runtime only; not needed to build)
- **macOS**: [FUSE-T](https://github.com/macos-fuse-t/fuse-t) (recommended — no kernel
  extension, works on Apple Silicon; macFUSE is also supported)
- **Linux**: FUSE 3 (`fuse3` / `libfuse3-dev` package)

## Install

Grab a prebuilt binary from [Releases](https://github.com/AngerLab/gotrackfs/releases)
(`darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64`), or build from source:

```sh
git clone https://github.com/AngerLab/gotrackfs && cd gotrackfs && make install
```

## Usage

```sh
gotrackfs [options] SOURCE VIRTUAL

# Mount ~/Music as a virtual library in ./VIRTUAL
gotrackfs ~/Music ./VIRTUAL
```

| Flag | Description |
|------|-------------|
| `-debug` | Verbose FUSE and VFS debug logging |
| `-keep-album` | Keep monolithic audio files visible next to virtual tracks |
| `-allow-other` | Allow other users to access the mount (requires `user_allow_other` in `/etc/fuse.conf`) |
| `-cache-ttl` | Cache time-to-live for sliced tracks after last close (default `5m`, e.g. `10m`, `30s`) |
| `-max-quality` | Cap output quality of sliced tracks as `bits/rate`, e.g. `24/96`, `16/44.1` (rate-only `96` works too). Each dimension is lowered only if the source is above the cap; everything at or below stays bit-exact. Depth reductions to 16 bit are dithered (f-weighted noise shaping). Default: empty = original format |

Unmount with `Ctrl+C` (graceful SIGINT/SIGTERM handling included).

Linux, if you need to do it by hand:

```sh
fusermount3 -u VIRTUAL
```

## Docker

On any Linux system with Docker and FUSE, no local installation is needed:

```sh
docker run --rm \
    --name=gotrackfs \
    --device /dev/fuse \
    --cap-add SYS_ADMIN \
    --security-opt apparmor:unconfined \
    -v /path/to/yourmusiclibrary:/src:ro \
    -v /path/to/yourmountpoint:/dst:rshared \
    ghcr.io/angerlab/gotrackfs
```

- `-v .../library:/src:ro` — your music library, mounted read-only
- `-v .../mountpoint:/dst:rshared` — the virtual track library, visible on the host
- `--device /dev/fuse --cap-add SYS_ADMIN` — privileges required to mount FUSE
- ffmpeg is bundled in the image; tracks are sliced on demand inside the container

The container mounts with `allow_other` automatically, so the mounted content is
visible to your regular host user. Extra flags go after the image name,
e.g. `... gotrackfs -keep-album`.

## How it works

- For each directory containing CUE sheet(s), gotrackfs computes a virtual
  state: real files pass through untouched, the monolithic audio file is hidden,
  and one virtual FLAC per track is exposed.
- Opening a virtual track triggers an ffmpeg slice (`-ss/-t`, sample-accurate
  for FLAC/WAV) into a per-track temp file, executed by at most a few concurrent
  processes. The slice is cached and refcounted; TTL cleanup keeps the temp dir
  bounded; cuts are cancelled when no reader is left waiting.
- Everything else (covers, logs, scans, unrelated files) is a byte-exact
  pass-through.
- Multi-file CUEs (albums already split per track) are left untouched.

### Caveats

- Re-ripping an audio file *in place* (same path, no rename) is not detected
  until the containing directory's mtime changes. Rippers that write-then-rename
  are detected normally.
- Slices live in `$TMPDIR/gotrackfs-*` and are cleaned up on unmount.

## Development

```sh
make test    # go test ./...
make race    # go test -race ./...
make build   # ./gotrackfs
```

The suite covers CUE parsing, cache invalidation, filename collision handling,
unicode normalization, and the slicer lifecycle (concurrency, cancellation, TTL).

## License

[MIT](LICENSE)
