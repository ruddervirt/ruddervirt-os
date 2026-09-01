// SPDX-License-Identifier: GPL-3.0-only

package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSha256File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// sha256("hello world")
	const want = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	got, err := sha256File(path)
	if err != nil {
		t.Fatalf("sha256File(%q) err = %v, want nil", path, err)
	}
	if got != want {
		t.Errorf("sha256File(%q) = %q, want %q", path, got, want)
	}

	if _, err := sha256File(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("sha256File(missing) err = nil, want an error")
	}
}

// Version-tag parsing/comparison is covered by
// internal/versions/versions_test.go's TestParseSemver/TestCompareSemver;
// see update.go's comment on why this package uses versions.CompareSemver
// directly instead of its own comparator.

func TestSetupBinaryURL(t *testing.T) {
	const want = "https://raw.githubusercontent.com/ruddervirt/ruddervirt-os/release-binaries/v1.2.3/ruddervirt-setup"
	if got := SetupBinaryURL("v1.2.3"); got != want {
		t.Errorf("SetupBinaryURL(v1.2.3) = %q, want %q", got, want)
	}
}

func TestSetupChecksumURL(t *testing.T) {
	const want = "https://raw.githubusercontent.com/ruddervirt/ruddervirt-os/release-binaries/v1.2.3/ruddervirt-setup.sha256"
	if got := SetupChecksumURL("v1.2.3"); got != want {
		t.Errorf("SetupChecksumURL(v1.2.3) = %q, want %q", got, want)
	}
}
