// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// stabilizerVersionApplySteps builds the two-step installStep pipeline that
// applies an already-validated guarded chart-version patch (see
// stabilizer_upgrade.go's planStabilizerVersionUpgrade) and waits for the
// rollout - same shape and same "can't watch a rollout from inside a TUI"
// reasoning as stabilizerSettingsApplySteps (stabilizer_settings_tui.go),
// kept as its own small function rather than sharing one because the two
// callers build their patch bodies completely differently (one setting's
// value vs. an already-marshalled version+pins patch).
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
		{
			label: "Waiting for the rollout to complete",
			run: func(cfg Config, ch chan<- tea.Msg) {
				const label = "Waiting for the rollout to complete"
				jobName := "job/helm-install-" + helmChartName
				if err := pollUntil(ch, "Waiting for the stabilizer helm-install job", 60, 5*time.Second, func() bool {
					return runPrivileged(kubectlBinPath, "-n", helmChartNamespace, "get", jobName).Run() == nil
				}); err != nil {
					ch <- stepDoneMsg{label: label, err: err}
					return
				}
				if err := runStreamed(ch, kubectlBinPath, "-n", helmChartNamespace, "wait", "--for=condition=complete", jobName, "--timeout=600s"); err != nil {
					ch <- stepDoneMsg{label: label, err: err}
					return
				}
				ch <- stepOutputMsg("Waiting for stabilizer to become ready again...")
				err := runStreamed(ch, kubectlBinPath, "-n", stabilizerNamespace, "wait",
					"--for=condition=Available", "deployment.apps/stabilizer", "--timeout=600s")
				ch <- stepDoneMsg{label: label, err: err}
			},
		},
	}
}
