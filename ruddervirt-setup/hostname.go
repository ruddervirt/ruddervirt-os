// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/k3s"
)

// hostnameLocked reports whether the hostname must no longer change: k3s.service
// (installSteps' "Enabling and starting k3s") starts `k3s server` with no
// --node-name flag (see k3sUnitContent, install_steps.go), so k3s registers
// this node under whatever hostname is live at that moment - changing it
// afterward would orphan the Node object instead of renaming it.
// k3s.InstalledK3sVersion (whether the binary is installed) is close enough
// to "install pipeline has run" to treat as the point of no return, same
// lock-on-first-irreversible-commitment reasoning as internal/storage's
// StorageEngineApplied.
//
// Stays in package main since it only feeds initialModel/Init's
// hostname-declared gating, itself package-main-only (model.go).
func hostnameLocked() bool {
	_, ok := k3s.InstalledK3sVersion()
	return ok
}

// hostnameDeclaredMsg carries finalizeHostnameDeclaredCmd's result back into
// Update - used both when the operator just set a hostname through
// screenHostnameChange, and when Init() finds the hostname already
// customized outside this flow (see finalizeHostnameDeclaredCmd).
type hostnameDeclaredMsg struct {
	cfg config.Config
	err error
}

// finalizeHostnameDeclaredCmd persists cfg.System.HostnameDeclared=true so
// future launches/"configure" entries skip re-checking the live hostname -
// same reasoning as finalizePasswordChangeCmd in password.go.
func finalizeHostnameDeclaredCmd(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		cfg.System.HostnameDeclared = true
		err := config.SaveConfig(cfg, config.ConfigPath)
		return hostnameDeclaredMsg{cfg: cfg, err: err}
	}
}
