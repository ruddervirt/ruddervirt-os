// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/aileron"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
)

// aileronUIURL adapts internal/aileron.AileronUIURL's Config-free signature
// to package main's callers (view.go's home screen). Kept here since it's a
// thin config.Config-unwrapping helper, not domain logic.
func aileronUIURL(cfg config.Config) string {
	return aileron.AileronUIURL(cfg.System.AileronUIEnabled, cfg.Network)
}

// applyAileron adapts internal/aileron.ApplyAileron to the fixed
// func(ch chan<- installsteps.StepMsg, kubectlBin, version string, uiEnabled bool) error
// shape k3s_bridge.go's prepareK3sStep passes as PrepareK3sStep's
// applyAileronFn parameter.
func applyAileron(ch chan<- installsteps.StepMsg, kubectlBin, version string, uiEnabled bool) error {
	return aileron.ApplyAileron(ch, wrapStepOutput, config.WritePrivileged, kubectlBin, version, uiEnabled)
}

// aileronVersionsFetchedMsg carries fetchAileronVersionsCmd's result back
// into Update - same reasoning as k3sVersionsFetchedMsg (k3s_bridge.go).
type aileronVersionsFetchedMsg struct {
	versions []string
}

func fetchAileronVersionsCmd() tea.Cmd {
	return func() tea.Msg {
		versions, _ := aileron.FetchAileronVersions() // best-effort - cycling just no-ops if this fails
		return aileronVersionsFetchedMsg{versions: versions}
	}
}

// stabilizerChartPresent adapts internal/aileron.StabilizerChartPresent to
// the plain func() bool shape k3s_bridge.go's prepareK3sStep and
// status_bridge.go's fetchServiceStatuses pass around as an injected
// parameter.
func stabilizerChartPresent() bool {
	return aileron.StabilizerChartPresent()
}

// stabilizerDetectedMsg carries detectStabilizerCmd's result back into
// Update - same reasoning as aileronVersionsFetchedMsg above.
type stabilizerDetectedMsg struct {
	present bool
}

// detectStabilizerCmd is fired once from Init(), same as
// fetchAileronVersionsCmd.
func detectStabilizerCmd() tea.Cmd {
	return func() tea.Msg {
		return stabilizerDetectedMsg{present: aileron.StabilizerChartPresent()}
	}
}
