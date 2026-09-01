// SPDX-License-Identifier: GPL-3.0-only

// Package installsteps holds the generic step-runner shape shared by every
// pipeline in this module (install, self-update, OS-update, and the three
// stabilizer flows): a Step{Label, Run, Plan} triple, launched onto its own
// goroutine/channel by LaunchStep, and previewed by ComputePlanLines.
//
// Deliberately bubbletea-free (see StepMsg's doc comment) - package main's
// own tea.Cmd wrappers (launchStep/readFromCh, install_steps.go) adapt
// LaunchStep's plain channel into the bubbletea event loop.
package installsteps

import (
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec"
)

// StepMsg is a type alias (not a defined type) for exec.StepMsg, so a chan
// StepMsg and a chan exec.StepMsg are interchangeable with no conversion. It
// lives in internal/exec rather than here since exec.RunStreamed is the
// natural bottom of this module's dependency graph; defining it here would
// force exec and every step-streaming domain package
// (aileron/k3s/kubevirt/multus/secrets/manifests/storage) to import this
// package just for a type, backwards from the real dependency direction.
type StepMsg = exec.StepMsg

// StepOutputMsg carries one raw output line from a running step - the
// StepMsg every exec.RunStreamed call in this module's Step.Run
// implementations sends via their wrap parameter.
type StepOutputMsg string

// StepDoneMsg reports that one Step's Run has finished, successfully (Err
// nil) or not.
type StepDoneMsg struct {
	Label string
	Err   error
}

// Step is one entry in an install/update pipeline: Run does the real work
// (streaming progress to ch, then sending a StepDoneMsg), Plan (if non-nil)
// previews what Run would do for cfg without changing state. Reused across
// every pipeline in this module - don't change this shape without checking
// all of them.
type Step struct {
	Label string
	Run   func(cfg config.Config, ch chan<- StepMsg)
	// Plan, if non-nil, returns a one-line read-only preview of what Run will
	// do for cfg - e.g. "skip - k3s v1.34.5+k3s1 already installed" vs "will
	// download k3s v1.34.5+k3s1". Must not mutate state, run a privileged/sudo
	// command, or assume a live k3s API exists - this runs before the operator
	// confirms Apply, possibly on a node where nothing is installed yet. nil
	// means skip-vs-do can't be cheaply predicted; ComputePlanLines then falls
	// back to a generic "will run" line.
	Plan func(cfg config.Config) string
}

// LaunchStep starts step.Run on its own goroutine against cfg, returning
// the channel it streams StepMsg values to - buffered generously so Run
// never blocks on a slow UI. Callers needing a tea.Cmd wrap the channel
// themselves (see package main's launchStep, install_steps.go).
func LaunchStep(step Step, cfg config.Config) chan StepMsg {
	ch := make(chan StepMsg, 100)
	go step.Run(cfg, ch)
	return ch
}

// ComputePlanLines previews steps against cfg without changing system
// state, one line per step in order - callers run this inside their own
// tea.Cmd (see package main's computeInstallPlanCmd) so a slow check can't
// block the UI thread.
func ComputePlanLines(steps []Step, cfg config.Config) []string {
	lines := make([]string, len(steps))
	for i, step := range steps {
		if step.Plan != nil {
			lines[i] = step.Plan(cfg)
		} else {
			lines[i] = "will run"
		}
	}
	return lines
}
