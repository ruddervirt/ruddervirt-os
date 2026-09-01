// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/tui/pipeline"
)

func TestStabilizerVersionModelViewConfirm(t *testing.T) {
	confirmInput := textinput.New()

	t.Run("with cleared pins", func(t *testing.T) {
		m := StabilizerVersionModel{
			Target:       "1.3.0",
			ClearedPins:  []string{"aileron.image.tag"},
			ConfirmInput: confirmInput,
			ConfirmError: "boom",
		}
		out := m.ViewConfirm("1.2.3")
		if !strings.Contains(out, "1.2.3 -> 1.3.0") {
			t.Errorf("ViewConfirm() = %q, want the current -> target line", out)
		}
		if !strings.Contains(out, "aileron.image.tag") {
			t.Errorf("ViewConfirm() = %q, want the cleared pin listed", out)
		}
		if !strings.Contains(out, "boom") {
			t.Errorf("ViewConfirm() = %q, want the confirm error shown", out)
		}
	})

	t.Run("no cleared pins", func(t *testing.T) {
		m := StabilizerVersionModel{Target: "1.3.0", ConfirmInput: confirmInput}
		out := m.ViewConfirm("(unset)")
		if strings.Contains(out, "redundant image pins") {
			t.Errorf("ViewConfirm() = %q, must not mention cleared pins when there are none", out)
		}
	})
}

func TestStabilizerVersionModelClearConfirmLeavesTargetPatchAndPipeline(t *testing.T) {
	m := StabilizerVersionModel{
		Target:       "1.3.0",
		Patch:        []byte("patch bytes"),
		ClearedPins:  []string{"aileron.image.tag"},
		ConfirmInput: textinput.New(),
		ConfirmError: `Type "yes" to proceed, or Esc to cancel.`,
		Pipeline:     pipeline.Model{Done: true},
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
	if got.Target != "1.3.0" || string(got.Patch) != "patch bytes" || len(got.ClearedPins) != 1 {
		t.Errorf("Target/Patch/ClearedPins = %q/%q/%v, want left untouched", got.Target, got.Patch, got.ClearedPins)
	}
	if !got.Pipeline.Done {
		t.Error("Pipeline.Done = false, want left untouched (true)")
	}
}

func TestStabilizerVersionModelResetClearsTargetPatchAndPipelineToo(t *testing.T) {
	m := StabilizerVersionModel{
		Target:       "1.3.0",
		Patch:        []byte("patch bytes"),
		ClearedPins:  []string{"aileron.image.tag"},
		ConfirmInput: textinput.New(),
		Pipeline:     pipeline.Model{Done: true},
	}

	got := m.Reset()

	if got.Target != "" {
		t.Errorf("Target = %q, want cleared", got.Target)
	}
	if got.Patch != nil {
		t.Errorf("Patch = %v, want nil", got.Patch)
	}
	if got.ClearedPins != nil {
		t.Errorf("ClearedPins = %v, want nil", got.ClearedPins)
	}
	if got.Pipeline.Done {
		t.Error("Pipeline.Done = true, want the zero-value pipeline")
	}
}

// TestStabilizerVersionModelPipelineWiringAdvancesToDone/StopsOnFailure
// mirror TestInstallModelPipelineWiringAdvancesToDone/StopsOnFailure
// (install_test.go) - see its installStep/drainPipeline doc comments for
// why these use synthetic steps rather than the real
// stabilizerVersionApplySteps (which shells out via kubectl).
func TestStabilizerVersionModelPipelineWiringAdvancesToDone(t *testing.T) {
	steps := []installsteps.Step{installStep("first", nil), installStep("second", nil)}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	m := StabilizerVersionModel{Target: "1.3.0", Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Done {
		t.Fatal("Pipeline.Done = false, want true after both steps succeed")
	}
	out := m.ViewApply(10)
	if !strings.Contains(out, "Applied and rolled out.") {
		t.Errorf("ViewApply() = %q, want the success message", out)
	}
	if !strings.Contains(out, "Press Esc to return to Update.") {
		t.Errorf("ViewApply() = %q, want the return-to-Update hint", out)
	}
}

func TestStabilizerVersionModelPipelineWiringStopsOnFailure(t *testing.T) {
	steps := []installsteps.Step{installStep("first", errors.New("boom"))}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	m := StabilizerVersionModel{Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Failed {
		t.Fatal("Pipeline.Failed = false, want true after a step errors")
	}
	out := m.ViewApply(10)
	if !strings.Contains(out, "Failed.") {
		t.Errorf("ViewApply() = %q, want the failure message", out)
	}
}
