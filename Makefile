BIN      := gotrackfs
MODULE   := github.com/AngerLab/gotrackfs
VERSION  ?= dev

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
	go install $(MODULE)/cmd/gotrackfs@$(VERSION)

clean:
	rm -f $(BIN)
