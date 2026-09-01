// SPDX-License-Identifier: GPL-3.0-only

// Package power holds the main menu's "power options" submenu's one
// destructive action - reboot - a single systemctl call handed to systemd,
// same shape as internal/osupdate's rpm-ostree step. Deliberately no
// shutdown counterpart: powering off entirely takes the host - and every VM
// on it - offline until someone physically (or via the hypervisor's own
// out-of-band management) turns it back on, with no way back from inside
// this TUI; an operator who genuinely needs that can still reach `shutdown`
// from the shell menu entry.
package power

import (
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/installsteps"
)

// SystemctlBinPath is FCOS's systemctl, run below with a single "reboot" arg.
const SystemctlBinPath = "/usr/bin/systemctl"

// RebootStepLabel names the single step in RebootSteps.
const RebootStepLabel = "Rebooting"

// RebootSteps is screens.PowerModel's reboot pipeline: `systemctl reboot`
// hands off to systemd and returns almost immediately - the actual reboot
// happens moments later, asynchronously, once systemd finishes stopping
// services.
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
