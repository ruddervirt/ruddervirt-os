// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSemver(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want parsedSemver
		ok   bool
	}{
		{"clean release", "v1.2.3", parsedSemver{major: 1, minor: 2, patch: 3}, true},
		{"rc suffix rejected", "v1.2.3-rc1", parsedSemver{}, false},
		{"build suffix rejected", "v1.2.3+build1", parsedSemver{}, false},
		{"missing v prefix rejected", "1.2.3", parsedSemver{}, false},
		{"garbage", "not-a-version", parsedSemver{}, false},
	}
	for _, c := range cases {
		got, ok := parseSemver(c.in)
		if ok != c.ok {
			t.Errorf("%s: parseSemver(%q) ok = %v, want %v", c.name, c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("%s: parseSemver(%q) = %+v, want %+v", c.name, c.in, got, c.want)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int // sign only
		ok   bool
	}{
		{"equal", "v1.2.3", "v1.2.3", 0, true},
		{"a newer major", "v2.0.0", "v1.9.9", 1, true},
		{"a older minor", "v1.1.0", "v1.2.0", -1, true},
		{"a older patch", "v1.2.2", "v1.2.3", -1, true},
		{"unparseable a", "garbage", "v1.2.3", 0, false},
		{"unparseable b", "v1.2.3", "garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := compareSemver(c.a, c.b)
		if ok != c.ok {
			t.Errorf("%s: compareSemver(%q, %q) ok = %v, want %v", c.name, c.a, c.b, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		sign := 0
		if got > 0 {
			sign = 1
		} else if got < 0 {
			sign = -1
		}
		if sign != c.want {
			t.Errorf("%s: compareSemver(%q, %q) = %d (sign %d), want sign %d", c.name, c.a, c.b, got, sign, c.want)
		}
	}
}

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

func TestManifestDefinesCRD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator.yaml")
	manifest := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: example
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: kubevirts.kubevirt.io
`
	if err := os.WriteFile(path, []byte(manifest), 0644); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}

	found, err := manifestDefinesCRD(path, "kubevirts.kubevirt.io")
	if err != nil {
		t.Fatalf("manifestDefinesCRD() error = %v", err)
	}
	if !found {
		t.Fatal("manifestDefinesCRD() = false, want true for embedded CRD")
	}

	found, err = manifestDefinesCRD(path, "cdis.cdi.kubevirt.io")
	if err != nil {
		t.Fatalf("manifestDefinesCRD() missing CRD error = %v", err)
	}
	if found {
		t.Fatal("manifestDefinesCRD() = true, want false for missing CRD")
	}
}

func TestKubeVirtCDIInstallSpecs(t *testing.T) {
	expected := map[string]kubevirtCDIInstallSpec{
		"KubeVirt": {
			displayName:            "KubeVirt",
			operatorManifest:       "kubevirt-operator.yaml",
			crdName:                "kubevirts.kubevirt.io",
			customResourceManifest: "kubevirt-cr.yaml",
		},
		"CDI": {
			displayName:            "CDI",
			operatorManifest:       "cdi-operator.yaml",
			crdName:                "cdis.cdi.kubevirt.io",
			customResourceManifest: "cdi-cr.yaml",
		},
	}

	if len(kubevirtCDIInstallSpecs) != len(expected) {
		t.Fatalf("len(kubevirtCDIInstallSpecs) = %d, want %d", len(kubevirtCDIInstallSpecs), len(expected))
	}
	for _, got := range kubevirtCDIInstallSpecs {
		want, ok := expected[got.displayName]
		if !ok {
			t.Fatalf("unexpected install component %q", got.displayName)
		}
		if got != want {
			t.Errorf("install component %q = %#v, want %#v", got.displayName, got, want)
		}
	}
}
