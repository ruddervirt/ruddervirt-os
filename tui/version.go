package main

// version is overridden at build time via:
//
//	go build -ldflags "-X main.version=$(VERSION)"
//
// (see Makefile's $(TUI_BIN) rule). Left as "dev" for local `go build`/`make
// build-tui`/`make boot` runs where VERSION isn't set to a real release tag.
var version = "dev"
