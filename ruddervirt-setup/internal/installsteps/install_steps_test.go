// SPDX-License-Identifier: GPL-3.0-only

package installsteps

import (
	"testing"

	"ruddervirt-setup/internal/config"
)

func TestComputePlanLines(t *testing.T) {
	steps := []Step{
		{
			Label: "with a plan",
			Plan:  func(cfg config.Config) string { return "skip - already done" },
		},
		{
			Label: "without a plan",
		},
	}
	got := ComputePlanLines(steps, config.Config{})
	want := []string{"skip - already done", "will run"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ComputePlanLines = %v, want %v", got, want)
	}
}

func TestComputePlanLinesEmpty(t *testing.T) {
	got := ComputePlanLines(nil, config.Config{})
	if len(got) != 0 {
		t.Errorf("ComputePlanLines(nil) = %v, want empty", got)
	}
}

func TestLaunchStep(t *testing.T) {
	step := Step{
		Label: "test step",
		Run: func(cfg config.Config, ch chan<- StepMsg) {
			ch <- StepOutputMsg("line one")
			ch <- StepOutputMsg("line two")
			ch <- StepDoneMsg{Label: "test step"}
		},
	}
	ch := LaunchStep(step, config.Config{})

	var lines []string
	var done StepDoneMsg
	for i := 0; i < 3; i++ {
		msg := <-ch
		switch v := msg.(type) {
		case StepOutputMsg:
			lines = append(lines, string(v))
		case StepDoneMsg:
			done = v
		default:
			t.Fatalf("unexpected message type %T", msg)
		}
	}
	if len(lines) != 2 || lines[0] != "line one" || lines[1] != "line two" {
		t.Errorf("streamed lines = %v, want [line one line two]", lines)
	}
	if done.Label != "test step" || done.Err != nil {
		t.Errorf("StepDoneMsg = %+v, want {Label: test step, Err: nil}", done)
	}
}
