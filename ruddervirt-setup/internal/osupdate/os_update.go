// SPDX-License-Identifier: GPL-3.0-only

// Package osupdate holds the Fedora CoreOS base image's update flow
// ("rpm-ostree upgrade"), distinct from internal/selfupdate (which updates
// the ruddervirt-setup binary). Named to disambiguate from app.go's Bubble
// Tea Update() router - an unrelated "update" in the Elm-architecture sense.
package osupdate

import (
	"context"
	"os"
	"strings"
	"time"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/installsteps"
)

// RpmOstreeBinPath is the FCOS base image's rpm-ostree, also used by
// install_steps.go for layered-package installs; here it drives the Update
// screen's "Operating system" row.
const RpmOstreeBinPath = "/usr/bin/rpm-ostree"

// osUpdateAvailableCheckTimeout bounds OSUpdateAvailable's background check:
// "upgrade --check" does a real network fetch, unlike the plain
// systemctl/kubectl probes internal/status is sized for.
const osUpdateAvailableCheckTimeout = 20 * time.Second

// UpdateStepLabel is the single step OSUpdateSteps runs.
const UpdateStepLabel = "Updating operating system"

// OSUpdateSteps is the Update screen's "Operating system" row pipeline:
// `rpm-ostree upgrade --bypass-driver` stages the latest deployment
// immediately, bypassing Zincati's own update-driver scheduling
// (config.SystemConfig.AutoUpdate/WriteZincatiConfig) - an explicit
// operator-requested update, distinct from the background auto-update
// toggle. Does NOT reboot; the staged deployment applies on next reboot.
var OSUpdateSteps = []installsteps.Step{
	{
		Label: UpdateStepLabel,
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			wrap := func(l string) installsteps.StepMsg { return installsteps.StepOutputMsg(l) }
			err := exec.RunStreamed(ch, wrap, RpmOstreeBinPath, "upgrade", "--bypass-driver")
			ch <- installsteps.StepDoneMsg{Label: UpdateStepLabel, Err: err}
		},
	},
}

// CurrentOSVersion reads VERSION_ID from /etc/os-release for the Update
// screen - world-readable, needing no privilege unlike the rest of this
// package. Best-effort: an empty return (missing file, no VERSION_ID) just
// leaves the row blank, never an error.
func CurrentOSVersion() string {
	return CurrentOSVersionFromPath("/etc/os-release")
}

// CurrentOSVersionFromPath is CurrentOSVersion with the path pulled out so
// tests can point it at a fixture instead of the real /etc/os-release.
func CurrentOSVersionFromPath(path string) string {
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

// OSUpdateAvailable runs `rpm-ostree upgrade --check` to refresh rpm-ostree's
// cached-update state (exit code ignored - it's non-zero when there's simply
// nothing new), then greps `rpm-ostree status` for the "AvailableUpdate"
// stanza header, which rpm-ostree only prints once a newer deployment is
// found (the same contract coreos/rpm-ostree's own
// tests/vmcheck/test-autoupdate-check.sh asserts). Unlike OSUpdateSteps,
// this must never risk a password prompt fighting bubbletea's raw-terminal
// mode, so it uses the same non-interactive/best-effort path as
// internal/status: any failure just means false, never a visible error.
func OSUpdateAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), osUpdateAvailableCheckTimeout)
	defer cancel()
	_, _ = exec.RunNonInteractive(ctx, RpmOstreeBinPath, "upgrade", "--check").CombinedOutput()

	ctx2, cancel2 := context.WithTimeout(context.Background(), osUpdateAvailableCheckTimeout)
	defer cancel2()
	out, err := exec.RunNonInteractive(ctx2, RpmOstreeBinPath, "status").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "AvailableUpdate")
}
