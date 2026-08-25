// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// defaultHostname mirrors the /etc/hostname Ignition bakes into every image
// (see server.bu) - if that file still holds this exactly, the operator has
// never declared one of their own.
const defaultHostname = "ruddervirt-os"

// hostnamePath is where hostnameIsDefault reads from and setHostname's
// hostnamectl call writes to.
const hostnamePath = "/etc/hostname"

// hostnameLabelPattern matches a single DNS label - letters, digits, and
// hyphens, neither leading nor trailing with a hyphen - the same shape
// hostnamectl itself enforces.
var hostnameLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// parseHostname validates val as a hostname hostnamectl will actually
// accept: a dot-separated sequence of DNS labels, 253 characters or fewer.
func parseHostname(val string) (string, error) {
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

// hostnameIsDefault reports whether hostnamePath still holds defaultHostname
// - i.e. whether the operator has never declared one of their own. Reads
// the file rather than the live kernel hostname (os.Hostname()) deliberately:
// on a real boot systemd keeps the two in sync, but under `make
// test-container` the container runtime sets its own kernel hostname (e.g.
// the container ID) independent of the /etc/hostname the dev overlay writes
// - os.Hostname() would then never match defaultHostname even on an
// undeclared node, silently skipping this whole forced flow. Reading the
// file needs no privilege (mode 0644, see server.bu), so it's safe to call
// directly wherever a screen decision is made, no async command needed.
func hostnameIsDefault() bool {
	data, err := os.ReadFile(hostnamePath)
	return err == nil && strings.TrimSpace(string(data)) == defaultHostname
}

// hostnameLocked reports whether the hostname must no longer change:
// k3s.service (installSteps' "Enabling and starting k3s") starts `k3s
// server` with no --node-name flag (see k3sUnitContent, install_steps.go),
// so k3s registers this node under whatever the live hostname is at that
// moment - changing it afterward would orphan the existing Node object
// instead of renaming it. installedK3sVersion (k3s.go) reports whether the
// k3s binary has been installed, which - since installSteps runs straight
// through to "Enabling and starting k3s" a few steps later with no operator
// interaction in between - is close enough to "has this node's install
// pipeline run" to treat as the point of no return, same "lock on the first
// irreversible commitment" reasoning as storage.go's storageEngineApplied.
func hostnameLocked() bool {
	_, ok := installedK3sVersion()
	return ok
}

// setHostname sets the hostname to newHostname. Tries hostnamectl first -
// the systemd-native way, which updates the running kernel hostname and
// hostnamePath together - but falls back to writing hostnamePath directly
// (plus a best-effort classic `hostname` command for the live value, which
// just calls sethostname() with no D-Bus dependency) if hostnamectl fails.
// That fallback matters because this step is mandatory with no skip (see
// the screenHostnameChange case in the KeyCtrlS handler, app_update.go) -
// it must never dead-end an operator on a system where hostnamectl can't
// reach systemd-hostnamed, e.g. `make test-container`'s plain FCOS userland
// running under docker/podman with no systemd as PID 1. hostnamectl
// rejecting the input isn't a realistic failure mode to worry about
// distinguishing here - parseHostname already validates it first.
func setHostname(newHostname string) error {
	if _, err := runPrivileged("/usr/bin/hostnamectl", "set-hostname", newHostname).CombinedOutput(); err == nil {
		return nil
	}
	// Deliberately not writePrivileged: its temp-file-then-rename pattern
	// fails with "Device or resource busy" here, because container
	// runtimes (docker/podman) bind-mount /etc/hostname as an individual
	// file - same as /etc/hosts and /etc/resolv.conf - and the kernel
	// refuses a rename() onto an active mountpoint. tee writes in place
	// (open+truncate+write, no rename) instead, which works on both a
	// bind-mounted file and a normal one.
	cmd := runPrivileged("/usr/bin/tee", hostnamePath)
	cmd.Stdin = strings.NewReader(newHostname + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return wrapCmdErr(out, err)
	}
	// Best-effort: hostnameIsDefault reads hostnamePath, not the live
	// value, so the write above is what actually unlocks the rest of the
	// flow - a failure here (e.g. no CAP_SYS_ADMIN in this container)
	// isn't fatal to that.
	_, _ = runPrivileged("/usr/bin/hostname", newHostname).CombinedOutput()
	return nil
}

// hostnameSetMsg carries setHostnameCmd's result back into Update.
type hostnameSetMsg struct {
	err error
}

func setHostnameCmd(newHostname string) tea.Cmd {
	return func() tea.Msg {
		return hostnameSetMsg{err: setHostname(newHostname)}
	}
}

// hostnameDeclaredMsg carries finalizeHostnameDeclaredCmd's result back into
// Update - used both when the operator just set a hostname through
// screenHostnameChange, and when Init() finds the hostname already
// customized outside this flow (see finalizeHostnameDeclaredCmd).
type hostnameDeclaredMsg struct {
	cfg Config
	err error
}

// finalizeHostnameDeclaredCmd persists cfg.System.HostnameDeclared=true so
// future launches/"configure" entries skip re-checking the live hostname -
// same reasoning as finalizePasswordChangeCmd in password.go.
func finalizeHostnameDeclaredCmd(cfg Config) tea.Cmd {
	return func() tea.Msg {
		cfg.System.HostnameDeclared = true
		err := saveConfig(cfg, configPath)
		return hostnameDeclaredMsg{cfg: cfg, err: err}
	}
}
