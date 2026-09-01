// SPDX-License-Identifier: GPL-3.0-only

package versions

// Version is overridden at build time via:
//
//	go build -ldflags "-X ruddervirt-setup/internal/versions.Version=$(VERSION)"
//
// (see Makefile's $(TUI_BIN) rule). Left as "dev" for local `go build`/`make
// build-tui`/`make boot` runs where VERSION isn't set to a real release tag.
var Version = "dev"
