// SPDX-License-Identifier: GPL-3.0-only

package pipeline

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
)

// drain runs cmd (and every cmd it returns via Update) until the pipeline
// reaches Done/Failed, failing loudly instead of hanging if the message
// stream dries up first. Mirrors app_test.go's confirm-screen pattern:
// without draining, a test that only reads the first message can leave its
// goroutine reading a channel after the test exits, racing a later test's
// exec.DefaultRunner swap.
func drain(t *testing.T, m Model, cmd tea.Cmd, cfg config.Config) Model {
	t.Helper()
	for i := 0; ; i++ {
		if m.Done || m.Failed {
			return m
		}
		if cmd == nil {
			t.Fatal("cmd = nil before pipeline reached Done/Failed - stuck")
		}
		if i > 1000 {
			t.Fatal("drain: too many iterations, pipeline never finished")
		}
		msg := cmd()
		m, cmd = m.Update(msg, cfg)
	}
}

func step(label string, err error) installsteps.Step {
	return installsteps.Step{
		Label: label,
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			ch <- installsteps.StepOutputMsg("running " + label)
			ch <- installsteps.StepDoneMsg{Label: label, Err: err}
		},
	}
}

func TestNewEmptyStepsIsImmediatelyDone(t *testing.T) {
	m, cmd := New(nil, config.Config{})
	if !m.Done {
		t.Fatal("Done = false, want true for an empty pipeline")
	}
	if cmd != nil {
		t.Fatal("cmd != nil, want nil for an empty pipeline")
	}
}

func TestPipelineAdvancesThroughAllStepsToDone(t *testing.T) {
	steps := []installsteps.Step{step("first", nil), step("second", nil), step("third", nil)}
	m, cmd := New(steps, config.Config{})

	m = drain(t, m, cmd, config.Config{})

	if !m.Done {
		t.Fatal("Done = false, want true after every step succeeds")
	}
	if m.Failed {
		t.Fatal("Failed = true, want false")
	}
	if m.Idx != len(steps) {
		t.Fatalf("Idx = %d, want %d (one past the last step)", m.Idx, len(steps))
	}
	wantLogs := []string{
		"running first", "✓ first",
		"running second", "✓ second",
		"running third", "✓ third",
	}
	if len(m.Logs) != len(wantLogs) {
		t.Fatalf("Logs = %v, want %v", m.Logs, wantLogs)
	}
	for i, w := range wantLogs {
		if m.Logs[i] != w {
			t.Errorf("Logs[%d] = %q, want %q", i, m.Logs[i], w)
		}
	}
}

func TestPipelineStopsAndMarksFailedOnStepError(t *testing.T) {
	wantErr := errors.New("boom")
	steps := []installsteps.Step{step("first", nil), step("second", wantErr), step("third", nil)}
	m, cmd := New(steps, config.Config{})

	m = drain(t, m, cmd, config.Config{})

	if !m.Failed {
		t.Fatal("Failed = false, want true after a step errors")
	}
	if m.Done {
		t.Fatal("Done = true, want false - a failed pipeline never reaches Done")
	}
	if m.Idx != 1 {
		t.Fatalf("Idx = %d, want 1 - must not advance past the failed step", m.Idx)
	}
	last := m.Logs[len(m.Logs)-1]
	if last != "✗ second: boom" {
		t.Fatalf("last log = %q, want %q", last, "✗ second: boom")
	}
	// "third" must never have run.
	for _, l := range m.Logs {
		if l == "running third" {
			t.Fatal("step \"third\" ran after \"second\" failed - pipeline must stop, not continue")
		}
	}
}

func TestUpdateIsNoOpOnceTerminal(t *testing.T) {
	m := Model{Done: true, Logs: []string{"✓ first"}}
	got, cmd := m.Update(installsteps.StepOutputMsg("late output"), config.Config{})
	if cmd != nil {
		t.Fatal("cmd != nil, want nil - a Done pipeline must not keep reading a channel")
	}
	if len(got.Logs) != 1 {
		t.Fatalf("Logs = %v, want unchanged (late messages after Done are dropped)", got.Logs)
	}
}

func TestViewRendersRunningDoneAndFailed(t *testing.T) {
	steps := []installsteps.Step{step("only", nil)}

	running := Model{Steps: steps, Idx: 0}
	if out := running.View("Title", "done", "failed", 10); out == "" {
		t.Fatal("View() = \"\", want non-empty output while running")
	}

	done := Model{Steps: steps, Idx: 1, Done: true, Logs: []string{"✓ only"}}
	out := done.View("Title", "All finished.", "failed", 10)
	if !strings.Contains(out, "All finished.") {
		t.Errorf("View() = %q, want it to contain the done message", out)
	}

	failed := Model{Steps: steps, Idx: 0, Failed: true, Logs: []string{"✗ only: boom"}}
	out = failed.View("Title", "done", "It broke.", 10)
	if !strings.Contains(out, "It broke.") {
		t.Errorf("View() = %q, want it to contain the failed message", out)
	}
}

// TestViewTailsLogsToVisibleLines confirms View caps the rendered log to
// the last visibleLines entries, "tail -f" style.
func TestViewTailsLogsToVisibleLines(t *testing.T) {
	m := Model{Logs: []string{"one", "two", "three", "four", "five"}}
	out := m.View("Title", "done", "failed", 2)
	if strings.Contains(out, "one") || strings.Contains(out, "two") || strings.Contains(out, "three") {
		t.Errorf("View() with visibleLines=2 shows earlier lines it shouldn't:\n%s", out)
	}
	if !strings.Contains(out, "four") || !strings.Contains(out, "five") {
		t.Errorf("View() with visibleLines=2 is missing the last two lines:\n%s", out)
	}
}
