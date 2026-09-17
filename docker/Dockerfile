# Multi-stage build: compile gotrackfs against libfuse3, ship in a slim
# alpine image with ffmpeg for on-demand track slicing.

# NOTE: builder and runtime MUST use the same alpine release:
# the cgo binary is linked against the libfuse3 ABI of the builder's fuse3-dev.
FROM golang:1.27-alpine3.24 AS builder

RUN apk add --no-cache gcc musl-dev pkgconf fuse3-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=1 GOFLAGS=-tags=fuse3 \
    go build -trimpath -ldflags "-s -w" \
    -o /usr/local/bin/gotrackfs ./cmd/gotrackfs

FROM alpine:3.24

# ffmpeg is required for on-demand track slicing at runtime.
# cgofuse dlopen()s "libfuse3.so.3", but recent alpine ships libfuse3.so.4
# (soname bump in libfuse 3.18) - add a compatibility symlink.
RUN apk add --no-cache ffmpeg fuse3 ca-certificates \
    && ln -sf /usr/lib/libfuse3.so.4 /usr/lib/libfuse3.so.3

# Allow non-root mounters to use the allow_other option
RUN echo "user_allow_other" >> /etc/fuse.conf

# FUSE requires that the user who mounts the filesystem has an entry in
# /etc/passwd. Since the uid is only known at runtime (docker --user),
# we open /etc/passwd for writing and let the launcher add the entry.
RUN chmod 666 /etc/passwd

# Music library (mounted read-only) and virtual mount point
VOLUME /src
VOLUME /dst

COPY docker/launcher.sh /usr/local/bin/
RUN chmod 555 /usr/local/bin/launcher.sh

COPY --from=builder /usr/local/bin/gotrackfs /usr/local/bin/gotrackfs

ENTRYPOINT ["/usr/local/bin/launcher.sh", "/src", "/dst"]
