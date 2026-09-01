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

// ViewAutoUpdateNotice renders screenOSUpdateConfirm - a heads-up shown
// before starting OSUpdateSteps only when cfg.System.AutoUpdate is already
// on (see app.go's IsOSUpdate handling), since a manual update is likely
// redundant with the background auto-update Zincati is already driving.
// Enter proceeds anyway, Esc cancels back to Update - no "type yes" gate
// like the destructive confirms elsewhere, since this doesn't need
// protecting from itself: it's the same harmless, no-reboot-required
// `rpm-ostree upgrade` OSUpdateSteps always runs, just possibly unnecessary.
func (m OSUpdateModel) ViewAutoUpdateNotice() string {
	s := "\n" + tui.TitleStyle.Render("Update Operating System") + "\n\n"
	s += tui.WarningStyle.Render("Automatic OS package updates are already turned on for this host.") + "\n"
	s += "A manual update here is likely unnecessary - Zincati is already staging\n"
	s += "new deployments in the background on its own schedule.\n"
	s += "\n" + tui.HintBar([2]string{"Enter", "update anyway"}, [2]string{"Esc", "cancel"}) + "\n"
	return s
}

// View renders screenOSUpdate via pipeline.Model.View. Unlike
// InstallModel.ViewInstall (which returns to the main menu), both hints
// point back to the Update screen - this is only ever reached from
// screenUpdateVersions. The done copy's "Press r to reboot now" offers the
// reboot it just told the operator they need, straight from here - see
// app.go's tea.KeyRunes handling and model.go's powerConfirmOrigin doc
// comment for how that reuses the power-options reboot flow.
func (m OSUpdateModel) View(visibleLines int) string {
	return m.Pipeline.View(
		"Updating operating system...",
		tui.SuccessStyle.Render("Staged. Reboot to switch into the new deployment.")+" "+tui.HelpStyle.Render("Press r to reboot now, or Esc to return to Update."),
		tui.ErrorStyle.Render("Update failed.")+" "+tui.HelpStyle.Render("Press Esc to return to Update."),
		visibleLines,
	)
}
