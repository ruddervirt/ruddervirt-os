// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/osupdate"
)

// osUpdateAvailableMsg is a background, non-interactive check for whether
// rpm-ostree already sees a newer deployment - purely for the Update
// screen's "available" icon (see updateRowHasUpgrade, view.go). Unlike
// osupdate.OSUpdateSteps (interactive-sudo RunStreamed), this must never
// risk a password prompt fighting bubbletea's raw-terminal mode, so it uses
// the same non-interactive/best-effort path status.go's background polls
// use - any failure just means no icon, never a visible error.
type osUpdateAvailableMsg struct {
	available bool
}

func checkOSUpdateAvailableCmd() tea.Cmd {
	return func() tea.Msg {
		return osUpdateAvailableMsg{available: osupdate.OSUpdateAvailable()}
	}
}
