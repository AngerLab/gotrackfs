BIN      := gotrackfs
MODULE   := github.com/AngerLab/gotrackfs
VERSION  ?= dev

.PHONY: build test race install clean

build:
	go build -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/gotrackfs

test:
	go test ./...

race:
	go test -race ./...

install:
	go install $(MODULE)/cmd/gotrackfs@$(VERSION)

clean:
	rm -f $(BIN)
