package main

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

func TestParseSetupVersion(t *testing.T) {
	cases := []struct {
		in                  string
		major, minor, patch int
		ok                  bool
	}{
		{"v1.2.3", 1, 2, 3, true},
		{"v0.0.1", 0, 0, 1, true},
		{"  v2.10.0  ", 2, 10, 0, true},
		{"dev", 0, 0, 0, false},
		{"", 0, 0, 0, false},
		{"v1.2", 0, 0, 0, false},
		{"1.2.3", 0, 0, 0, false},
		{"v1.2.3-rc1", 0, 0, 0, false},
	}
	for _, c := range cases {
		major, minor, patch, ok := parseSetupVersion(c.in)
		if ok != c.ok {
			t.Errorf("parseSetupVersion(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if major != c.major || minor != c.minor || patch != c.patch {
			t.Errorf("parseSetupVersion(%q) = %d.%d.%d, want %d.%d.%d",
				c.in, major, minor, patch, c.major, c.minor, c.patch)
		}
	}
}

func TestCompareSetupVersions(t *testing.T) {
	cases := []struct {
		a, b string
		cmp  int // sign only
		ok   bool
	}{
		{"v1.2.3", "v1.2.3", 0, true},
		{"v1.2.4", "v1.2.3", 1, true},
		{"v1.2.3", "v1.2.4", -1, true},
		{"v1.3.0", "v1.2.9", 1, true},
		{"v2.0.0", "v1.9.9", 1, true},
		{"dev", "v1.2.3", 0, false},
		{"v1.2.3", "dev", 0, false},
		{"", "v1.2.3", 0, false},
	}
	for _, c := range cases {
		cmp, ok := compareSetupVersions(c.a, c.b)
		if ok != c.ok {
			t.Errorf("compareSetupVersions(%q, %q) ok = %v, want %v", c.a, c.b, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		got := sign(cmp)
		if got != c.cmp {
			t.Errorf("compareSetupVersions(%q, %q) sign = %d, want %d", c.a, c.b, got, c.cmp)
		}
	}
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}

func TestSetupBinaryURL(t *testing.T) {
	const want = "https://raw.githubusercontent.com/ruddervirt/ruddervirt-os/release-binaries/v1.2.3/ruddervirt-setup"
	if got := setupBinaryURL("v1.2.3"); got != want {
		t.Errorf("setupBinaryURL(v1.2.3) = %q, want %q", got, want)
	}
}

func TestSetupChecksumURL(t *testing.T) {
	const want = "https://raw.githubusercontent.com/ruddervirt/ruddervirt-os/release-binaries/v1.2.3/ruddervirt-setup.sha256"
	if got := setupChecksumURL("v1.2.3"); got != want {
		t.Errorf("setupChecksumURL(v1.2.3) = %q, want %q", got, want)
	}
}
