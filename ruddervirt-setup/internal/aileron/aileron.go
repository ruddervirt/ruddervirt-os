// SPDX-License-Identifier: GPL-3.0-only

// Package aileron holds Aileron-specific install/status logic. It has no
// access to package main, so ApplyAileron takes ch/wrap/write as explicit
// parameters and StabilizerChartPresent reaches for internal/status
// directly - see package main's aileron_bridge.go for the Config-narrowing
// adapters matching the installStep{run, plan} shape.
package aileron

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/k3s"
	"ruddervirt-setup/internal/manifests"
	"ruddervirt-setup/internal/network"
	"ruddervirt-setup/internal/status"
	"ruddervirt-setup/internal/versions"
)

// aileronUINodePort is the NodePort aileronUI.service.nodePort defaults to
// in ghcr.io/ruddervirt/charts/aileron's values.yaml; ruddervirt-setup
// exposes no override for it, so it's safe to hardcode. Matches
// AILERON_UI_PORT in the Makefile, used the same way for `make boot`.
const aileronUINodePort = "30806"

// AileronUIURL returns the URL to reach the Aileron UI at, for the home
// screen's display - empty if the UI is disabled or this node's address
// can't currently be resolved.
func AileronUIURL(uiEnabled bool, netCfg network.NetworkConfig) string {
	if !uiEnabled {
		return ""
	}
	ip, err := network.ResolveLocalIP(netCfg)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("http://%s:%s", ip, aileronUINodePort)
}

// FetchAileronVersions lists ruddervirt/aileron release tags from GitHub,
// newest first, for the Settings screen's version picker - using the plain
// vMAJOR.MINOR.PATCH comparator (versions.CompareSemver) since Aileron's
// tags don't carry a k3s-style +BUILD suffix.
func FetchAileronVersions() ([]string, error) {
	releases, err := versions.FetchGitHubReleases("ruddervirt/aileron", "aileron releases")
	if err != nil {
		return nil, err
	}

	var tags []string
	for _, r := range releases {
		if r.Draft || r.Prerelease || r.TagName == "" {
			continue
		}
		if _, ok := versions.ParseSemver(r.TagName); !ok {
			continue
		}
		tags = append(tags, r.TagName)
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("no aileron releases found")
	}

	sort.SliceStable(tags, func(i, j int) bool {
		cmp, ok := versions.CompareSemver(tags[i], tags[j])
		return ok && cmp > 0
	})

	return tags, nil
}

// ApplyAileron renders manifests/aileron-helmchart.yaml's
// __AILERON_VERSION__ placeholder (the release tag stripped of its leading
// "v", since Helm chart versions must be bare semver) and applies the
// resulting HelmChart object, then waits for the controller's
// asynchronously-created install Job to appear and complete. k3s's
// always-on helm-controller does the actual chart install as a Job inside
// the cluster; no helm binary is ever invoked here.
func ApplyAileron(ch chan<- exec.StepMsg, wrap func(string) exec.StepMsg, write func(path string, data []byte) error, kubectlBin, version string, uiEnabled bool) error {
	const jobNamespace = "kube-system"
	const jobName = "job/helm-install-aileron"

	chartVersion := strings.TrimPrefix(version, "v")

	if err := manifests.RenderAndApply(ch, wrap, write, kubectlBin, "aileron-helmchart.yaml", "aileron-helmchart", []manifests.Placeholder{
		{Token: "__AILERON_VERSION__", Value: chartVersion},
		{Token: "__AILERON_UI_ENABLED__", Value: strconv.FormatBool(uiEnabled)},
	}); err != nil {
		return err
	}

	// The controller creates the install Job asynchronously - wait for it
	// to exist, then for it to finish.
	return k3s.WaitForHelmInstallJob(ch, wrap, kubectlBin, jobNamespace, jobName, "aileron")
}

// stabilizerHelmChartName is the HelmChart object name that signals Aileron
// is owned by a different chart than the one ruddervirt-setup applies
// itself (manifests/aileron-helmchart.yaml's HelmChart is named "aileron").
const stabilizerHelmChartName = "stabilizer"

// StabilizerChartPresent reports whether a HelmChart object named
// stabilizerHelmChartName exists anywhere on the cluster, via a bounded,
// non-interactive kubectl call (never risk a password prompt fighting
// bubbletea's raw-terminal mode). `kubectl get --field-selector` exits 0
// even when nothing matches, so this checks whether anything was returned.
func StabilizerChartPresent() bool {
	if !status.HaveNonInteractiveSudo() {
		return false
	}
	const kubectlBin = "/usr/local/bin/kubectl"
	ctx, cancel := context.WithTimeout(context.Background(), status.StatusCheckTimeout)
	defer cancel()
	out, err := exec.RunNonInteractive(ctx, kubectlBin, "get", "helmchart", "--all-namespaces",
		"--field-selector", "metadata.name="+stabilizerHelmChartName, "-o", "name").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}
