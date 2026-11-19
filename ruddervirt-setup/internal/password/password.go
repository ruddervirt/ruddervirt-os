// SPDX-License-Identifier: GPL-3.0-only

package password

import (
	"fmt"
	"strings"

	"ruddervirt-setup/internal/exec"
)

const (
	adminUsername = "admin"
	rootUsername  = "root"
	// defaultAdminPasswordHash mirrors the password_hash baked into server.bu
	// for admin; if unchanged, the operator hasn't set a new one. Images up to
	// and including the one that added LockRootAccount also gave root this
	// same hash, which is why locking root is a fix-up step and not just a
	// server.bu change.
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
//
// An empty hash field is a valid entry, not a parse failure: `passwd -d`
// leaves one, and it means the account logs in with no password at all.
// Callers have to tell that apart from "some hash we don't recognize", so it
// comes back as "" rather than an error - see AdminPasswordUnset.
func parseShadowHash(line string) (string, error) {
	fields := strings.SplitN(strings.TrimSpace(line), ":", 3)
	if len(fields) < 2 {
		return "", fmt.Errorf("unexpected shadow entry: %q", line)
	}
	return fields[1], nil
}

// AdminPasswordState describes how admin's shadow entry stands relative to
// what the setup flow requires before it will hand over Settings.
type AdminPasswordState int

const (
	// AdminPasswordOK is a hash that is neither the well-known default nor
	// empty - either a password the operator chose, or a deliberate lock
	// ("!"/"*") on a node the operator reaches by SSH key instead. Nothing to
	// force: there is no guessable password here. Note that a locked admin is
	// only viable if keys were added by hand - server.bu provisions none - so
	// AdminCanLogIn, not this state, is what guards against locking root on a
	// node that would then have no way in at all.
	AdminPasswordOK AdminPasswordState = iota
	// AdminPasswordDefault is still server.bu's well-known default hash.
	AdminPasswordDefault
	// AdminPasswordUnset is an empty hash field: login with no password
	// whatsoever, which `passwd -d admin` leaves behind. It is strictly worse
	// than the default password, so the flow forces a change here too - a
	// plain "is it the default hash?" test would wave it through.
	AdminPasswordUnset
)

// NeedsChange reports whether the operator must set a new admin password
// before continuing.
func (s AdminPasswordState) NeedsChange() bool { return s != AdminPasswordOK }

// shadowHash returns user's password-hash field from /etc/shadow.
func shadowHash(user string) (string, error) {
	out, err := exec.RunPrivileged("/usr/bin/grep", "-m", "1", "^"+user+":", shadowPath).Output()
	if err != nil {
		return "", fmt.Errorf("could not read %s for %s: %w", shadowPath, user, err)
	}
	return parseShadowHash(string(out))
}

// isLockedHash reports whether hash is one no password can satisfy: "!" from
// passwd -l, or "*" for an account never given a password, with or without a
// hash left behind after it. An empty hash is emphatically not locked - it
// means login with no password at all.
func isLockedHash(hash string) bool {
	return strings.HasPrefix(hash, "!") || strings.HasPrefix(hash, "*")
}

// CheckAdminPassword classifies the admin account's shadow hash.
func CheckAdminPassword() (AdminPasswordState, error) {
	hash, err := shadowHash(adminUsername)
	if err != nil {
		return AdminPasswordOK, err
	}
	switch hash {
	case defaultAdminPasswordHash:
		return AdminPasswordDefault, nil
	case "":
		return AdminPasswordUnset, nil
	}
	return AdminPasswordOK, nil
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

// RootIsLocked reports whether root's shadow hash is one that no password
// can ever satisfy: "!" (passwd -l), "*" (Fedora CoreOS's own default for an
// account that was never given a password) or either with nothing after it.
// Anything else - notably the default hash older images baked in for root -
// means root can still be logged into directly.
func RootIsLocked() (bool, error) {
	hash, err := shadowHash(rootUsername)
	if err != nil {
		return false, err
	}
	return isLockedHash(hash), nil
}

// AdminCanLogIn reports whether admin still has some usable way in: a real
// password hash (not the "!"/"*" of a locked account, nor the empty field
// `passwd -d` leaves), or failing that at least one SSH authorized key.
//
// This exists to keep LockRootAccount from removing the last route into a
// node. server.bu provisions no SSH keys, so admin's password is normally
// the only credential that exists; an operator who locks admin without
// having added a key would, once root is locked too, be left with nothing
// short of a bootloader/initramfs recovery. Any failure reading the key
// files is reported as "cannot log in" - erring toward leaving root's
// fallback alone rather than toward locking the operator out.
func AdminCanLogIn() (bool, error) {
	hash, err := shadowHash(adminUsername)
	if err != nil {
		return false, err
	}
	// Any hash that isn't a lock and isn't empty is a password someone can
	// actually use - including, at this point, the well-known default.
	if hash != "" && !isLockedHash(hash) {
		return true, nil
	}
	return adminHasAuthorizedKey(), nil
}

// adminHasAuthorizedKey reports whether admin has at least one non-blank,
// non-comment line in either the standard authorized_keys file or Fedora
// CoreOS's authorized_keys.d fragment directory. grep exits non-zero both
// for "no match" and for a genuine read failure; both mean "assume no key"
// here, which is the cautious answer for AdminCanLogIn's purpose.
func adminHasAuthorizedKey() bool {
	home := adminHomeDir()
	err := exec.RunPrivileged("/usr/bin/grep", "-r", "-s", "-q", "-E", `^[^#[:space:]]`,
		home+"/.ssh/authorized_keys", home+"/.ssh/authorized_keys.d").Run()
	return err == nil
}

// adminHomeDir reads admin's home from passwd rather than assuming Fedora
// CoreOS's /var/home/admin, falling back to that default if getent fails.
func adminHomeDir() string {
	const fallback = "/var/home/" + adminUsername
	out, err := exec.RunPrivileged("/usr/bin/getent", "passwd", adminUsername).Output()
	if err != nil {
		return fallback
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(fields) < 6 || fields[5] == "" {
		return fallback
	}
	return fields[5]
}

// LockRootAccount prefixes root's shadow hash with "!" so no password
// authenticates as root - console, serial and su alike. admin keeps full
// root access through sudo, which authenticates the invoking user and never
// consults root's shadow entry, so this costs nothing operationally.
//
// Idempotent: passwd -l leaves an already-locked hash alone rather than
// stacking a second "!", so re-running it on a node provisioned by a
// current image (where root has no password to begin with) is a no-op.
func LockRootAccount() error {
	if out, err := exec.RunPrivileged("/usr/bin/passwd", "-l", rootUsername).CombinedOutput(); err != nil {
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
