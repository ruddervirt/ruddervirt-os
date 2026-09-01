// SPDX-License-Identifier: GPL-3.0-only

package main

import "strings"

// cmdContains reports whether every one of substrs appears somewhere in
// name+args joined by spaces - used to key off a command's shape without
// hardcoding the exact sudo/-n wrapping runNonInteractive applies.
// Package-main-local copy of internal/exec/exectest's identical
// CmdContains, kept here since it's used pervasively across test files
// otherwise unrelated to internal/exec.
func cmdContains(name string, args []string, substrs ...string) bool {
	line := strings.Join(append([]string{name}, args...), " ")
	for _, s := range substrs {
		if !strings.Contains(line, s) {
			return false
		}
	}
	return true
}

// hasField reports whether tok appears as an exact space-separated token in
// line - unlike cmdContains, which does substring matching and so would
// wrongly match "add" inside "ipv4.addresses".
func hasField(line, tok string) bool {
	for _, f := range strings.Fields(line) {
		if f == tok {
			return true
		}
	}
	return false
}
