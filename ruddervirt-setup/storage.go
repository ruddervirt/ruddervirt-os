// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/storage"
)

// prepareStorageStep adapts storage.PrepareStorageDevice to the installsteps.Step
// shape (install_steps.go), which - like every installsteps.Step - only accepts
// (cfg config.Config, ch chan<- installsteps.StepMsg).
func prepareStorageStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Preparing storage device"
	ch <- stepDoneMsg{Label: label, Err: storage.PrepareStorageDevice(cfg.Storage.Engine, ch, wrapStepOutput, config.WritePrivileged)}
}

// planStorageDevice previews prepareStorageStep, reusing the same
// storage.AppliedStorageEngine() marker check. Reports BLOCKED rather than a
// false "will prepare disk for X" if a hand-edited config disagrees with the
// disk's locked engine - Settings already prevents picking a different
// engine once the marker exists (config.go), but the preview stays honest
// regardless of how cfg got here.
func planStorageDevice(cfg config.Config) string {
	if applied, err := storage.AppliedStorageEngine(); err == nil {
		if applied == cfg.Storage.Engine {
			return fmt.Sprintf("skip - storage device already prepared for %s", cfg.Storage.Engine)
		}
		return fmt.Sprintf("BLOCKED - disk locked to %s (reinstall OS to switch to %s)", applied, cfg.Storage.Engine)
	}
	return fmt.Sprintf("will prepare disk for %s", cfg.Storage.Engine)
}
