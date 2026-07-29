package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKubeVirtCDIManifestsPresent(t *testing.T) {
	dir := t.TempDir() + string(filepath.Separator)

	for _, name := range kubevirtCDIManifestFiles {
		if err := os.WriteFile(dir+name, []byte("placeholder"), 0644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	if !kubevirtCDIManifestsPresent(dir) {
		t.Errorf("kubevirtCDIManifestsPresent(%q) = false, want true with all files present", dir)
	}

	if err := os.Remove(dir + kubevirtCDIManifestFiles[0]); err != nil {
		t.Fatalf("removing %s: %v", kubevirtCDIManifestFiles[0], err)
	}
	if kubevirtCDIManifestsPresent(dir) {
		t.Errorf("kubevirtCDIManifestsPresent(%q) = true, want false after removing %s", dir, kubevirtCDIManifestFiles[0])
	}
}

func TestResolveKubeVirtAndCDIVersion(t *testing.T) {
	cases := []struct {
		name         string
		cfg          Config
		wantKubeVirt string
		wantCDI      string
	}{
		{"unset falls back to defaults", Config{}, defaultKubeVirtVersion, defaultCDIVersion},
		{
			"explicit values are trimmed and passed through",
			Config{Versions: VersionsConfig{KubeVirt: "  v1.9.0  ", CDI: "  v1.65.0  "}},
			"v1.9.0", "v1.65.0",
		},
	}
	for _, c := range cases {
		if got := resolveKubeVirtVersion(c.cfg); got != c.wantKubeVirt {
			t.Errorf("%s: resolveKubeVirtVersion() = %q, want %q", c.name, got, c.wantKubeVirt)
		}
		if got := resolveCDIVersion(c.cfg); got != c.wantCDI {
			t.Errorf("%s: resolveCDIVersion() = %q, want %q", c.name, got, c.wantCDI)
		}
	}
}
