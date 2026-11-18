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
	"ruddervirt-setup/internal/status"
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

// resolveKubeOvnVersion/resolveMultusVersion/resolveAileronVersion mirror
// resolveK3sVersion for the other cluster-wide components, extracted from
// applyKubeOvnStep/prepareK3sStep below so clusterUpgradePending can ask
// "what would this step apply?" and get exactly the answer the step itself
// will use. Same reason planK3sInstall shares resolveK3sVersion.
func resolveKubeOvnVersion(cfg config.Config) string {
	if v := strings.TrimSpace(cfg.Versions.KubeOVN); v != "" {
		return v
	}
	return versionspkg.DefaultKubeOVNVersion
}

func resolveMultusVersion(cfg config.Config) string {
	if v := strings.TrimSpace(cfg.Versions.Multus); v != "" {
		return v
	}
	return versionspkg.DefaultMultusVersion
}

func resolveAileronVersion(cfg config.Config) string {
	if v := strings.TrimSpace(cfg.Versions.Aileron); v != "" {
		return v
	}
	return versionspkg.DefaultAileronVersion
}

// desiredClusterVersion returns the version this node would apply for one
// cluster-scoped config.SettingField key.
func desiredClusterVersion(cfg config.Config, key string) string {
	switch key {
	case "versions.kubeovn":
		return resolveKubeOvnVersion(cfg)
	case "versions.multus":
		return resolveMultusVersion(cfg)
	case "versions.kubevirt":
		return resolveKubeVirtVersion(cfg)
	case "versions.cdi":
		return resolveCDIVersion(cfg)
	case "versions.aileron":
		return resolveAileronVersion(cfg)
	}
	return ""
}

// clusterUpgradePending reports whether the live cluster runs a different
// version than this node would apply for any of keys, naming the first
// component that differs.
//
// This is what lets a cluster-wide upgrade be driven from any control-plane
// node while a plain join install still skips the cluster-wide steps: those
// steps are skipped on a joining node because the cluster already has what
// they would apply, not because of which node is running them. Once a
// component's desired version actually differs, the step has real work to do
// and runs - from whichever node the operator drove the upgrade on.
//
// Deliberately conservative about unknowns: a component the probe couldn't
// read (empty string - API unreachable, or k3s not up yet) is never counted
// as pending. On a joining node every one of these components is already
// installed by definition, so an empty answer means the probe failed, and
// re-applying the CNI or the KubeVirt CR to a live production cluster on the
// strength of a failed probe is exactly the wrong call. Positive evidence of
// a difference only.
func clusterUpgradePending(cfg config.Config, live map[string]string, keys ...string) (string, bool) {
	for _, key := range keys {
		running := live[key]
		if running == "" {
			continue
		}
		if desired := desiredClusterVersion(cfg, key); desired != "" && desired != running {
			return fmt.Sprintf("%s %s -> %s", strings.TrimPrefix(key, "versions."), running, desired), true
		}
	}
	return "", false
}

// skipWhenJoining announces and completes label's step when this node is
// joining an existing cluster AND the cluster already runs every version the
// step would apply, reporting true so the caller returns early.
//
// keys are the cluster-scoped components the step applies (see
// clusterUpgradePending). Re-applying them with nothing to change would at
// best be redundant and at worst restart the CNI or the KubeVirt operator on
// a live cluster for no reason; re-applying them when a version really does
// differ is the upgrade the operator asked for, and is legitimate from any
// control-plane node.
func skipWhenJoining(label string, keys []string, cfg config.Config, ch chan<- installsteps.StepMsg) bool {
	server := joiningCluster()
	if server == "" {
		return false
	}
	live := clusterComponentVersionsByFieldKey(status.FetchClusterComponentVersions(kubectlBinPath))
	if pending, ok := clusterUpgradePending(cfg, live, keys...); ok {
		ch <- stepOutputMsg(fmt.Sprintf(
			"Joining the existing cluster at %s, but %s is a cluster-wide upgrade - applying it from this node", server, pending))
		return false
	}
	ch <- stepOutputMsg(fmt.Sprintf(
		"Joining the existing cluster at %s - the cluster already runs what %q applies, skipping", server, label))
	ch <- stepDoneMsg{Label: label}
	return true
}

// planJoinSkip returns the plan line for a cluster-wide step on a joining
// node, and whether it has one. True covers all three join cases - the step
// will be skipped, it will run to apply a cluster-wide upgrade, or the k3s
// config can't be read at all (nothing about this node's role can be
// previewed until that's fixed). False means this node owns its own cluster,
// so the caller previews the step normally.
//
// Reads with plain os.ReadFile and a non-interactive cluster probe, never a
// privileged command - same Step.Plan contract as planConfigReadProblem.
func planJoinSkip(cfg config.Config, keys ...string) (string, bool) {
	if line, ok := planConfigReadProblem(); ok {
		return line, true
	}
	server := k3s.NodeMembership(os.ReadFile).JoinServer
	if server == "" {
		return "", false
	}
	live := clusterComponentVersionsByFieldKey(status.FetchClusterComponentVersions(kubectlBinPath))
	if pending, ok := clusterUpgradePending(cfg, live, keys...); ok {
		return "will run - cluster-wide upgrade (" + pending + ") applied from this node", true
	}
	return "skip - joining the existing cluster at " + server + ", which already runs these versions", true
}

// planApplyKubeOvn deliberately never touches a live cluster, same
// reasoning as planApplyManifests below.
func planApplyKubeOvn(cfg config.Config) string {
	if line, ok := planConfigReadProblem(); ok {
		return line
	}
	if server := k3s.NodeMembership(os.ReadFile).JoinServer; server != "" {
		return "will run - labels this node kube-ovn/role=master and adds it to ovn-central (cluster at " + server + ")"
	}
	return "will run - applies kube-ovn CNI and waits for it to become healthy"
}

// prepareK3sStepKeys are the cluster-scoped components "Applying manifests"
// applies, for skipWhenJoining/planJoinSkip's "is there actually an upgrade
// here?" check. The storage engine isn't among them: it has no version of
// its own, and switching engines is already blocked outright on a node with
// a prepared disk (storage.engine's Locked) and refused against a mismatched
// cluster (prepareStorageStep, storage.go).
//
// There is deliberately no equivalent for "Applying kube-ovn": that step is
// never skipped on a joining node - see applyKubeOvnStep.
var prepareK3sStepKeys = []string{"versions.multus", "versions.kubevirt", "versions.cdi", "versions.aileron"}

// applyKubeOvnStep runs on EVERY node, joining or not - the one cluster-wide
// step with no join skip.
//
// It is the only thing that makes a new node part of kube-ovn's control
// plane, and it has two node-local effects that nothing else performs:
// it labels this node kube-ovn/role=master (k3s applies a config file's
// node-label only at a node's first registration, so a node that joined
// without it never gets it any other way), and it re-renders the chart's
// masterNodes value from the live control-plane node list, which is what
// drives ovn-central's replica count and NODE_IPS raft addresses.
//
// Skipping it was wrong in the worst direction: the joining node would end
// up labeled master by the first node's next apply while absent from
// NODE_IPS, making it eligible to host an ovn-central replica that is not a
// raft member.
//
// Re-applying is self-limiting rather than disruptive. The rendered
// HelmChart is byte-identical when neither the version nor the node set
// changed, so kubectl apply reports "unchanged", the resource's generation
// doesn't move, and k3s's helm-controller starts no job. The OVN rollout
// happens exactly when the OVN membership really did change - which is
// precisely when it has to.
func applyKubeOvnStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Applying kube-ovn"
	if server := joiningCluster(); server != "" {
		ch <- stepOutputMsg(fmt.Sprintf(
			"Joining the existing cluster at %s - applying kube-ovn so this node is labeled kube-ovn/role=master and joins ovn-central's raft", server))
	}
	kubeOvnVersion := resolveKubeOvnVersion(cfg)
	err := k3s.ApplyKubeOvnStep(ch, wrapStepOutput, config.WritePrivileged, cfg.Network.PodCIDR, cfg.Network.SvcCIDR, kubeOvnVersion)
	ch <- stepDoneMsg{Label: label, Err: err}
}

func prepareK3sStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Applying manifests"
	if skipWhenJoining(label, prepareK3sStepKeys, cfg, ch) {
		return
	}

	aileronVersion := resolveAileronVersion(cfg)
	multusVersion := resolveMultusVersion(cfg)
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
	if line, ok := planJoinSkip(cfg, prepareK3sStepKeys...); ok {
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
