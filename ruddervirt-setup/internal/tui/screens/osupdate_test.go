// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"errors"
	"strings"
	"testing"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/tui/pipeline"
)

// TestOSUpdateModelPipelineWiringAdvancesToDone/StopsOnFailure mirror
// TestInstallModelPipelineWiringAdvancesToDone/StopsOnFailure - see
// install_test.go's installStep/drainPipeline helpers (reused here) for why
// these use synthetic steps rather than the real osupdate.OSUpdateSteps.
func TestOSUpdateModelPipelineWiringAdvancesToDone(t *testing.T) {
	steps := []installsteps.Step{installStep("first", nil), installStep("second", nil)}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	m := OSUpdateModel{Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Done {
		t.Fatal("Pipeline.Done = false, want true after both steps succeed")
	}
	out := m.View(10)
	if !strings.Contains(out, "Staged. Reboot to switch into the new deployment.") {
		t.Errorf("View() = %q, want the success message", out)
	}
	if !strings.Contains(out, "Press r to reboot now, or Esc to return to Update.") {
		t.Errorf("View() = %q, want the reboot-now/return-to-Update hint", out)
	}
}

func TestOSUpdateModelPipelineWiringStopsOnFailure(t *testing.T) {
	steps := []installsteps.Step{installStep("first", errors.New("boom"))}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	m := OSUpdateModel{Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Failed {
		t.Fatal("Pipeline.Failed = false, want true after a step errors")
	}
	out := m.View(10)
	if !strings.Contains(out, "Update failed.") {
		t.Errorf("View() = %q, want the failure message", out)
	}
	if !strings.Contains(out, "Press Esc to return to Update.") {
		t.Errorf("View() = %q, want the return-to-Update hint on failure", out)
	}
}

func TestOSUpdateModelViewRunningState(t *testing.T) {
	steps := []installsteps.Step{installStep("first", nil), installStep("second", nil)}
	cfg := config.Config{}
	pl, _ := pipeline.New(steps, cfg)
	m := OSUpdateModel{Pipeline: pl}
	out := m.View(10)
	if !strings.Contains(out, "Updating operating system...") {
		t.Errorf("View() = %q, want the title", out)
	}
	if !strings.Contains(out, "Running: first...") {
		t.Errorf("View() = %q, want the running-step hint", out)
	}
}
