BIN      := gotrackfs
MODULE   := github.com/AngerLab/gotrackfs

# cgofuse builds against libfuse2 by default; use libfuse3 on Linux
ifeq ($(shell uname),Linux)
GO_TAGS := -tags=fuse3
endif

.PHONY: build test race install clean

build:
	go build $(GO_TAGS) -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/gotrackfs

test:
	go test $(GO_TAGS) ./...

race:
	go test -race $(GO_TAGS) ./...

install:
	go build $(GO_TAGS) -trimpath -ldflags "-s -w" -o "$$(go env GOPATH)/bin/$(BIN)" ./cmd/gotrackfs

clean:
	rm -f $(BIN)
