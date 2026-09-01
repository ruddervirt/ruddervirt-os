// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"

	"ruddervirt-setup/internal/stabilizer"
	"ruddervirt-setup/internal/tui"
	"ruddervirt-setup/internal/tui/pipeline"
)

// StabilizerAdoptModel is the "Adopt to ruddervirt.com" wizard's sub-model
// (screenStabilizerAileronCheck through screenStabilizerAdopt) - the
// zone/NATS-password/Nebula-config input sequence, the plan/confirm step,
// and the adopt pipeline itself (stabilizer.AdoptSteps). The third screen
// group to wire in internal/tui/pipeline's generic step-runner: Pipeline
// backs screenStabilizerAdopt exactly the way InstallModel.Pipeline backs
// screenInstall, for stabilizer.AdoptSteps instead of installSteps.
//
// Deliberately holds NO Update method of its own (unlike
// InstallModel/UpdateModel, which each forward to a single ConfirmInput):
// this wizard has four separate input fields, each active on a different
// screen (ZoneInput/NatsPasswordInput/NebulaInput/ConfirmInput), so app.go
// forwards key presses to whichever one is active directly against this
// struct's fields rather than through an ambiguous single Update method.
// The sequential Enter-key dispatch between this wizard's own screens
// (validate zone -> NATS password -> resolve Nebula config -> plan ->
// confirm -> launch) is group-local logic, but per convention (Enter
// dispatch is an app.go responsibility, same as SettingsModel/UpdateModel)
// it stays there too, called directly against this struct's fields.
//
// stabilizer.SetPendingSecrets(natsUser, natsPassword, nebulaConfig) is
// called by app.go right before launching Pipeline via pipeline.New: the
// secret values (NatsPasswordInput's value, NebulaContent) flow through
// SetPendingSecrets, never stored elsewhere on this struct beyond their own
// input fields, and never threaded through config.Config.
type StabilizerAdoptModel struct {
	ZoneInput         textinput.Model
	NatsPasswordInput textinput.Model
	NebulaInput       textinput.Model
	NebulaResolving   bool
	// NebulaContent holds the fetched-and-validated Nebula config only
	// until screenStabilizerConfirm launches Pipeline, at which point it's
	// handed to stabilizer.SetPendingSecrets and cleared - never persisted
	// to config.Config.
	NebulaContent string
	Error         string

	// WillAdopt carries computeStabilizerPlanCmd's result
	// (stabilizer_bridge.go) into screenStabilizerConfirm's plan summary.
	WillAdopt    bool
	ConfirmInput textinput.Model
	ConfirmError string

	Pipeline pipeline.Model
}

// ClearInputs blanks and blurs every input field (ZoneInput,
// NatsPasswordInput, NebulaInput, ConfirmInput), plus the transient
// NebulaResolving/NebulaContent/Error/ConfirmError state that goes with
// them, leaving WillAdopt/Pipeline untouched. app.go calls it when Esc
// cancels straight back to Settings from any of this wizard's
// pre-execution screens (one-way, single-shot entries - see
// isStabilizerWizardScreen's doc comment - so no "step back one field" to
// support).
func (m StabilizerAdoptModel) ClearInputs() StabilizerAdoptModel {
	m.ZoneInput.SetValue("")
	m.ZoneInput.Blur()
	m.NatsPasswordInput.SetValue("")
	m.NatsPasswordInput.Blur()
	m.NebulaInput.SetValue("")
	m.NebulaInput.Blur()
	m.NebulaResolving = false
	m.NebulaContent = ""
	m.ConfirmInput.SetValue("")
	m.ConfirmInput.Blur()
	m.ConfirmError = ""
	m.Error = ""
	return m
}

// Reset clears every field back to its zero value, including WillAdopt and
// Pipeline (both left untouched by ClearInputs above) - called by app.go's
// generic cross-group Esc handler when abandoning a flow back to
// screenMenu.
func (m StabilizerAdoptModel) Reset() StabilizerAdoptModel {
	m = m.ClearInputs()
	m.WillAdopt = false
	m.Pipeline = pipeline.Model{}
	return m
}

// ViewAileronCheck renders screenStabilizerAileronCheck. Holds no state of
// its own (the aileron-readiness check is a brief background step, same as
// InstallModel.ViewPlanning); kept as a method so every screen in this
// group renders from the same place.
func (m StabilizerAdoptModel) ViewAileronCheck() string {
	return "\nChecking that aileron is installed and running...\n"
}

// ViewWarning renders screenStabilizerWarning.
func (m StabilizerAdoptModel) ViewWarning() string {
	s := "\n" + tui.TitleStyle.Render("Adopt to ruddervirt.com") + "\n\n"
	s += tui.ErrorStyle.Render("This cannot be done without coordination from selfhosted@ruddervirt.com.") + "\n\n"
	s += "ruddervirt needs to provide secrets (zone name, NATS password, and a\n"
	s += "Nebula mesh config) before this will work - contact\n"
	s += "selfhosted@ruddervirt.com first if you haven't already.\n"
	s += "\n" + tui.HintBar([2]string{"Enter", "continue"}, [2]string{"Esc", "cancel"}) + "\n"
	return s
}

// ViewField renders screenStabilizerZone/NatsPassword/Nebula.
// title/label/help are computed by the caller (stabilizerFieldScreenInfo,
// which needs the package-main-only screen enum to pick between the
// three); input is passed in too, for the same reason.
func (m StabilizerAdoptModel) ViewField(title, label, help string, input textinput.Model) string {
	s := "\n" + tui.TitleStyle.Render(title) + "\n\n"
	s += help
	s += fmt.Sprintf("%s:  %s\n", label, input.View())
	if m.NebulaResolving {
		s += "\n" + tui.HelpStyle.Render("Fetching...") + "\n"
	} else if m.Error != "" {
		s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render("Error: "+m.Error))
	}
	s += "\n" + tui.HintBar([2]string{"Enter", "continue"}, [2]string{"Esc", "cancel"}) + "\n"
	return s
}

// ViewPlanning renders screenStabilizerPlanning. Holds no state of its own,
// same as ViewAileronCheck above.
func (m StabilizerAdoptModel) ViewPlanning() string {
	return "\nComputing adoption plan...\n"
}

// ViewConfirm renders screenStabilizerConfirm. zone is
// m.cfg.Stabilizer.Zone, passed in rather than the whole config.Config
// since that's the only field this needs.
func (m StabilizerAdoptModel) ViewConfirm(zone string) string {
	s := "\n" + tui.TitleStyle.Render("Confirm Adoption to ruddervirt.com") + "\n\n"
	s += fmt.Sprintf("Zone: %s\nNATS: %s\nNATS user: %s (= zone name)\n\n", zone, stabilizer.DefaultStabilizerNatsURL, zone)
	s += fmt.Sprintf("This will create the %s and %s Secrets in %s, then:\n", stabilizer.NatsAuthSecretName, stabilizer.NebulaSecretName, stabilizer.StabilizerNamespace)
	if m.WillAdopt {
		s += "  adopt the existing standalone Aileron release - its operator,\n"
		s += "  vncgateway, and UI Deployments will be deleted and recreated\n"
		s += "  (a few seconds of console/API disruption; running VMs are\n"
		s += "  unaffected).\n"
	} else {
		s += "  install stabilizer fresh (no standalone Aileron release was found\n"
		s += "  to adopt).\n"
	}
	s += "then apply the stabilizer chart and wait for it to become ready.\n"
	s += fmt.Sprintf("\n%s\n  %s\n", tui.HelpStyle.Render("Type \"yes\" to proceed, or Esc to cancel:"), m.ConfirmInput.View())
	if m.ConfirmError != "" {
		s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render(m.ConfirmError))
	}
	return s
}

// ViewAdopt renders screenStabilizerAdopt - the running adopt pipeline
// itself - via pipeline.Model.View.
func (m StabilizerAdoptModel) ViewAdopt(visibleLines int) string {
	return m.Pipeline.View(
		"Adopting to ruddervirt.com...",
		tui.SuccessStyle.Render("This node is now connected to ruddervirt.com.")+" "+tui.HelpStyle.Render("Press Esc to return to menu."),
		tui.ErrorStyle.Render("Adoption failed.")+" "+tui.HelpStyle.Render("Press Esc to return to menu."),
		visibleLines,
	)
}
