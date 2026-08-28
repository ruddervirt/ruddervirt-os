// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// rpmOstreeBinPath is the FCOS base image's rpm-ostree - ruddervirt-setup
// already shells out to it for layered-package installs (install_steps.go);
// this file adds the "Operating system" row on the Update screen, which
// updates the OS image itself.
const rpmOstreeBinPath = "/usr/bin/rpm-ostree"

// osUpdateAvailableCheckTimeout bounds the background availability check
// (os_update.go's checkOSUpdateAvailableCmd) - "upgrade --check" does a real
// network fetch against the update server, unlike the plain systemctl/kubectl
// probes statusCheckTimeout (status.go) is sized for.
const osUpdateAvailableCheckTimeout = 20 * time.Second

// osUpdateSteps is the single-step pipeline the Update screen's "Operating
// system" row runs: `rpm-ostree upgrade --bypass-driver` stages the latest
// available deployment immediately, bypassing Zincati's own update-driver
// scheduling (System.AutoUpdate/writeZincatiConfig, install_steps.go) - an
// explicit "check and update now" the operator asked for, distinct from the
// background auto-update toggle. This does NOT reboot: like every rpm-ostree
// upgrade, the newly staged deployment only takes effect on the next reboot.
var osUpdateSteps = []installStep{
	{
		label: "Updating operating system",
		run: func(cfg Config, ch chan<- tea.Msg) {
			const label = "Updating operating system"
			err := runStreamed(ch, rpmOstreeBinPath, "upgrade", "--bypass-driver")
			ch <- stepDoneMsg{label: label, err: err}
		},
	},
}

// currentOSVersion reads VERSION_ID out of /etc/os-release for the Update
// screen's "Operating system" row - world-readable, so unlike everything
// else this file does it needs no privilege at all. Best-effort: an empty
// return (missing file, no VERSION_ID line) just means the row shows
// nothing where a version normally would, never an error.
func currentOSVersion() string {
	return currentOSVersionFromPath("/etc/os-release")
}

// currentOSVersionFromPath is currentOSVersion with the path pulled out so
// tests can point it at a fixture instead of the real /etc/os-release.
func currentOSVersionFromPath(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "VERSION_ID="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// osUpdateAvailableMsg carries a background, non-interactive check for
// whether rpm-ostree already sees a newer deployment - purely for the
// Update screen's "available" icon (see updateRowHasUpgrade, view.go).
// Unlike osUpdateSteps (which actually applies the update via the
// interactive-sudo runStreamed path), this must never risk a password
// prompt fighting bubbletea's raw-terminal mode, so it goes through the same
// non-interactive/best-effort path status.go's background polls use - any
// failure (not FCOS, no cached sudo ticket, offline) just means no icon,
// never a visible error.
type osUpdateAvailableMsg struct {
	available bool
}

func checkOSUpdateAvailableCmd() tea.Cmd {
	return func() tea.Msg {
		return osUpdateAvailableMsg{available: osUpdateAvailable()}
	}
}

// osUpdateAvailable runs `rpm-ostree upgrade --check` (refreshes rpm-ostree's
// own cached-update state; its exit code is deliberately ignored - it exits
// non-zero when there's simply nothing new) and then greps `rpm-ostree
// status`'s plain-text output for the "AvailableUpdate" stanza header,
// which rpm-ostree only ever prints once a newer deployment has actually
// been found - the same contract coreos/rpm-ostree's own
// tests/vmcheck/test-autoupdate-check.sh asserts against.
func osUpdateAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), osUpdateAvailableCheckTimeout)
	defer cancel()
	_, _ = runNonInteractive(ctx, rpmOstreeBinPath, "upgrade", "--check").CombinedOutput()

	ctx2, cancel2 := context.WithTimeout(context.Background(), osUpdateAvailableCheckTimeout)
	defer cancel2()
	out, err := runNonInteractive(ctx2, rpmOstreeBinPath, "status").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "AvailableUpdate")
}
