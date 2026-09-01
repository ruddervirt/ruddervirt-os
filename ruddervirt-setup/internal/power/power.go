// SPDX-License-Identifier: GPL-3.0-only

// Package power holds the main menu's "power options" submenu's two
// destructive actions - reboot and shutdown - each a single systemctl call
// handed to systemd, same shape as internal/osupdate's rpm-ostree step.
package power

import (
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/installsteps"
)

// SystemctlBinPath is FCOS's systemctl - the single command both steps
// below run, differing only in their final argument.
const SystemctlBinPath = "/usr/bin/systemctl"

// RebootStepLabel/ShutdownStepLabel name the single step in RebootSteps/
// ShutdownSteps respectively.
const (
	RebootStepLabel   = "Rebooting"
	ShutdownStepLabel = "Shutting down"
)

// RebootSteps is screens.PowerModel's Action == "reboot" pipeline:
// `systemctl reboot` hands off to systemd and returns almost immediately -
// the actual reboot happens moments later, asynchronously, once systemd
// finishes stopping services.
var RebootSteps = []installsteps.Step{
	{
		Label: RebootStepLabel,
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			wrap := func(l string) installsteps.StepMsg { return installsteps.StepOutputMsg(l) }
			err := exec.RunStreamed(ch, wrap, SystemctlBinPath, "reboot")
			ch <- installsteps.StepDoneMsg{Label: RebootStepLabel, Err: err}
		},
	},
}

// ShutdownSteps is screens.PowerModel's Action == "shutdown" pipeline - same
// hand-off-and-return shape as RebootSteps, via `systemctl poweroff`.
var ShutdownSteps = []installsteps.Step{
	{
		Label: ShutdownStepLabel,
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			wrap := func(l string) installsteps.StepMsg { return installsteps.StepOutputMsg(l) }
			err := exec.RunStreamed(ch, wrap, SystemctlBinPath, "poweroff")
			ch <- installsteps.StepDoneMsg{Label: ShutdownStepLabel, Err: err}
		},
	},
}
