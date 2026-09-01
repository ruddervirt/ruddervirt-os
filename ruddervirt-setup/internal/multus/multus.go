// SPDX-License-Identifier: GPL-3.0-only

package multus

import (
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/manifests"
)

// ApplyMultus renders manifests/multus.yaml's __MULTUS_VERSION__ placeholder
// (offered on the Update screen from a hand-curated allowlist,
// supported-versions.yaml, not a live fetch - rke2-multus's chart version
// scheme doesn't track multus-cni's upstream GitHub releases) and applies
// the resulting HelmChart object. Previously this manifest was applied
// unversioned, leaving k3s's helm-controller to silently resolve "latest" -
// never reproducible across two installs run days apart. wrap/write are
// the caller's tea.Msg wrapper and privileged file-write primitive - this
// package has neither of its own.
func ApplyMultus(ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, write func(path string, data []byte) error, kubectlBin, version string) error {
	return manifests.RenderAndApply(ch, wrap, write, kubectlBin, "multus.yaml", "multus", []manifests.Placeholder{
		{Token: "__MULTUS_VERSION__", Value: version},
	})
}
