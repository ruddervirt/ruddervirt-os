// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/stabilizer"
	"ruddervirt-setup/internal/status"
)

// nebulaConfigResolvedMsg carries resolveNebulaConfigCmd's result back into
// Update - fetching/reading and validating the Nebula config happens off
// the UI thread since internal/stabilizer.ResolveNebulaConfig can block on
// a network round trip.
type nebulaConfigResolvedMsg struct {
	content string
	err     error
}

// resolveNebulaConfigCmd runs internal/stabilizer.ResolveNebulaConfig,
// unwraps a whole Secret manifest to its plain Nebula config if that's what
// was fetched (ExtractNebulaConfig), then ValidateNebulaConfig - as a single
// tea.Cmd. screenStabilizerNebula fires this on Enter and reacts to
// nebulaConfigResolvedMsg once it lands.
func resolveNebulaConfigCmd(pathOrURL string) tea.Cmd {
	return func() tea.Msg {
		raw, err := stabilizer.ResolveNebulaConfig(pathOrURL)
		if err != nil {
			return nebulaConfigResolvedMsg{err: err}
		}
		content, err := stabilizer.ExtractNebulaConfig(raw)
		if err != nil {
			return nebulaConfigResolvedMsg{err: fmt.Errorf("%s: %w", pathOrURL, err)}
		}
		if err := stabilizer.ValidateNebulaConfig(content); err != nil {
			return nebulaConfigResolvedMsg{err: fmt.Errorf("%s doesn't look like a valid nebula config: %w", pathOrURL, err)}
		}
		return nebulaConfigResolvedMsg{content: content}
	}
}

// aileronReadyCheckMsg carries checkAileronReadyCmd's result back into
// Update - gates entry into the "Adopt to ruddervirt.com" wizard.
type aileronReadyCheckMsg struct {
	ready bool
}

// checkAileronReadyCmd reports (read-only, off the UI thread) whether
// Aileron is actually installed and running - reuses status.AileronReady,
// the same live "deployment.apps/aileron Available" check the home screen's
// Services row relies on, so this stays correct whether Aileron is
// standalone or already stabilizer-managed. Adopting against an Aileron
// that's missing or unhealthy would re-stamp ownership of resources that
// aren't really there - this blocks the wizard before it asks for secrets,
// not just before the final write (see adoptAileronStep's own re-check,
// internal/stabilizer/adopt.go, for defense-in-depth).
//
// status.K3sServiceActive() is checked FIRST: before k3s is installed,
// kubectl (which execs through /usr/local/bin/k3s) silently reports success
// doing nothing (see K3sServiceActive's doc comment), which would otherwise
// make this falsely report "ready" on a node k3s was never installed on.
func checkAileronReadyCmd(kubectlBin string) tea.Cmd {
	return func() tea.Msg {
		return aileronReadyCheckMsg{ready: status.K3sServiceActive() && status.AileronReady(kubectlBin)}
	}
}

// stabilizerPlanMsg carries computeStabilizerPlanCmd's result back into
// Update - feeds screenStabilizerConfirm's plan summary.
type stabilizerPlanMsg struct {
	willAdopt bool
}

// computeStabilizerPlanCmd checks (read-only) whether a standalone aileron
// release is present, off the UI thread, so screenStabilizerPlanning's
// "Computing plan..." message can show while it runs.
func computeStabilizerPlanCmd(kubectlBin string) tea.Cmd {
	return func() tea.Msg {
		return stabilizerPlanMsg{willAdopt: stabilizer.StandaloneAileronReleasePresent(kubectlBin)}
	}
}
