// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"ruddervirt-setup/internal/tui"
	"ruddervirt-setup/internal/tui/pipeline"
)

// OSUpdateModel is the OS-update screen's sub-model (screenOSUpdate only) -
// the simplest of the pipeline-backed screen groups: it just wires
// pipeline.Model around internal/osupdate.OSUpdateSteps, following the
// InstallModel/UpdateModel pattern. Unlike screenUpdateVersions ->
// screenUpdateConfirm -> screenUpdate, there's no separate check/confirm
// step: the Update screen launches Pipeline directly on Enter (rpm-ostree
// only ever moves to the single latest deployment on the configured
// stream, and doesn't take effect until a reboot, so there's nothing
// meaningful to confirm first - see app.go). So this struct holds no
// text-input/confirm state and has no Update method of its own: every
// message it needs routes straight into Pipeline.Update from app.go, same
// as InstallModel.Pipeline.
type OSUpdateModel struct {
	Pipeline pipeline.Model
}

// View renders screenOSUpdate via pipeline.Model.View. Unlike
// InstallModel.ViewInstall (which returns to the main menu), both hints
// point back to the Update screen - this is only ever reached from
// screenUpdateVersions.
func (m OSUpdateModel) View(visibleLines int) string {
	return m.Pipeline.View(
		"Updating operating system...",
		tui.SuccessStyle.Render("Staged. Reboot to switch into the new deployment.")+" "+tui.HelpStyle.Render("Press Esc to return to Update."),
		tui.ErrorStyle.Render("Update failed.")+" "+tui.HelpStyle.Render("Press Esc to return to Update."),
		visibleLines,
	)
}
