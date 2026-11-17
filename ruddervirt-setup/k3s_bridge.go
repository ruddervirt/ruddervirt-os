// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
	execpkg "ruddervirt-setup/internal/exec"
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
	err := k3s.RenderK3sConfigStep(cfg.Network, ch, wrapStepOutput, execpkg.ReadFilePrivileged, config.WritePrivileged)
	ch <- stepDoneMsg{Label: label, Err: err}
}

// planRenderK3sConfig previews renderK3sConfigStep: the one case worth
// calling out is an operator-provided config the step won't touch, since
// "Rendering k3s config" otherwise reads as though it will.
func planRenderK3sConfig(cfg config.Config) string {
	if line, ok := planConfigReadProblem(); ok {
		return line
	}
	if owned, reason := k3s.ConfigIsOperatorOwned(os.ReadFile); owned {
		return fmt.Sprintf("skip - %s; leaving it as-is", reason)
	}
	return fmt.Sprintf("will render %s from %s", k3s.ClusterConfigPath, k3s.ConfigTemplatePath)
}

// planConfigReadProblem turns an unreadable/unparseable k3s config into a
// plan line, for the three previews whose answer depends on it. Without this
// they'd silently fall back to "new cluster" and the pipeline would then
// overwrite a config it simply couldn't read - most likely one an operator
// placed with sudo at mode 0600. Reads with plain os.ReadFile, never a
// privileged one: Step.Plan must never risk a sudo prompt fighting
// bubbletea's raw-terminal mode (see installsteps.Step's doc comment).
func planConfigReadProblem() (string, bool) {
	if _, err := k3s.ReadMembership(os.ReadFile, k3s.ClusterConfigPath); err != nil {
		return fmt.Sprintf(
			"cannot read %s (%v) - make it readable (mode 0644) so the installer can tell whether this node joins an existing cluster",
			k3s.ClusterConfigPath, err,
		), true
	}
	return "", false
}

// joiningCluster reports the cluster this node is configured to join, from
// whichever config the next k3s start will use (see k3s.NodeMembership) -
// empty when it bootstraps its own cluster, i.e. today's single-node default.
func joiningCluster() string {
	return k3s.NodeMembership(execpkg.ReadFilePrivileged).JoinServer
}

// skipWhenJoining announces and completes label's step when this node is
// joining an existing cluster, reporting true so the caller returns early.
// The steps that use it apply cluster-singleton resources that the cluster's
// first node already owns - re-applying them from a joining node would at
// best be redundant and at worst fight the existing cluster over resource
// ownership and versions.
func skipWhenJoining(label string, ch chan<- installsteps.StepMsg) bool {
	server := joiningCluster()
	if server == "" {
		return false
	}
	ch <- stepOutputMsg(fmt.Sprintf(
		"Joining the existing cluster at %s - %q belongs to the cluster's first node, skipping", server, label))
	ch <- stepDoneMsg{Label: label}
	return true
}

// planJoinSkip returns the plan line for a cluster-singleton step, and
// whether it applies - true on a joining node (the step is skipped) or when
// the k3s config can't be read at all (nothing about this node's role can be
// previewed until that's fixed). False means preview the step normally.
func planJoinSkip() (string, bool) {
	if line, ok := planConfigReadProblem(); ok {
		return line, true
	}
	if server := k3s.NodeMembership(os.ReadFile).JoinServer; server != "" {
		return "skip - joining the existing cluster at " + server, true
	}
	return "", false
}

// planApplyKubeOvn deliberately never touches a live cluster, same
// reasoning as planApplyManifests below.
func planApplyKubeOvn(cfg config.Config) string {
	if line, ok := planJoinSkip(); ok {
		return line
	}
	return "will run - applies kube-ovn CNI and waits for it to become healthy"
}

func applyKubeOvnStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Applying kube-ovn"
	if skipWhenJoining(label, ch) {
		return
	}
	kubeOvnVersion := strings.TrimSpace(cfg.Versions.KubeOVN)
	if kubeOvnVersion == "" {
		kubeOvnVersion = versionspkg.DefaultKubeOVNVersion
	}
	err := k3s.ApplyKubeOvnStep(ch, wrapStepOutput, config.WritePrivileged, cfg.Network.PodCIDR, cfg.Network.SvcCIDR, kubeOvnVersion)
	ch <- stepDoneMsg{Label: label, Err: err}
}

func prepareK3sStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Applying manifests"
	if skipWhenJoining(label, ch) {
		return
	}

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
	if line, ok := planJoinSkip(); ok {
		return line
	}
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
