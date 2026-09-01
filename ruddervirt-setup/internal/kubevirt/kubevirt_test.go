// SPDX-License-Identifier: GPL-3.0-only

package kubevirt

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
	expected := map[string]CDIInstallSpec{
		"KubeVirt": {
			DisplayName:            "KubeVirt",
			OperatorManifest:       "kubevirt-operator.yaml",
			CRDName:                "kubevirts.kubevirt.io",
			CustomResourceManifest: "kubevirt-cr.yaml",
		},
		"CDI": {
			DisplayName:            "CDI",
			OperatorManifest:       "cdi-operator.yaml",
			CRDName:                "cdis.cdi.kubevirt.io",
			CustomResourceManifest: "cdi-cr.yaml",
		},
	}

	if len(CDIInstallSpecs) != len(expected) {
		t.Fatalf("len(CDIInstallSpecs) = %d, want %d", len(CDIInstallSpecs), len(expected))
	}
	for _, got := range CDIInstallSpecs {
		want, ok := expected[got.DisplayName]
		if !ok {
			t.Fatalf("unexpected install component %q", got.DisplayName)
		}
		if got != want {
			t.Errorf("install component %q = %#v, want %#v", got.DisplayName, got, want)
		}
	}
}
