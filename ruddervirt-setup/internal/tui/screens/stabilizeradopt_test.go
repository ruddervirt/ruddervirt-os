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

func TestStabilizerAdoptModelViewAileronCheck(t *testing.T) {
	if out := (StabilizerAdoptModel{}).ViewAileronCheck(); !strings.Contains(out, "Checking that aileron") {
		t.Errorf("ViewAileronCheck() = %q, want it to mention checking aileron", out)
	}
}

func TestStabilizerAdoptModelViewWarning(t *testing.T) {
	out := (StabilizerAdoptModel{}).ViewWarning()
	if !strings.Contains(out, "selfhosted@ruddervirt.com") {
		t.Errorf("ViewWarning() = %q, want it to mention selfhosted@ruddervirt.com", out)
	}
}

func TestStabilizerAdoptModelViewField(t *testing.T) {
	input := textinput.New()
	input.SetValue("zone-a")

	t.Run("normal", func(t *testing.T) {
		m := StabilizerAdoptModel{Error: "boom"}
		out := m.ViewField("Zone Name", "Zone name", "help text\n\n", input)
		if !strings.Contains(out, "Zone Name") || !strings.Contains(out, "zone-a") {
			t.Errorf("ViewField() = %q, want title and input value", out)
		}
		if !strings.Contains(out, "boom") {
			t.Errorf("ViewField() = %q, want the error shown", out)
		}
	})

	t.Run("resolving takes priority over a stale error", func(t *testing.T) {
		m := StabilizerAdoptModel{NebulaResolving: true, Error: "stale"}
		out := m.ViewField("Nebula Mesh Config", "Path or URL", "", input)
		if !strings.Contains(out, "Fetching...") {
			t.Errorf("ViewField() = %q, want the fetching indicator", out)
		}
	})
}

func TestStabilizerAdoptModelViewPlanning(t *testing.T) {
	if out := (StabilizerAdoptModel{}).ViewPlanning(); !strings.Contains(out, "Computing adoption plan") {
		t.Errorf("ViewPlanning() = %q, want it to mention computing the plan", out)
	}
}

func TestStabilizerAdoptModelViewConfirm(t *testing.T) {
	confirmInput := textinput.New()

	t.Run("adopting an existing standalone release", func(t *testing.T) {
		m := StabilizerAdoptModel{WillAdopt: true, ConfirmInput: confirmInput, ConfirmError: "boom"}
		out := m.ViewConfirm("zone-a")
		if !strings.Contains(out, "zone-a") {
			t.Errorf("ViewConfirm() = %q, want the zone shown", out)
		}
		if !strings.Contains(out, "adopt the existing standalone Aileron release") {
			t.Errorf("ViewConfirm() = %q, want the adopt-existing copy", out)
		}
		if !strings.Contains(out, "boom") {
			t.Errorf("ViewConfirm() = %q, want the confirm error shown", out)
		}
	})

	t.Run("installing fresh", func(t *testing.T) {
		m := StabilizerAdoptModel{WillAdopt: false, ConfirmInput: confirmInput}
		out := m.ViewConfirm("zone-a")
		if !strings.Contains(out, "install stabilizer fresh") {
			t.Errorf("ViewConfirm() = %q, want the fresh-install copy", out)
		}
	})
}

func stabilizerAdoptModelWithEverythingSet() StabilizerAdoptModel {
	m := StabilizerAdoptModel{
		ZoneInput:         textinput.New(),
		NatsPasswordInput: textinput.New(),
		NebulaInput:       textinput.New(),
		NebulaResolving:   true,
		NebulaContent:     "nebula config bytes",
		Error:             "boom",
		WillAdopt:         true,
		ConfirmInput:      textinput.New(),
		ConfirmError:      `Type "yes" to proceed, or Esc to cancel.`,
		Pipeline:          pipeline.Model{Done: true},
	}
	m.ZoneInput.SetValue("zone-a")
	m.ZoneInput.Focus()
	m.NatsPasswordInput.SetValue("hunter2")
	m.NatsPasswordInput.Focus()
	m.NebulaInput.SetValue("https://example.com/nebula.yaml")
	m.NebulaInput.Focus()
	m.ConfirmInput.SetValue("yes")
	m.ConfirmInput.Focus()
	return m
}

func TestStabilizerAdoptModelClearInputsLeavesWillAdoptAndPipeline(t *testing.T) {
	got := stabilizerAdoptModelWithEverythingSet().ClearInputs()

	if got.ZoneInput.Value() != "" || got.ZoneInput.Focused() {
		t.Errorf("ZoneInput = %q/focused=%v, want cleared and blurred", got.ZoneInput.Value(), got.ZoneInput.Focused())
	}
	if got.NatsPasswordInput.Value() != "" || got.NatsPasswordInput.Focused() {
		t.Errorf("NatsPasswordInput = %q/focused=%v, want cleared and blurred", got.NatsPasswordInput.Value(), got.NatsPasswordInput.Focused())
	}
	if got.NebulaInput.Value() != "" || got.NebulaInput.Focused() {
		t.Errorf("NebulaInput = %q/focused=%v, want cleared and blurred", got.NebulaInput.Value(), got.NebulaInput.Focused())
	}
	if got.NebulaResolving {
		t.Error("NebulaResolving = true, want false")
	}
	if got.NebulaContent != "" {
		t.Errorf("NebulaContent = %q, want cleared", got.NebulaContent)
	}
	if got.ConfirmInput.Value() != "" || got.ConfirmInput.Focused() {
		t.Errorf("ConfirmInput = %q/focused=%v, want cleared and blurred", got.ConfirmInput.Value(), got.ConfirmInput.Focused())
	}
	if got.ConfirmError != "" {
		t.Errorf("ConfirmError = %q, want cleared", got.ConfirmError)
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want cleared", got.Error)
	}
	// ClearInputs deliberately leaves these two alone - see its doc comment.
	if !got.WillAdopt {
		t.Error("WillAdopt = false, want left untouched (true)")
	}
	if !got.Pipeline.Done {
		t.Error("Pipeline.Done = false, want left untouched (true)")
	}
}

func TestStabilizerAdoptModelResetClearsWillAdoptAndPipelineToo(t *testing.T) {
	got := stabilizerAdoptModelWithEverythingSet().Reset()

	if got.WillAdopt {
		t.Error("WillAdopt = true, want false")
	}
	if got.Pipeline.Done {
		t.Error("Pipeline.Done = true, want the zero-value pipeline")
	}
	if got.ZoneInput.Value() != "" {
		t.Errorf("ZoneInput.Value() = %q, want cleared", got.ZoneInput.Value())
	}
}

// TestStabilizerAdoptModelPipelineWiringAdvancesToDone/StopsOnFailure mirror
// TestInstallModelPipelineWiringAdvancesToDone/StopsOnFailure
// (install_test.go) - see its installStep/drainPipeline doc comments for
// why these use synthetic steps rather than the real stabilizer.AdoptSteps.
func TestStabilizerAdoptModelPipelineWiringAdvancesToDone(t *testing.T) {
	steps := []installsteps.Step{installStep("first", nil), installStep("second", nil)}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	m := StabilizerAdoptModel{Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Done {
		t.Fatal("Pipeline.Done = false, want true after both steps succeed")
	}
	out := m.ViewAdopt(10)
	if !strings.Contains(out, "This node is now connected to ruddervirt.com.") {
		t.Errorf("ViewAdopt() = %q, want the success message", out)
	}
}

func TestStabilizerAdoptModelPipelineWiringStopsOnFailure(t *testing.T) {
	steps := []installsteps.Step{installStep("first", errors.New("boom"))}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	m := StabilizerAdoptModel{Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Failed {
		t.Fatal("Pipeline.Failed = false, want true after a step errors")
	}
	out := m.ViewAdopt(10)
	if !strings.Contains(out, "Adoption failed.") {
		t.Errorf("ViewAdopt() = %q, want the failure message", out)
	}
}
