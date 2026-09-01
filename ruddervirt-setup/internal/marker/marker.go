// SPDX-License-Identifier: GPL-3.0-only

// Package marker holds the "write/read a marker file recording an applied
// value, treat a match as already-done" idiom shared by internal/storage
// and internal/kubevirt - both use a marker file under /var/lib/ruddervirt
// to avoid redoing a one-shot, idempotent host-state change. Deliberately
// not part of internal/versions: the recorded value isn't always a version
// (storage's marker holds an engine name).
package marker

import (
	"os"
	"strings"
)

// Read returns the trimmed contents of the marker file at path. An error
// (most commonly os.ErrNotExist) means nothing has been applied yet - every
// caller treats any read error the same, as "not yet applied."
func Read(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// Write records value at path via write, the caller's privileged file-write
// primitive (internal/config.WritePrivileged) - this package has none of
// its own.
func Write(write func(path string, data []byte) error, path, value string) error {
	return write(path, []byte(value))
}
