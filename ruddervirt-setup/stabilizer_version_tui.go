// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// stabilizerVersionApplySteps builds the two-step installStep pipeline that
// applies an already-validated guarded chart-version patch (see
// stabilizer_upgrade.go's planStabilizerVersionUpgrade) and watches for the
// rollout - same shape and same "can't watch a rollout from inside a TUI"
// reasoning as stabilizerSettingsApplySteps (stabilizer_settings_tui.go).
// Its own step 1 (patch body construction differs - one setting's value vs.
// an already-marshalled version+pins patch), but step 2 is shared verbatim
// via waitForStabilizerRolloutStep, defined alongside stabilizerSettingsApplySteps.
func stabilizerVersionApplySteps(helmChartNamespace, helmChartName string, patchJSON []byte, targetVersion string) []installStep {
	return []installStep{
		{
			label: fmt.Sprintf("Patching to %s", targetVersion),
			run: func(cfg Config, ch chan<- tea.Msg) {
				label := fmt.Sprintf("Patching to %s", targetVersion)
				ch <- stepOutputMsg(fmt.Sprintf("Patching %s/%s...", helmChartNamespace, helmChartName))
				err := runStreamed(ch, kubectlBinPath, "-n", helmChartNamespace, "patch", "helmchart.helm.cattle.io", helmChartName,
					"--type=merge", "-p", string(patchJSON))
				ch <- stepDoneMsg{label: label, err: err}
			},
		},
		waitForStabilizerRolloutStep(helmChartNamespace, helmChartName),
	}
}
