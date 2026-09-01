// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
)

// saveSettingCmd persists cfg after a settings-field edit as a tea.Cmd (its
// own goroutine), so a slow/root-owned-directory write never blocks the UI
// loop - same reasoning as checkPasswordChangedCmd in password.go.
func saveSettingCmd(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		return settingsSavedMsg{err: config.SaveConfig(cfg, config.ConfigPath)}
	}
}
