// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/tui/pipeline"
)

// installStep builds a synthetic installsteps.Step that reports one output
// line then finishes (successfully unless err != nil) - deliberately NOT
// the real installSteps var (install_steps.go, package main): those steps'
// Run funcs reach past the exec.CommandRunner seam (e.g.
// network.ApplyNetworkConfig, config.WriteZincatiConfig) and would perform
// real system changes the instant LaunchStep's goroutine starts. A
// synthetic step proves InstallModel wires pipeline.Model/pipeline.New
// correctly without that risk, same as internal/tui/pipeline's own tests.
func installStep(label string, err error) installsteps.Step {
	return installsteps.Step{
		Label: label,
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			ch <- installsteps.StepOutputMsg("running " + label)
			ch <- installsteps.StepDoneMsg{Label: label, Err: err}
		},
	}
}

// drainPipeline runs cmd (and every cmd Update returns) until m reaches a
// terminal state - same drain-before-return pattern internal/tui/pipeline's
// own tests follow, avoiding a leaked goroutine after the test exits.
func drainPipeline(t *testing.T, m pipeline.Model, cmd tea.Cmd, cfg config.Config) pipeline.Model {
	t.Helper()
	for i := 0; ; i++ {
		if m.Done || m.Failed {
			return m
		}
		if cmd == nil {
			t.Fatal("cmd = nil before pipeline reached Done/Failed - stuck")
		}
		if i > 1000 {
			t.Fatal("drainPipeline: too many iterations, pipeline never finished")
		}
		msg := cmd()
		m, cmd = m.Update(msg, cfg)
	}
}

func TestInstallModelViewPlanning(t *testing.T) {
	m := InstallModel{}
	if out := m.ViewPlanning(); !strings.Contains(out, "Computing install plan") {
		t.Errorf("ViewPlanning() = %q, want it to mention computing the plan", out)
	}
}

func TestInstallModelViewConfirm(t *testing.T) {
	steps := []installsteps.Step{installStep("Step one", nil), installStep("Step two", nil)}
	m := InstallModel{
		PlanLines:    []string{"will do X", ""},
		ConfirmInput: textinput.New(),
		ConfirmError: "boom",
	}
	out := m.ViewConfirm(steps)
	if !strings.Contains(out, "Step one") || !strings.Contains(out, "Step two") {
		t.Errorf("ViewConfirm() = %q, want both step labels", out)
	}
	if !strings.Contains(out, "will do X") {
		t.Errorf("ViewConfirm() = %q, want the first step's plan line", out)
	}
	if !strings.Contains(out, "will run") {
		t.Errorf("ViewConfirm() = %q, want the second step's default \"will run\" (empty plan line)", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("ViewConfirm() = %q, want the confirm error shown", out)
	}
}

// TestInstallModelPipelineWiringAdvancesToDone exercises pipeline.New/
// pipeline.Model.Update exactly the way app.go wires them into
// InstallModel.Pipeline on screenInstallConfirm's "yes" press, using
// synthetic steps - confirming the plumbing advances a pipeline to Done
// end-to-end.
func TestInstallModelPipelineWiringAdvancesToDone(t *testing.T) {
	steps := []installsteps.Step{installStep("first", nil), installStep("second", nil)}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	m := InstallModel{ConfirmInput: textinput.New(), Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Done {
		t.Fatal("Pipeline.Done = false, want true after both steps succeed")
	}
	out := m.ViewInstall(10)
	if !strings.Contains(out, "Install complete.") {
		t.Errorf("ViewInstall() = %q, want the success message", out)
	}
	if !strings.Contains(out, "Press Esc to return to menu.") {
		t.Errorf("ViewInstall() = %q, want the return-to-menu hint", out)
	}
}

// TestInstallModelPipelineWiringStopsOnFailure mirrors the above for a
// failing step.
func TestInstallModelPipelineWiringStopsOnFailure(t *testing.T) {
	steps := []installsteps.Step{installStep("first", errors.New("boom"))}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	m := InstallModel{ConfirmInput: textinput.New(), Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Failed {
		t.Fatal("Pipeline.Failed = false, want true after a step errors")
	}
	out := m.ViewInstall(10)
	if !strings.Contains(out, "Install failed.") {
		t.Errorf("ViewInstall() = %q, want the failure message", out)
	}
}

func TestInstallModelUpdateForwardsToConfirmInput(t *testing.T) {
	m := InstallModel{ConfirmInput: textinput.New()}
	m.ConfirmInput.Focus()

	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("yes")})
	if got.ConfirmInput.Value() != "yes" {
		t.Fatalf("ConfirmInput.Value() = %q, want %q - typing must forward to ConfirmInput", got.ConfirmInput.Value(), "yes")
	}
}

func TestInstallModelResetClearsPlanConfirmAndPipeline(t *testing.T) {
	m := InstallModel{
		PlanLines:    []string{"will run", "already applied"},
		ConfirmInput: textinput.New(),
		ConfirmError: `Type "yes" to proceed, or Esc to cancel.`,
		Pipeline:     pipeline.Model{Done: true},
	}
	m.ConfirmInput.SetValue("yes")
	m.ConfirmInput.Focus()

	got := m.Reset()

	if got.PlanLines != nil {
		t.Errorf("PlanLines = %v, want nil", got.PlanLines)
	}
	if got.ConfirmInput.Value() != "" || got.ConfirmInput.Focused() {
		t.Errorf("ConfirmInput = %q/focused=%v, want cleared and blurred", got.ConfirmInput.Value(), got.ConfirmInput.Focused())
	}
	if got.ConfirmError != "" {
		t.Errorf("ConfirmError = %q, want cleared", got.ConfirmError)
	}
	if got.Pipeline.Done || got.Pipeline.Steps != nil {
		t.Errorf("Pipeline = %+v, want the zero value", got.Pipeline)
	}
}
