// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/k3s"
	"ruddervirt-setup/internal/kubevirt"
	versionspkg "ruddervirt-setup/internal/versions"
)

// resolveK3sVersion returns cfg's configured k3s version, or
// versionspkg.DefaultK3sVersion if unset - shared by installK3sStep and
// planK3sInstall so the two can never disagree on "desired". Kept here (not
// internal/k3s) since installK3sStep below constructs package main's
// stepDoneMsg directly, and moving it would make internal/k3s import
// internal/installsteps just for that message - same reasoning as
// storage.go and kubevirt_bridge.go's resolve*Version funcs.
func resolveK3sVersion(cfg config.Config) string {
	v := strings.TrimSpace(cfg.Versions.K3s)
	if v == "" {
		return versionspkg.DefaultK3sVersion
	}
	return v
}

// planK3sInstall previews installK3sStep: an exact match on the resolved
// version string, since in steady state the configured version is exactly
// what should already be on disk.
func planK3sInstall(cfg config.Config) string {
	version := resolveK3sVersion(cfg)
	if installed, ok := k3s.InstalledK3sVersion(); ok && installed == version {
		return fmt.Sprintf("skip - k3s %s already installed", version)
	}
	return fmt.Sprintf("will download k3s %s", version)
}

// installK3sStep/renderK3sConfigStep/applyKubeOvnStep/prepareK3sStep adapt
// internal/k3s's Config-free functions to the installsteps.Step{Run, Plan}
// shape (install_steps.go).
func installK3sStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Installing k3s"
	version := resolveK3sVersion(cfg)
	err := k3s.InstallK3sStep(version, ch, wrapStepOutput)
	ch <- stepDoneMsg{Label: label, Err: err}
}

func renderK3sConfigStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Rendering k3s config"
	err := k3s.RenderK3sConfigStep(cfg.Network, ch, wrapStepOutput, config.WritePrivileged)
	ch <- stepDoneMsg{Label: label, Err: err}
}

// planApplyKubeOvn deliberately never touches a live cluster, same
// reasoning as planApplyManifests below.
func planApplyKubeOvn(cfg config.Config) string {
	return "will run - applies kube-ovn CNI and waits for it to become healthy"
}

func applyKubeOvnStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Applying kube-ovn"
	kubeOvnVersion := strings.TrimSpace(cfg.Versions.KubeOVN)
	if kubeOvnVersion == "" {
		kubeOvnVersion = versionspkg.DefaultKubeOVNVersion
	}
	err := k3s.ApplyKubeOvnStep(ch, wrapStepOutput, config.WritePrivileged, cfg.Network.PodCIDR, cfg.Network.SvcCIDR, kubeOvnVersion)
	ch <- stepDoneMsg{Label: label, Err: err}
}

func prepareK3sStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Applying manifests"

	aileronVersion := strings.TrimSpace(cfg.Versions.Aileron)
	if aileronVersion == "" {
		aileronVersion = versionspkg.DefaultAileronVersion
	}
	multusVersion := strings.TrimSpace(cfg.Versions.Multus)
	if multusVersion == "" {
		multusVersion = versionspkg.DefaultMultusVersion
	}
	kubevirtVersion := resolveKubeVirtVersion(cfg)
	cdiVersion := resolveCDIVersion(cfg)

	err := k3s.PrepareK3sStep(
		ch, wrapStepOutput, config.WritePrivileged,
		cfg.Storage.Engine, cfg.System.AileronUIEnabled, aileronVersion, multusVersion,
		kubevirtVersion, cdiVersion,
		stabilizerChartPresent, applyAileron,
	)
	ch <- stepDoneMsg{Label: label, Err: err}
}

// planApplyManifests deliberately never touches a live cluster for most of
// what this step does - there may be no k3s API to query yet (e.g. the first
// install, before k3s has started), and kubectl apply/wait are naturally
// idempotent (already-applied/already-Ready is a fast no-op) for the
// storage/kube-ovn/multus/Aileron parts. The KubeVirt/CDI cluster-apply skip
// decision, unlike those, is predictable from local markers alone (same as
// planKubeVirtCDIDownload), so it's called out explicitly here.
func planApplyManifests(cfg config.Config) string {
	kubevirtVersion := resolveKubeVirtVersion(cfg)
	cdiVersion := resolveCDIVersion(cfg)
	kvNote := "will apply"
	if kubevirt.KubeVirtClusterApplySatisfied(kubevirtVersion) {
		kvNote = "already applied - skip"
	}
	cdiNote := "will apply"
	if kubevirt.CDIClusterApplySatisfied(cdiVersion) {
		cdiNote = "already applied - skip"
	}
	return fmt.Sprintf(
		"will run - applies storage; KubeVirt %s (%s) and CDI %s (%s); Aileron unless a \"stabilizer\" HelmChart already manages it",
		kubevirtVersion, kvNote, cdiVersion, cdiNote,
	)
}

// k3sVersionsFetchedMsg carries fetchK3sVersions' result back into Update -
// run as a tea.Cmd (not synchronously in initialModel) so a slow or
// unreachable network doesn't delay the TUI's first paint.
type k3sVersionsFetchedMsg struct {
	versions []string
}

func fetchK3sVersionsCmd() tea.Cmd {
	return func() tea.Msg {
		versions, _ := k3s.FetchK3sVersions() // best-effort - cycling just no-ops if this fails
		return k3sVersionsFetchedMsg{versions: versions}
	}
}
