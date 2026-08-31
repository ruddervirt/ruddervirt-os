// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// applyMultus renders manifests/multus.yaml's __MULTUS_VERSION__ placeholder
// (offered on the Update screen from a hand-curated allowlist,
// supported-versions.yaml, same as KubeVirt/CDI/kube-ovn, not a live fetch -
// rke2-multus's own chart version scheme doesn't track multus-cni's
// upstream GitHub releases at all) and applies the resulting HelmChart
// object - same read-template/substitute/write-tempfile/apply shape as
// applyKubeOvn/applyAileron. Split out of prepareK3sStep (k3s.go), which
// used to apply this manifest unversioned/unsubstituted (spec.version
// omitted entirely, leaving k3s's helm-controller to silently resolve
// "latest" - never reproducible across two installs run days apart), so the
// multus CRD apply immediately before it in prepareK3sStep is untouched.
func applyMultus(ch chan<- tea.Msg, kubectlBin, version string) error {
	const templatePath = "/etc/ruddervirt/manifests/multus.yaml"
	if err := writeManifestFile(ch, "multus.yaml"); err != nil {
		return err
	}
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return err
	}
	rendered := strings.ReplaceAll(string(data), "__MULTUS_VERSION__", version)

	tmp, err := os.CreateTemp("", "multus-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(rendered); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return runStreamed(ch, kubectlBin, "apply", "-f", tmpPath)
}
