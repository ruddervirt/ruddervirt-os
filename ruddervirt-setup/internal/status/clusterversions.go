// SPDX-License-Identifier: GPL-3.0-only

package status

import (
	"context"
	"strings"

	"ruddervirt-setup/internal/exec"
)

// ClusterComponentVersions reports the version each cluster-wide component
// actually runs, read from the cluster itself rather than from this node's
// ruddervirt-setup.yaml.
//
// This matters because every component here is cluster-scoped while
// ruddervirt-setup.yaml is per-node, so the two drift the moment a cluster
// has more than one node:
//
//   - a node that joined an existing cluster has whatever versions its
//     installer image shipped as defaults, which have nothing to do with
//     what the cluster runs - rendering those as the current versions is
//     simply wrong;
//   - after an upgrade performed from any one node, every *other* node's
//     config is stale, and would re-apply its own older version - a
//     downgrade - on its next Apply.
//
// Reading the versions back from the cluster is what makes a cluster-wide
// upgrade safe to drive from any node: the cluster, not a node's config
// file, is the source of truth, and each node picks the change up the next
// time it looks (see versions.NewerVersion for the reconciliation rule).
//
// An empty field means "could not determine" - the component isn't
// installed yet, the API was unreachable, or its version isn't in the shape
// we expect - and every caller must treat that as "no information" rather
// than as a version, falling back to this node's configured value.
type ClusterComponentVersions struct {
	// Reachable is false when k3s isn't running on this node, so nothing
	// below was even attempted. Distinguishes "the cluster says these
	// components aren't installed" from "we never got to ask".
	Reachable bool

	KubeOVN  string
	Multus   string
	KubeVirt string
	CDI      string
	Aileron  string
}

// clusterVersionTag runs a bounded, non-interactive `kubectl get ... -o
// jsonpath=...` and normalizes the result into the leading-"v" tag shape
// ruddervirt-setup's config and version pickers use everywhere.
//
// The normalization is load-bearing, not cosmetic: the three HelmChart
// components store the tag differently. kube-ovn and multus get the
// configured value substituted verbatim (so "v1.15.32"), while Aileron's
// chart version is deliberately stripped of its "v" because Helm chart
// versions must be bare semver (see aileron.ApplyAileron). Reading either
// shape back and re-adding the "v" when it's missing gives one comparable
// form, so a version read from the cluster can be compared against a
// configured one with versions.CompareSemver.
func clusterVersionTag(kubectlBin string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), StatusCheckTimeout)
	defer cancel()
	out, err := exec.RunNonInteractive(ctx, kubectlBin, args...).Output()
	if err != nil {
		return ""
	}
	return normalizeVersionTag(string(out))
}

// normalizeVersionTag trims a jsonpath result and re-adds the leading "v"
// when it's missing, so a version read off the cluster is directly
// comparable with a configured one. Split out from clusterVersionTag to be
// testable without a cluster.
func normalizeVersionTag(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// FetchClusterComponentVersions probes the live cluster for every
// cluster-wide component's running version. Synchronous and bounded by
// StatusCheckTimeout per probe, in the same spirit as FetchServiceStatuses -
// callers run it as a background tea.Cmd, never on the render path.
//
// Gated on K3sServiceActive for the reason that function documents: before
// k3s is installed the kubectl wrapper execs a shebang-less placeholder and
// POSIX shell's ENOEXEC fallback makes every `kubectl get` exit 0 with empty
// output. Without this gate a fresh node would read every component as
// "" - harmless here, since "" means "unknown" - but the same false success
// is a real hazard for callers that branch on Reachable, so the gate stays
// where every other kubectl probe in this package has it.
func FetchClusterComponentVersions(kubectlBin string) ClusterComponentVersions {
	if !K3sServiceActive() || !HaveNonInteractiveSudo() {
		return ClusterComponentVersions{}
	}

	v := ClusterComponentVersions{Reachable: true}

	// The three k3s HelmChart CRs (helm.cattle.io/v1) carry the version the
	// chart was asked to install in spec.version. That's "declared", not
	// "rolled out" - if a helm-install Job is mid-flight or failed, this is
	// ahead of reality. Deliberate: it's the value ruddervirt-setup itself
	// wrote and would otherwise re-apply, so it's the right thing to
	// reconcile a node's config against. (The Aileron row has a true
	// applied-vs-declared display for the stabilizer-managed case already -
	// see UpdateModel.ViewVersions.)
	v.KubeOVN = clusterVersionTag(kubectlBin, "-n", "kube-system", "get", "helmchart.helm.cattle.io", "kube-ovn",
		"-o", "jsonpath={.spec.version}")
	v.Multus = clusterVersionTag(kubectlBin, "-n", "kube-system", "get", "helmchart.helm.cattle.io", "multus",
		"-o", "jsonpath={.spec.version}")
	v.Aileron = clusterVersionTag(kubectlBin, "-n", "kube-system", "get", "helmchart.helm.cattle.io", "aileron",
		"-o", "jsonpath={.spec.version}")

	// KubeVirt and CDI are operator-reconciled, so their own status reports
	// what is actually running, which is strictly better than a declared
	// value. status.observedKubeVirtVersion/observedVersion only advance once
	// the operator has finished rolling the new version out; during an
	// upgrade they still read the old version, which is the honest answer
	// and keeps a half-finished upgrade from being recorded as done.
	//
	// Resource names match the readiness probes in status.go: the KubeVirt CR
	// is namespaced (kubevirt/kubevirt), the CDI CR is cluster-scoped (cdi).
	v.KubeVirt = clusterVersionTag(kubectlBin, "-n", "kubevirt", "get", "kubevirt", "kubevirt",
		"-o", "jsonpath={.status.observedKubeVirtVersion}")
	v.CDI = clusterVersionTag(kubectlBin, "get", "cdi", "cdi",
		"-o", "jsonpath={.status.observedVersion}")

	return v
}
