// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/stabilizer/settings"
	"ruddervirt-setup/internal/tui/pipeline"
)

func TestStabilizerSettingsModelViewConfirm(t *testing.T) {
	def, ok := settings.StabilizerSettingByKey("build_max_cpu")
	if !ok {
		t.Fatal("build_max_cpu not found in settings.StabilizerSettingDefs")
	}
	confirmInput := textinput.New()

	t.Run("known current value", func(t *testing.T) {
		m := StabilizerSettingsModel{
			PendingDef:     def,
			PendingValue:   16,
			PendingCurrent: 8,
			ConfirmInput:   confirmInput,
			ConfirmError:   "boom",
		}
		out := m.ViewConfirm()
		if !strings.Contains(out, "build_max_cpu") || !strings.Contains(out, "8") || !strings.Contains(out, "16") {
			t.Errorf("ViewConfirm() = %q, want the key and both values shown", out)
		}
		if !strings.Contains(out, "boom") {
			t.Errorf("ViewConfirm() = %q, want the confirm error shown", out)
		}
	})

	t.Run("no current value falls back to chart-default copy", func(t *testing.T) {
		m := StabilizerSettingsModel{PendingDef: def, PendingValue: 16, ConfirmInput: confirmInput}
		out := m.ViewConfirm()
		if !strings.Contains(out, "chart default") {
			t.Errorf("ViewConfirm() = %q, want the chart-default fallback", out)
		}
	})
}

func TestStabilizerSettingsModelClearConfirmLeavesPendingAndPipeline(t *testing.T) {
	def, _ := settings.StabilizerSettingByKey("build_max_cpu")
	m := StabilizerSettingsModel{
		PendingDef:     def,
		PendingValue:   16,
		PendingCurrent: 8,
		ConfirmInput:   textinput.New(),
		ConfirmError:   `Type "yes" to proceed, or Esc to cancel.`,
		Pipeline:       pipeline.Model{Done: true},
	}
	m.ConfirmInput.SetValue("yes")
	m.ConfirmInput.Focus()

	got := m.ClearConfirm()

	if got.ConfirmInput.Value() != "" || got.ConfirmInput.Focused() {
		t.Errorf("ConfirmInput = %q/focused=%v, want cleared and blurred", got.ConfirmInput.Value(), got.ConfirmInput.Focused())
	}
	if got.ConfirmError != "" {
		t.Errorf("ConfirmError = %q, want cleared", got.ConfirmError)
	}
	if got.PendingDef.Key != def.Key || got.PendingValue != 16 || got.PendingCurrent != 8 {
		t.Errorf("Pending* = %+v/%v/%v, want left untouched", got.PendingDef, got.PendingValue, got.PendingCurrent)
	}
	if !got.Pipeline.Done {
		t.Error("Pipeline.Done = false, want left untouched (true)")
	}
}

func TestStabilizerSettingsModelResetClearsPendingAndPipelineToo(t *testing.T) {
	def, _ := settings.StabilizerSettingByKey("build_max_cpu")
	m := StabilizerSettingsModel{
		PendingDef:     def,
		PendingValue:   16,
		PendingCurrent: 8,
		ConfirmInput:   textinput.New(),
		Pipeline:       pipeline.Model{Done: true},
	}

	got := m.Reset()

	if got.PendingDef.Key != "" {
		t.Errorf("PendingDef = %+v, want the zero value", got.PendingDef)
	}
	if got.PendingValue != nil {
		t.Errorf("PendingValue = %v, want nil", got.PendingValue)
	}
	if got.PendingCurrent != nil {
		t.Errorf("PendingCurrent = %v, want nil", got.PendingCurrent)
	}
	if got.Pipeline.Done {
		t.Error("Pipeline.Done = true, want the zero-value pipeline")
	}
}

// TestStabilizerSettingsModelPipelineWiringAdvancesToDone/StopsOnFailure
// mirror TestInstallModelPipelineWiringAdvancesToDone/StopsOnFailure
// (install_test.go) - see its installStep/drainPipeline doc comments for
// why these use synthetic steps rather than the real
// stabilizerSettingsApplySteps (which shells out via kubectl).
func TestStabilizerSettingsModelPipelineWiringAdvancesToDone(t *testing.T) {
	steps := []installsteps.Step{installStep("first", nil), installStep("second", nil)}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	def, _ := settings.StabilizerSettingByKey("build_max_cpu")
	m := StabilizerSettingsModel{PendingDef: def, Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Done {
		t.Fatal("Pipeline.Done = false, want true after both steps succeed")
	}
	out := m.ViewApply(10)
	if !strings.Contains(out, "Applied and rolled out.") {
		t.Errorf("ViewApply() = %q, want the success message", out)
	}
	if !strings.Contains(out, "Press Esc to return to Settings.") {
		t.Errorf("ViewApply() = %q, want the return-to-Settings hint", out)
	}
}

func TestStabilizerSettingsModelPipelineWiringStopsOnFailure(t *testing.T) {
	steps := []installsteps.Step{installStep("first", errors.New("boom"))}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	m := StabilizerSettingsModel{Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Failed {
		t.Fatal("Pipeline.Failed = false, want true after a step errors")
	}
	out := m.ViewApply(10)
	if !strings.Contains(out, "Failed.") {
		t.Errorf("ViewApply() = %q, want the failure message", out)
	}
}
