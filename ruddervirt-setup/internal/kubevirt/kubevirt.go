// SPDX-License-Identifier: GPL-3.0-only

// Package kubevirt holds KubeVirt/CDI-specific logic: manifest download,
// marker tracking, and CRD-presence validation. Generic semver/version
// helpers live in internal/versions instead.
package kubevirt

import (
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/marker"
)

// kubevirtManifestMarkerPath/cdiManifestMarkerPath record which
// KubeVirt/CDI versions were last downloaded to /etc/ruddervirt/manifests/,
// so DownloadKubeVirtCDIManifestsStep can skip a redundant re-download.
const (
	kubevirtManifestMarkerPath = "/var/lib/ruddervirt/kubevirt-manifest.applied"
	cdiManifestMarkerPath      = "/var/lib/ruddervirt/cdi-manifest.applied"
)

// kubevirtCDIManifestFiles lists every file DownloadKubeVirtCDIManifestsStep
// writes into manifestDir - shared with the skip-check so a manually
// deleted file forces a re-download instead of trusting a stale marker.
var kubevirtCDIManifestFiles = []string{
	"kubevirt-operator.yaml",
	"kubevirt-cr.yaml",
	"cdi-operator.yaml",
	"cdi-cr.yaml",
}

// CDIInstallSpec describes the operator manifest that owns each required
// CRD and the custom resource that must only be applied after that CRD has
// reached the Established condition. Exported for internal/k3s's post-CNI
// orchestration, which ranges over CDIInstallSpecs to apply each in turn.
type CDIInstallSpec struct {
	DisplayName            string
	OperatorManifest       string
	CRDName                string
	CustomResourceManifest string
}

var CDIInstallSpecs = []CDIInstallSpec{
	{
		DisplayName:            "KubeVirt",
		OperatorManifest:       "kubevirt-operator.yaml",
		CRDName:                "kubevirts.kubevirt.io",
		CustomResourceManifest: "kubevirt-cr.yaml",
	},
	{
		DisplayName:            "CDI",
		OperatorManifest:       "cdi-operator.yaml",
		CRDName:                "cdis.cdi.kubevirt.io",
		CustomResourceManifest: "cdi-cr.yaml",
	},
}

func appliedKubeVirtManifestVersion() (string, error) {
	return marker.Read(kubevirtManifestMarkerPath)
}

func appliedCDIManifestVersion() (string, error) {
	return marker.Read(cdiManifestMarkerPath)
}

// markKubeVirtManifestApplied/markCDIManifestApplied write via write, the
// caller's privileged file-write primitive (internal/config.WritePrivileged).
func markKubeVirtManifestApplied(write func(path string, data []byte) error, version string) error {
	return marker.Write(write, kubevirtManifestMarkerPath, version)
}

func markCDIManifestApplied(write func(path string, data []byte) error, version string) error {
	return marker.Write(write, cdiManifestMarkerPath, version)
}

// kubevirtCDIManifestsPresent reports whether every file in
// kubevirtCDIManifestFiles still exists under manifestDir.
func kubevirtCDIManifestsPresent(manifestDir string) bool {
	for _, name := range kubevirtCDIManifestFiles {
		if _, err := os.Stat(manifestDir + name); err != nil {
			return false
		}
	}
	return true
}

// manifestDefinesCRD verifies that an upstream operator manifest still embeds
// the CRD the TUI relies on it to install. Release asset layouts can change;
// rejecting an unexpected manifest here is safer than marking the download as
// complete and failing later when its custom resource is applied.
func manifestDefinesCRD(path, crdName string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	for {
		var document struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		if err := decoder.Decode(&document); err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
		if document.Kind == "CustomResourceDefinition" && document.Metadata.Name == crdName {
			return true, nil
		}
	}
}

// kubevirtCDIManifestsSatisfied is the check shared by
// DownloadKubeVirtCDIManifestsStep's skip-logic and PlanKubeVirtCDIDownload:
// the applied-version markers must match the desired versions and the
// manifest files must still be present.
func kubevirtCDIManifestsSatisfied(manifestDir, kubevirtVersion, cdiVersion string) bool {
	kv, err := appliedKubeVirtManifestVersion()
	if err != nil || kv != kubevirtVersion {
		return false
	}
	cdi, err := appliedCDIManifestVersion()
	if err != nil || cdi != cdiVersion {
		return false
	}
	return kubevirtCDIManifestsPresent(manifestDir)
}

// PlanKubeVirtCDIDownload previews DownloadKubeVirtCDIManifestsStep.
// kubevirtVersion/cdiVersion are already resolved (configured-or-default).
func PlanKubeVirtCDIDownload(kubevirtVersion, cdiVersion string) string {
	const manifestDir = "/etc/ruddervirt/manifests/"
	if kubevirtCDIManifestsSatisfied(manifestDir, kubevirtVersion, cdiVersion) {
		return fmt.Sprintf("skip - KubeVirt %s / CDI %s manifests already present", kubevirtVersion, cdiVersion)
	}
	return fmt.Sprintf("will download KubeVirt %s / CDI %s manifests", kubevirtVersion, cdiVersion)
}

// DownloadKubeVirtCDIManifestsStep re-downloads the upstream KubeVirt/CDI
// manifests for the versions configured in Settings, overwriting whatever
// is under /etc/ruddervirt/manifests/, unless kubevirtCDIManifestsSatisfied
// reports the desired versions are already present.
//
// kubevirtVersion/cdiVersion are already resolved (configured-or-default).
// ch/wrap/download/write are the caller's step-output channel and
// privileged download/write primitives (internal/config.WritePrivileged),
// since this package has no access to package main.
func DownloadKubeVirtCDIManifestsStep(
	kubevirtVersion, cdiVersion string,
	ch chan<- exec.StepMsg,
	wrap func(line string) exec.StepMsg,
	download func(url, destPath string, mode os.FileMode) error,
	write func(path string, data []byte) error,
) error {
	const manifestDir = "/etc/ruddervirt/manifests/"

	if kubevirtCDIManifestsSatisfied(manifestDir, kubevirtVersion, cdiVersion) {
		ch <- wrap(fmt.Sprintf("KubeVirt %s / CDI %s manifests already present", kubevirtVersion, cdiVersion))
		return nil
	}

	downloads := []struct{ url, destName string }{
		{
			fmt.Sprintf("https://github.com/kubevirt/kubevirt/releases/download/%s/kubevirt-operator.yaml", kubevirtVersion),
			"kubevirt-operator.yaml",
		},
		{
			fmt.Sprintf("https://github.com/kubevirt/kubevirt/releases/download/%s/kubevirt-cr.yaml", kubevirtVersion),
			"kubevirt-cr.yaml",
		},
		{
			fmt.Sprintf("https://github.com/kubevirt/containerized-data-importer/releases/download/%s/cdi-operator.yaml", cdiVersion),
			"cdi-operator.yaml",
		},
		{
			fmt.Sprintf("https://github.com/kubevirt/containerized-data-importer/releases/download/%s/cdi-cr.yaml", cdiVersion),
			"cdi-cr.yaml",
		},
	}

	for _, d := range downloads {
		ch <- wrap(fmt.Sprintf("Downloading %s...", d.destName))
		if err := download(d.url, manifestDir+d.destName, 0644); err != nil {
			return err
		}
	}

	for _, component := range CDIInstallSpecs {
		found, err := manifestDefinesCRD(manifestDir+component.OperatorManifest, component.CRDName)
		if err != nil {
			return fmt.Errorf("validating %s: %w", component.OperatorManifest, err)
		}
		if !found {
			return fmt.Errorf("%s does not define required CRD %s", component.OperatorManifest, component.CRDName)
		}
	}

	// Only mark applied once every file above actually landed - a mid-loop
	// failure must never mark a partial download as "applied".
	if err := markKubeVirtManifestApplied(write, kubevirtVersion); err != nil {
		return err
	}
	if err := markCDIManifestApplied(write, cdiVersion); err != nil {
		return err
	}

	ch <- wrap("KubeVirt/CDI manifests downloaded successfully")
	return nil
}
