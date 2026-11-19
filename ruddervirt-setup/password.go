// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/password"
)

// passwordCheckMsg carries the result of checkPasswordChangedCmd back into
// Update.
type passwordCheckMsg struct {
	state password.AdminPasswordState
	err   error
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
// password.CheckAdminPassword shells out via sudo and would otherwise
// block the UI loop - same reasoning as saveSettingCmd in config.go.
func checkPasswordChangedCmd() tea.Cmd {
	return func() tea.Msg {
		state, err := password.CheckAdminPassword()
		return passwordCheckMsg{state: state, err: err}
	}
}

// finalizePasswordChangeCmd locks the root account, then persists
// cfg.System.PasswordChanged/RootLocked=true so future "configure" entries
// skip re-checking /etc/shadow, then removes the now-stale credentials
// banner.
//
// Locking root here (rather than only in server.bu) is what fixes up a node
// installed from an image that baked the same well-known default hash into
// root as into admin: every path that confirms the admin password funnels
// through this one command. The lock is deliberately fatal rather than
// best-effort - silently leaving a known root password in place is the exact
// failure this guards against, and the operator is better off seeing the
// error than reaching Settings believing the node is secured.
//
// The AdminCanLogIn check in front of it is the counterweight: root's
// password is the fallback route in, and server.bu provisions no SSH keys,
// so locking root on a node whose admin account is itself locked or
// password-less would leave no way in at all. Refusing to lock leaves the
// node reachable and the problem visible, which beats a node that can only
// be recovered from the bootloader.
func finalizePasswordChangeCmd(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		canLogIn, err := password.AdminCanLogIn()
		if err != nil {
			return passwordFinalizedMsg{err: err}
		}
		if !canLogIn {
			return passwordFinalizedMsg{err: errors.New(
				"admin has neither a password nor an SSH key, so locking root would leave no way into this node - " +
					"set an admin password (passwd admin) or add a key to ~admin/.ssh/authorized_keys first")}
		}
		if err := password.LockRootAccount(); err != nil {
			return passwordFinalizedMsg{err: err}
		}
		// Confirm it actually took before recording RootLocked=true, which is
		// what stops "configure" ever checking again: a cfg claiming the node
		// is locked down while root still answers to the old well-known
		// password is the one outcome worth a second syscall to rule out.
		locked, err := password.RootIsLocked()
		if err != nil {
			return passwordFinalizedMsg{err: err}
		}
		if !locked {
			return passwordFinalizedMsg{err: errors.New("root still has a usable password after locking it - refusing to record this node as secured")}
		}
		cfg.System.PasswordChanged = true
		cfg.System.RootLocked = true
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
