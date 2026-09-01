// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"

	"ruddervirt-setup/internal/stabilizer/settings"
	"ruddervirt-setup/internal/tui"
	"ruddervirt-setup/internal/tui/pipeline"
)

// StabilizerSettingsModel is the single-setting-change confirm+apply
// screen's sub-model (screenStabilizerSettingsConfirm/Apply) - reached from
// an ordinary Settings-table row once stabilizer is adopted (see
// SettingsModel.Rows' StabilizerDef rows). The fourth screen group to wire
// in internal/tui/pipeline's generic step-runner: Pipeline backs
// screenStabilizerSettingsApply exactly the way InstallModel.Pipeline backs
// screenInstall, for a freshly-built two-step pipeline (patch, then watch
// the rollout - stabilizerSettingsApplySteps) instead of a static package
// var.
//
// Deliberately holds no Update method of its own -
// screenStabilizerSettingsConfirm is reached from screenSettings' own
// Enter dispatch (Picking/Editing branches, which validate the new value
// against stabilizerSettingsState via resolveStabilizerSettingChange
// before populating this struct's Pending* fields), and its own "yes"
// submit launches Pipeline via pipeline.New - both stay in app.go, called
// directly against this struct's fields, same convention as
// SettingsModel/InstallModel.
//
// stabilizerSettingsApplySteps/tuiKubectlExec/loadStabilizerSettingsStateCmd
// (stabilizer_settings_tui.go) deliberately stay bridge functions in
// package main rather than moving into internal/stabilizer/settings: they
// build/launch a live pipeline (installsteps.Step values that shell out via
// kubectl, including the privileged tuiKubectlExec strategy, which must
// stay distinct from the CLI's unprivileged settingsKubectl) and, like
// install_steps.go's installSteps var, are exercised directly by their own
// package-main tests against exectest.FakeRunner - moving them wouldn't
// reduce coupling, just relocate it.
type StabilizerSettingsModel struct {
	// PendingDef/PendingValue/PendingCurrent hold one validated,
	// not-yet-applied change from the Settings row's picker/free-text edit
	// through screenStabilizerSettingsConfirm's "yes" to Pipeline.
	PendingDef     settings.StabilizerSettingDef
	PendingValue   any
	PendingCurrent any
	ConfirmInput   textinput.Model
	ConfirmError   string

	Pipeline pipeline.Model
}

// ClearConfirm blanks and blurs ConfirmInput and clears ConfirmError,
// leaving PendingDef/PendingValue/PendingCurrent/Pipeline untouched -
// app.go calls this when Esc cancels screenStabilizerSettingsConfirm
// straight back to Settings (Pipeline is already zero there).
func (m StabilizerSettingsModel) ClearConfirm() StabilizerSettingsModel {
	m.ConfirmInput.SetValue("")
	m.ConfirmInput.Blur()
	m.ConfirmError = ""
	return m
}

// Reset clears every field back to its zero value, including
// PendingDef/PendingValue/PendingCurrent/Pipeline (left untouched by
// ClearConfirm above) - called by app.go's generic cross-group Esc handler
// when abandoning a flow back to screenMenu.
func (m StabilizerSettingsModel) Reset() StabilizerSettingsModel {
	m = m.ClearConfirm()
	m.PendingDef = settings.StabilizerSettingDef{}
	m.PendingValue = nil
	m.PendingCurrent = nil
	m.Pipeline = pipeline.Model{}
	return m
}

// ViewConfirm renders screenStabilizerSettingsConfirm.
func (m StabilizerSettingsModel) ViewConfirm() string {
	def := m.PendingDef
	curDisplay := "(not set - chart default)"
	if m.PendingCurrent != nil {
		curDisplay = settings.FormatStabilizerSettingValue(def, m.PendingCurrent)
	}
	s := "\n" + tui.TitleStyle.Render("Confirm Stabilizer Setting Change") + "\n\n"
	s += fmt.Sprintf("%s:  %s -> %s\n\n", def.Key, curDisplay, settings.FormatStabilizerSettingValue(def, m.PendingValue))
	s += "Applying this RESTARTS THE WHOLE RELEASE: stabilizer, vncauthproxy, the\n"
	s += "aileron operator, and the VNC gateway all restart (roughly 30-90 seconds).\n"
	s += "Consoles drop and the zone goes quiet to the cloud UI during that window.\n"
	s += "Running VMs are NOT affected. This is not a hot-reload.\n"
	s += fmt.Sprintf("\n%s\n  %s\n", tui.HelpStyle.Render("Type \"yes\" to proceed, or Esc to cancel:"), m.ConfirmInput.View())
	if m.ConfirmError != "" {
		s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render(m.ConfirmError))
	}
	return s
}

// ViewApply renders screenStabilizerSettingsApply - the running apply
// pipeline itself - via pipeline.Model.View.
func (m StabilizerSettingsModel) ViewApply(visibleLines int) string {
	return m.Pipeline.View(
		fmt.Sprintf("Applying %s...", m.PendingDef.Key),
		tui.SuccessStyle.Render("Applied and rolled out.")+" "+tui.HelpStyle.Render("Press Esc to return to Settings."),
		tui.ErrorStyle.Render("Failed.")+" "+tui.HelpStyle.Render("Press Esc to return to Settings."),
		visibleLines,
	)
}
