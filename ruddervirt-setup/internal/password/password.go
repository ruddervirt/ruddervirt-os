// SPDX-License-Identifier: GPL-3.0-only

package password

import (
	"fmt"
	"strings"

	"ruddervirt-setup/internal/exec"
)

const (
	adminUsername = "admin"
	// defaultAdminPasswordHash mirrors the password_hash baked into server.bu
	// for both root and admin; if unchanged, the operator hasn't set a new one.
	defaultAdminPasswordHash = "$6$ruddervirt0$hVyBGZxUjDrV7kW.cEWoJxBLIWzCDxPDRjE3NgDEmeAMoNEpXoJa2fR6PGEKu5dnI78I22zK5z1cC.IEDOnAZ/"
	shadowPath               = "/etc/shadow"
	// credentialsBannerPath is the /etc/issue.d snippet (see server.bu) that
	// prints the default admin/ruddervirt login at every console/SSH login
	// prompt - stale once the password has actually been changed.
	credentialsBannerPath = "/etc/issue.d/30_ruddervirt-credentials.issue"
	// MinAdminPasswordLength is the minimum length enforced when the
	// operator sets a new admin password (see the forced password-change
	// flow in app.go).
	MinAdminPasswordLength = 8
)

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

// AdminPasswordIsDefault reports whether the admin account's shadow hash
// still matches the well-known default.
func AdminPasswordIsDefault() (bool, error) {
	out, err := exec.RunPrivileged("/usr/bin/grep", "-m", "1", "^"+adminUsername+":", shadowPath).Output()
	if err != nil {
		return false, fmt.Errorf("could not read %s for %s: %w", shadowPath, adminUsername, err)
	}
	hash, err := parseShadowHash(string(out))
	if err != nil {
		return false, err
	}
	return hash == defaultAdminPasswordHash, nil
}

// SetAdminPassword sets the admin login password via chpasswd, which
// (unlike passwd) accepts the new password on stdin, not an interactive tty.
func SetAdminPassword(newPassword string) error {
	cmd := exec.RunPrivileged("/usr/sbin/chpasswd")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("%s:%s\n", adminUsername, newPassword))
	if out, err := cmd.CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	return nil
}

// RemoveCredentialsBanner deletes credentialsBannerPath. -f makes this a
// no-op (not an error) if it's already gone, e.g. from a previous configure
// run.
func RemoveCredentialsBanner() error {
	if out, err := exec.RunPrivileged("/usr/bin/rm", "-f", credentialsBannerPath).CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	return nil
}
