// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/tui"
	"ruddervirt-setup/internal/tui/pipeline"
)

// InstallModel is the install wizard's sub-model (screenInstall,
// screenInstallPlanning, screenInstallConfirm) - the first screen group to
// wire in internal/tui/pipeline's generic step-runner: PlanLines/
// ConfirmInput/ConfirmError back the two pre-execution screens, and
// Pipeline is the step-runner itself once the operator confirms.
//
// Reached from BOTH the Settings screen's Apply row and the Update
// screen's Apply-upgrades row - which one to return to on Esc
// (installConfirmOrigin) is cross-group routing and stays on the root
// Model (see package doc). The confirm screen's own rendering/yes-no
// handling is install-group-local (same UI regardless of origin); only
// "which screen to return to" is cross-group.
type InstallModel struct {
	// PlanLines carries computeInstallPlanCmd's per-step dry-run preview
	// (one line per installSteps entry, same order/index) into
	// ViewConfirm's plan summary.
	PlanLines    []string
	ConfirmInput textinput.Model
	ConfirmError string
	Pipeline     pipeline.Model
}

// Update forwards key presses to ConfirmInput. The yes/no submit logic
// (launching the pipeline via pipeline.New, which needs
// installSteps/config.Config) and confirm/cancel Esc handling stay in
// app.go, called directly against this struct's fields - same convention
// as SettingsModel.
func (m InstallModel) Update(msg tea.Msg) (InstallModel, tea.Cmd) {
	var cmd tea.Cmd
	m.ConfirmInput, cmd = m.ConfirmInput.Update(msg)
	return m, cmd
}

// Reset clears PlanLines/ConfirmInput/ConfirmError/Pipeline back to their
// zero values. app.go calls this both when screenInstallConfirm is
// cancelled back to installConfirmOrigin (Pipeline is already zero there)
// and from the generic cross-group Esc handler that abandons any flow back
// to screenMenu.
func (m InstallModel) Reset() InstallModel {
	m.PlanLines = nil
	m.ConfirmInput.SetValue("")
	m.ConfirmInput.Blur()
	m.ConfirmError = ""
	m.Pipeline = pipeline.Model{}
	return m
}

// ViewPlanning renders screenInstallPlanning. Holds no state of its own
// (computing the plan is a brief background step - see
// computeInstallPlanCmd in install_steps.go); kept as a method so every
// screen in this group renders from the same place.
func (m InstallModel) ViewPlanning() string {
	return "\nComputing install plan...\n"
}

// ViewConfirm renders screenInstallConfirm. steps is installSteps
// (install_steps.go), passed in rather than imported since
// internal/installsteps.Step is this package's dependency-free type but
// the actual install pipeline var still lives in package main.
func (m InstallModel) ViewConfirm(steps []installsteps.Step) string {
	s := "\n" + tui.TitleStyle.Render("Confirm Install") + "\n\n"
	s += "This will restart k3s, causing a brief interruption to the\n"
	s += "Kubernetes API and any running workloads. The full process can\n"
	s += "take 30+ minutes, mostly waiting for storage to become ready.\n"
	s += "\nPlan:\n"
	labelWidth := 0
	for _, step := range steps {
		if l := runewidth.StringWidth(step.Label); l > labelWidth {
			labelWidth = l
		}
	}
	for i, step := range steps {
		line := "will run"
		if i < len(m.PlanLines) && m.PlanLines[i] != "" {
			line = m.PlanLines[i]
		}
		if line == "will run" {
			line = tui.HelpStyle.Render(line)
		}
		s += fmt.Sprintf("  %s  %s\n", fitCell(step.Label, labelWidth), line)
	}
	s += fmt.Sprintf("\n%s\n  %s\n", tui.HelpStyle.Render("Type \"yes\" to proceed, or Esc to cancel:"), m.ConfirmInput.View())
	if m.ConfirmError != "" {
		s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render(m.ConfirmError))
	}
	return s
}

// ViewInstall renders screenInstall - the running pipeline itself - via
// pipeline.Model.View. doneMsg/failedMsg are pre-styled (green outcome +
// muted "Press Esc to return to menu." hint) - see pipeline.Model.View's
// doc comment for why.
func (m InstallModel) ViewInstall(visibleLines int) string {
	return m.Pipeline.View(
		"Installing RudderVirt...",
		tui.SuccessStyle.Render("Install complete.")+" "+tui.HelpStyle.Render("Press Esc to return to menu."),
		tui.ErrorStyle.Render("Install failed.")+" "+tui.HelpStyle.Render("Press Esc to return to menu."),
		visibleLines,
	)
}
