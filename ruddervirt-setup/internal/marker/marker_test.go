// SPDX-License-Identifier: GPL-3.0-only

package marker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.applied")
	if _, err := Read(path); err == nil {
		t.Fatal("Read(missing) err = nil, want an error")
	}
}

func TestReadTrimsWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marker.applied")
	if err := os.WriteFile(path, []byte("  v1.2.3\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read err = %v, want nil", err)
	}
	if got != "v1.2.3" {
		t.Errorf("Read = %q, want %q", got, "v1.2.3")
	}
}

func TestWriteCallsWriteFunc(t *testing.T) {
	var gotPath string
	var gotData []byte
	write := func(path string, data []byte) error {
		gotPath = path
		gotData = data
		return nil
	}
	if err := Write(write, "/var/lib/ruddervirt/thing.applied", "openebs"); err != nil {
		t.Fatalf("Write err = %v, want nil", err)
	}
	if gotPath != "/var/lib/ruddervirt/thing.applied" {
		t.Errorf("write called with path %q, want %q", gotPath, "/var/lib/ruddervirt/thing.applied")
	}
	if string(gotData) != "openebs" {
		t.Errorf("write called with data %q, want %q", gotData, "openebs")
	}
}

func TestWritePropagatesError(t *testing.T) {
	wantErr := errors.New("permission denied")
	write := func(path string, data []byte) error { return wantErr }
	if err := Write(write, "/some/path", "value"); !errors.Is(err, wantErr) {
		t.Errorf("Write err = %v, want %v", err, wantErr)
	}
}
