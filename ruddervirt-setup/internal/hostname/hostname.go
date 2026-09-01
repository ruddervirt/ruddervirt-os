// SPDX-License-Identifier: GPL-3.0-only

package hostname

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"ruddervirt-setup/internal/exec"
)

// defaultHostname mirrors the /etc/hostname Ignition bakes into every image
// (see server.bu); if unchanged, the operator hasn't declared one.
const defaultHostname = "ruddervirt-os"

// hostnamePath is where HostnameIsDefault reads from and SetHostname's
// hostnamectl call writes to.
const hostnamePath = "/etc/hostname"

// hostnameLabelPattern matches a single DNS label (letters, digits, hyphens,
// no leading/trailing hyphen) - the shape hostnamectl itself enforces.
var hostnameLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// ParseHostname validates val as a hostname hostnamectl will actually
// accept: a dot-separated sequence of DNS labels, 253 characters or fewer.
func ParseHostname(val string) (string, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return "", fmt.Errorf("hostname must not be empty")
	}
	if len(val) > 253 {
		return "", fmt.Errorf("hostname must be 253 characters or fewer")
	}
	for _, label := range strings.Split(val, ".") {
		if !hostnameLabelPattern.MatchString(label) {
			return "", fmt.Errorf("hostname must consist of dot-separated labels of letters, digits, and hyphens, not starting or ending with a hyphen")
		}
	}
	return val, nil
}

// HostnameIsDefault reports whether hostnamePath still holds defaultHostname,
// i.e. whether the operator has declared one of their own. Reads the file
// rather than os.Hostname(): under `make test-container` the container
// runtime sets its own kernel hostname independent of /etc/hostname, so
// os.Hostname() would never match and silently skip the forced flow. The
// file needs no privilege to read (mode 0644, see server.bu).
func HostnameIsDefault() bool {
	data, err := os.ReadFile(hostnamePath)
	return err == nil && strings.TrimSpace(string(data)) == defaultHostname
}

// SetHostname sets the hostname to newHostname. Tries hostnamectl first (the
// systemd-native way, updating both the kernel hostname and hostnamePath),
// falling back to writing hostnamePath directly plus a best-effort classic
// `hostname` command if hostnamectl fails - needed because e.g. `make
// test-container` has no systemd as PID 1 for hostnamectl to reach. This
// step is mandatory with no skip (see screenHostnameChange in app.go's
// KeyCtrlS handler), so it must never dead-end the operator. ParseHostname
// already validates the input, so a hostnamectl rejection isn't a case
// worth distinguishing here.
func SetHostname(newHostname string) error {
	if _, err := exec.RunPrivileged("/usr/bin/hostnamectl", "set-hostname", newHostname).CombinedOutput(); err == nil {
		return nil
	}
	// Not writePrivileged: its rename() pattern fails with "Device or
	// resource busy" on a bind-mounted /etc/hostname (docker/podman, like
	// /etc/hosts and /etc/resolv.conf). tee writes in place instead, which
	// works either way.
	cmd := exec.RunPrivileged("/usr/bin/tee", hostnamePath)
	cmd.Stdin = strings.NewReader(newHostname + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	// Best-effort: HostnameIsDefault reads hostnamePath (already written
	// above), not the live value, so a failure here (e.g. no CAP_SYS_ADMIN)
	// isn't fatal.
	_, _ = exec.RunPrivileged("/usr/bin/hostname", newHostname).CombinedOutput()
	return nil
}
