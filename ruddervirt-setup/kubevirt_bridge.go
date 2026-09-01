// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"strings"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/k3s"
	"ruddervirt-setup/internal/kubevirt"
	"ruddervirt-setup/internal/versions"
)

// resolveKubeVirtVersion/resolveCDIVersion return cfg's configured versions,
// or the defaults if unset - shared by downloadKubeVirtCDIManifestsStep and
// planKubeVirtCDIDownload so the two can never disagree on "desired". Kept
// here (not internal/kubevirt) since downloadKubeVirtCDIManifestsStep below
// constructs package main's stepDoneMsg directly - same reasoning as
// k3s_bridge.go's resolveK3sVersion.
func resolveKubeVirtVersion(cfg config.Config) string {
	v := strings.TrimSpace(cfg.Versions.KubeVirt)
	if v == "" {
		return versions.DefaultKubeVirtVersion
	}
	return v
}

func resolveCDIVersion(cfg config.Config) string {
	v := strings.TrimSpace(cfg.Versions.CDI)
	if v == "" {
		return versions.DefaultCDIVersion
	}
	return v
}

// downloadKubeVirtCDIManifestsStep/planKubeVirtCDIDownload adapt
// internal/kubevirt's Config-free functions to the installsteps.Step{Run,
// Plan} shape (install_steps.go).
func downloadKubeVirtCDIManifestsStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Downloading KubeVirt/CDI manifests"
	kubevirtVersion := resolveKubeVirtVersion(cfg)
	cdiVersion := resolveCDIVersion(cfg)
	err := kubevirt.DownloadKubeVirtCDIManifestsStep(kubevirtVersion, cdiVersion, ch, wrapStepOutput, k3s.DownloadToPrivilegedPath, config.WritePrivileged)
	ch <- stepDoneMsg{Label: label, Err: err}
}

func planKubeVirtCDIDownload(cfg config.Config) string {
	kubevirtVersion := resolveKubeVirtVersion(cfg)
	cdiVersion := resolveCDIVersion(cfg)
	return kubevirt.PlanKubeVirtCDIDownload(kubevirtVersion, cdiVersion)
}
