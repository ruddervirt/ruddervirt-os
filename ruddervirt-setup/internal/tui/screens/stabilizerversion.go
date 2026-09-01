// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"

	"ruddervirt-setup/internal/tui"
	"ruddervirt-setup/internal/tui/pipeline"
)

// StabilizerVersionModel is the guarded chart-version-change confirm+apply
// screen's sub-model (screenStabilizerVersionConfirm/Apply) - reached from
// the Update screen's "Aileron version" row once stabilizer is detected
// (see UpdateModel's doc comment on the versions.aileron special-casing
// that dispatches into this group, which stays in app.go since it's
// cross-group routing). The fifth and last screen group to wire in
// internal/tui/pipeline's generic step-runner: Pipeline backs
// screenStabilizerVersionApply exactly the way
// StabilizerSettingsModel.Pipeline backs screenStabilizerSettingsApply,
// for a freshly-built two-step pipeline (patch, then watch the rollout -
// stabilizerVersionApplySteps) built from an already-validated patch
// (internal/stabilizer's PlanStabilizerVersionUpgrade) instead of a single
// setting's value.
//
// Deliberately holds no Update method of its own - same reasoning as
// StabilizerSettingsModel: the picker's choice (screenUpdateVersions)
// validates and populates Target/Patch/ClearedPins via
// internal/stabilizer.PlanStabilizerVersionUpgrade, and this screen's own
// "yes" submit launches Pipeline via pipeline.New - both stay in app.go,
// called directly against this struct's fields.
type StabilizerVersionModel struct {
	// Target/Patch/ClearedPins carry PlanStabilizerVersionUpgrade's
	// validated result from the picker confirm into ViewConfirm's summary
	// and, on "yes", into Pipeline - the patch is built once (at pick
	// time, package main's app.go), not re-derived at confirm time, so
	// what's shown is exactly what gets applied.
	Target       string
	Patch        []byte
	ClearedPins  []string
	ConfirmInput textinput.Model
	ConfirmError string

	Pipeline pipeline.Model
}

// ClearConfirm blanks and blurs ConfirmInput and clears ConfirmError,
// leaving Target/Patch/ClearedPins/Pipeline untouched - app.go calls this
// when Esc cancels screenStabilizerVersionConfirm straight back to Update
// (Pipeline is already zero there).
func (m StabilizerVersionModel) ClearConfirm() StabilizerVersionModel {
	m.ConfirmInput.SetValue("")
	m.ConfirmInput.Blur()
	m.ConfirmError = ""
	return m
}

// Reset clears every field back to its zero value, including
// Target/Patch/ClearedPins/Pipeline (left untouched by ClearConfirm above) -
// called by app.go's generic cross-group Esc handler when abandoning a
// flow back to screenMenu.
func (m StabilizerVersionModel) Reset() StabilizerVersionModel {
	m = m.ClearConfirm()
	m.Target = ""
	m.Patch = nil
	m.ClearedPins = nil
	m.Pipeline = pipeline.Model{}
	return m
}

// ViewConfirm renders screenStabilizerVersionConfirm. current is the
// declared version to compare against ("(unset)" if none), passed in by
// app.go since it requires the root Model's stabilizerSettingsState cache
// (cross-group, per package doc).
func (m StabilizerVersionModel) ViewConfirm(current string) string {
	s := "\n" + tui.TitleStyle.Render("Confirm Version Change") + "\n\n"
	s += fmt.Sprintf("%s -> %s\n", current, m.Target)
	if len(m.ClearedPins) > 0 {
		s += "\nAlso clearing these redundant image pins (restating this release's own tag):\n"
		for _, p := range m.ClearedPins {
			s += "  " + p + "\n"
		}
	}
	s += "\nApplying this RESTARTS THE WHOLE RELEASE: stabilizer, vncauthproxy, the\n"
	s += "aileron operator, and the VNC gateway all restart (roughly 30-90 seconds).\n"
	s += "Consoles drop and the zone goes quiet to the cloud UI during that window.\n"
	s += "Running VMs are NOT affected. This is not a hot-reload.\n"
	s += fmt.Sprintf("\n%s\n  %s\n", tui.HelpStyle.Render("Type \"yes\" to proceed, or Esc to cancel:"), m.ConfirmInput.View())
	if m.ConfirmError != "" {
		s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render(m.ConfirmError))
	}
	return s
}

// ViewApply renders screenStabilizerVersionApply - the running apply
// pipeline itself - via pipeline.Model.View. Unlike
// StabilizerSettingsModel.ViewApply (which returns to Settings), both
// hints point back to Update - this is only ever reached from
// screenUpdateVersions.
func (m StabilizerVersionModel) ViewApply(visibleLines int) string {
	return m.Pipeline.View(
		fmt.Sprintf("Patching to %s...", m.Target),
		tui.SuccessStyle.Render("Applied and rolled out.")+" "+tui.HelpStyle.Render("Press Esc to return to Update."),
		tui.ErrorStyle.Render("Failed.")+" "+tui.HelpStyle.Render("Press Esc to return to Update."),
		visibleLines,
	)
}
