// SPDX-License-Identifier: GPL-3.0-only

package manifests

import (
	"os"
	"strings"

	"ruddervirt-setup/internal/exec"
)

// Placeholder is one __TOKEN__ -> value substitution for RenderAndApply.
type Placeholder struct {
	Token string
	Value string
}

// RenderAndApply is the "write the embedded manifest to disk, read it back,
// substitute placeholders, write the result to a tempfile, kubectl apply
// it" idiom shared, byte-identically, by internal/k3s's applyKubeOvn,
// internal/aileron's ApplyAileron, internal/stabilizer's applyStabilizer,
// and internal/multus's ApplyMultus.
//
// Each caller's own poll/wait phase (if any) is deliberately not part of
// this helper, since that differs across them: kube-ovn waits on live
// workload rollouts, not a Job; multus doesn't wait at all; aileron/
// stabilizer poll for a helm-install Job (see
// internal/k3s.WaitForHelmInstallJob, which can't live here since
// internal/k3s already imports this package).
//
// ch/wrap are the caller's tea.Msg channel/wrapper, write is the caller's
// privileged file-write primitive (internal/config.WritePrivileged) - this
// package has none of its own. manifestFile is the embedded manifest's
// relative path (passed through to WriteManifestFile). tempFilePrefix
// names the tempfile (os.CreateTemp(tempFilePrefix + "-*.yaml")) - kept as
// a parameter since it isn't automatically derivable from manifestFile in
// general.
func RenderAndApply(ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, write func(path string, data []byte) error, kubectlBin, manifestFile, tempFilePrefix string, placeholders []Placeholder) error {
	if err := WriteManifestFile(ch, wrap, write, manifestFile); err != nil {
		return err
	}
	templatePath := "/etc/ruddervirt/manifests/" + manifestFile
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return err
	}
	return renderSubstituteApply(ch, wrap, kubectlBin, tempFilePrefix, data, placeholders)
}

// renderSubstituteApply is RenderAndApply's substitute/tempfile/apply half,
// split out so it's directly unit-testable against fabricated template
// bytes - RenderAndApply's own template read comes from a fixed, root-owned
// OS path a non-privileged test process can't place a file at ahead of time.
func renderSubstituteApply(ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, kubectlBin, tempFilePrefix string, data []byte, placeholders []Placeholder) error {
	rendered := string(data)
	for _, p := range placeholders {
		rendered = strings.ReplaceAll(rendered, p.Token, p.Value)
	}

	tmp, err := os.CreateTemp("", tempFilePrefix+"-*.yaml")
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

	return exec.RunStreamed(ch, wrap, kubectlBin, "apply", "-f", tmpPath)
}
