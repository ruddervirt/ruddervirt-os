// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/password"
)

// passwordCheckMsg carries the result of checkPasswordChangedCmd back into
// Update.
type passwordCheckMsg struct {
	unchanged bool
	err       error
}

// passwordFinalizedMsg carries the result of finalizePasswordChangeCmd back
// into Update - recording cfg.System.PasswordChanged and removing the
// stale credentials banner, once the admin password is known to differ
// from the default.
type passwordFinalizedMsg struct {
	cfg config.Config
	err error
}

// checkPasswordChangedCmd runs as a tea.Cmd (its own goroutine) since
// password.AdminPasswordIsDefault shells out via sudo and would otherwise
// block the UI loop - same reasoning as saveSettingCmd in config.go.
func checkPasswordChangedCmd() tea.Cmd {
	return func() tea.Msg {
		unchanged, err := password.AdminPasswordIsDefault()
		return passwordCheckMsg{unchanged: unchanged, err: err}
	}
}

// finalizePasswordChangeCmd persists cfg.System.PasswordChanged=true so
// future "configure" entries skip re-checking /etc/shadow, then removes the
// now-stale credentials banner.
func finalizePasswordChangeCmd(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		cfg.System.PasswordChanged = true
		if err := config.SaveConfig(cfg, config.ConfigPath); err != nil {
			return passwordFinalizedMsg{err: err}
		}
		// Best-effort: the banner is cosmetic, and the password itself is
		// already changed and recorded - a failure removing it shouldn't
		// block the operator from reaching Settings.
		_ = password.RemoveCredentialsBanner()
		return passwordFinalizedMsg{cfg: cfg}
	}
}
