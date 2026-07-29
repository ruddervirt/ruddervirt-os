package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	adminUsername = "admin"
	// defaultAdminPasswordHash mirrors the password_hash baked into
	// server.bu for both root and admin ($6$ruddervirt0$...). If the admin
	// account's current shadow hash still matches this exactly, the
	// operator has never changed it from the well-known default.
	defaultAdminPasswordHash = "$6$ruddervirt0$hVyBGZxUjDrV7kW.cEWoJxBLIWzCDxPDRjE3NgDEmeAMoNEpXoJa2fR6PGEKu5dnI78I22zK5z1cC.IEDOnAZ/"
	shadowPath               = "/etc/shadow"
	// credentialsBannerPath is the /etc/issue.d snippet (see server.bu) that
	// prints the default admin/ruddervirt login at every console/SSH login
	// prompt - stale once the password has actually been changed.
	credentialsBannerPath  = "/etc/issue.d/30_ruddervirt-credentials.issue"
	minAdminPasswordLength = 8
)

// passwordCheckMsg carries the result of checkPasswordChangedCmd back into
// Update.
type passwordCheckMsg struct {
	unchanged bool
	err       error
}

// passwordSetMsg carries the result of setAdminPasswordCmd back into
// Update.
type passwordSetMsg struct {
	err error
}

// passwordFinalizedMsg carries the result of finalizePasswordChangeCmd back
// into Update - recording cfg.System.PasswordChanged and removing the
// stale credentials banner, once the admin password is known to differ
// from the default.
type passwordFinalizedMsg struct {
	cfg Config
	err error
}

// parseShadowHash extracts the password-hash field (the 2nd colon-separated
// field) from a single /etc/shadow line, e.g. as returned by
// `grep '^admin:' /etc/shadow`.
func parseShadowHash(line string) (string, error) {
	fields := strings.SplitN(strings.TrimSpace(line), ":", 3)
	if len(fields) < 2 || fields[1] == "" {
		return "", fmt.Errorf("unexpected shadow entry: %q", line)
	}
	return fields[1], nil
}

// adminPasswordIsDefault reports whether the admin account's current
// shadow hash still matches defaultAdminPasswordHash - i.e. whether the
// operator has never changed it from the well-known default.
func adminPasswordIsDefault() (bool, error) {
	out, err := runPrivileged("/usr/bin/grep", "-m", "1", "^"+adminUsername+":", shadowPath).Output()
	if err != nil {
		return false, fmt.Errorf("could not read %s for %s: %w", shadowPath, adminUsername, err)
	}
	hash, err := parseShadowHash(string(out))
	if err != nil {
		return false, err
	}
	return hash == defaultAdminPasswordHash, nil
}

// setAdminPassword sets the admin account's login password to newPassword
// via chpasswd, which - unlike passwd - accepts the new password on stdin
// instead of requiring an interactive terminal prompt.
func setAdminPassword(newPassword string) error {
	cmd := runPrivileged("/usr/sbin/chpasswd")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("%s:%s\n", adminUsername, newPassword))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// removeCredentialsBanner deletes credentialsBannerPath. -f makes this a
// no-op (not an error) if it's already gone, e.g. from a previous configure
// run.
func removeCredentialsBanner() error {
	if out, err := runPrivileged("/usr/bin/rm", "-f", credentialsBannerPath).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// checkPasswordChangedCmd runs as a tea.Cmd (its own goroutine) since
// adminPasswordIsDefault shells out via sudo and would otherwise block the
// UI loop - same reasoning as saveSettingCmd in config.go.
func checkPasswordChangedCmd() tea.Cmd {
	return func() tea.Msg {
		unchanged, err := adminPasswordIsDefault()
		return passwordCheckMsg{unchanged: unchanged, err: err}
	}
}

func setAdminPasswordCmd(newPassword string) tea.Cmd {
	return func() tea.Msg {
		return passwordSetMsg{err: setAdminPassword(newPassword)}
	}
}

// finalizePasswordChangeCmd persists cfg.System.PasswordChanged=true so
// future "configure" entries skip re-checking /etc/shadow, then removes the
// now-stale credentials banner.
func finalizePasswordChangeCmd(cfg Config) tea.Cmd {
	return func() tea.Msg {
		cfg.System.PasswordChanged = true
		if err := saveConfig(cfg, configPath); err != nil {
			return passwordFinalizedMsg{err: err}
		}
		// Best-effort: the banner is cosmetic, and the password itself is
		// already changed and recorded - a failure removing it shouldn't
		// block the operator from reaching Settings.
		_ = removeCredentialsBanner()
		return passwordFinalizedMsg{cfg: cfg}
	}
}
