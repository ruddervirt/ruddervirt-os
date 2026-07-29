package main

import "testing"

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

func TestFindAsset(t *testing.T) {
	assets := []ghAsset{
		{Name: "ruddervirt-install-v1.2.3.iso", BrowserDownloadURL: "https://example.com/iso"},
		{Name: setupBinaryAssetName, BrowserDownloadURL: "https://example.com/bin"},
		{Name: setupChecksumAssetName, BrowserDownloadURL: "https://example.com/sum"},
	}

	if a, ok := findAsset(assets, setupBinaryAssetName); !ok || a.BrowserDownloadURL != "https://example.com/bin" {
		t.Errorf("findAsset binary = %+v, ok=%v", a, ok)
	}
	if a, ok := findAsset(assets, setupChecksumAssetName); !ok || a.BrowserDownloadURL != "https://example.com/sum" {
		t.Errorf("findAsset checksum = %+v, ok=%v", a, ok)
	}
	if _, ok := findAsset(assets, "nonexistent"); ok {
		t.Errorf("findAsset nonexistent: expected ok=false")
	}
}
