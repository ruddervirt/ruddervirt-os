// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/installsteps"
)

// stabilizerVersionApplySteps builds the two-step installsteps.Step pipeline
// that applies an already-validated guarded chart-version patch (see
// stabilizer.PlanStabilizerVersionUpgrade, internal/stabilizer/upgrade.go)
// and watches the rollout - same shape as stabilizerSettingsApplySteps
// (stabilizer_settings_tui.go). Step 1 differs (an already-marshalled
// version+pins patch vs. one setting's value); step 2 is shared verbatim via
// waitForStabilizerRolloutStep.
func stabilizerVersionApplySteps(helmChartNamespace, helmChartName string, patchJSON []byte, targetVersion string) []installsteps.Step {
	return []installsteps.Step{
		{
			Label: fmt.Sprintf("Patching to %s", targetVersion),
			Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
				label := fmt.Sprintf("Patching to %s", targetVersion)
				ch <- stepOutputMsg(fmt.Sprintf("Patching %s/%s...", helmChartNamespace, helmChartName))
				err := exec.RunStreamed(ch, wrapStepOutput, kubectlBinPath, "-n", helmChartNamespace, "patch", "helmchart.helm.cattle.io", helmChartName,
					"--type=merge", "-p", string(patchJSON))
				ch <- stepDoneMsg{Label: label, Err: err}
			},
		},
		waitForStabilizerRolloutStep(helmChartNamespace, helmChartName),
	}
}
