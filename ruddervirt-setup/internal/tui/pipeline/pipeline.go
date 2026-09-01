// SPDX-License-Identifier: GPL-3.0-only

// Package pipeline implements the generic step-runner sub-model shared by
// every install/update-style flow in this module: launch a
// []installsteps.Step pipeline one step at a time, streaming its output
// into a scrolling log and tracking completion/failure. Embedded by six
// screens in internal/tui/screens (install, self-update, OS-update,
// stabilizer-adopt, stabilizer-settings-apply, stabilizer-version-apply)
// instead of each hand-rolling the same fields and message switch.
package pipeline

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/tui"
)

// Model is the generic step-runner sub-model. Idx indexes the step
// currently running (or, once Done, one past the last step). ch is
// unexported and touched only through New/Update - draining it directly
// from an embedding screen would race the still-running goroutine
// LaunchStep started.
type Model struct {
	Steps  []installsteps.Step
	Idx    int
	Logs   []string
	Done   bool
	Failed bool
	ch     chan installsteps.StepMsg
}

// launch starts step against cfg (via installsteps.LaunchStep's goroutine)
// and returns the tea.Cmd that reads its first message off the resulting
// channel; mirrors package main's launchStep (install_steps.go).
func launch(step installsteps.Step, cfg config.Config) (chan installsteps.StepMsg, tea.Cmd) {
	ch := installsteps.LaunchStep(step, cfg)
	return ch, readFromCh(ch)
}

func readFromCh(ch chan installsteps.StepMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// New launches steps[0] against cfg and returns the resulting Model plus
// the tea.Cmd that reads its first message. If steps is empty, returns a
// no-op Model with Done already true.
func New(steps []installsteps.Step, cfg config.Config) (Model, tea.Cmd) {
	m := Model{Steps: steps}
	if len(steps) == 0 {
		m.Done = true
		return m, nil
	}
	ch, cmd := launch(steps[0], cfg)
	m.ch = ch
	return m, cmd
}

// Update advances the pipeline on step-output/step-done messages: appends
// one line per StepOutputMsg (and keeps reading ch), and on StepDoneMsg
// either appends "✗ Label: err" and sets Failed, or appends "✓ Label" and
// advances to the next step (or sets Done once Steps is exhausted). cfg is
// only used to launch the next step, so it isn't stored on Model.
//
// Callers route messages here themselves (typically gated on their own "am
// I the active screen" check in the root Model) - Update can't tell which
// pipeline a given message belongs to, since every pipeline in the process
// shares the same two message types (see installsteps' doc comment). Once
// Done or Failed, Update is a no-op: nothing is left in flight to read.
func (m Model) Update(msg tea.Msg, cfg config.Config) (Model, tea.Cmd) {
	if m.Done || m.Failed {
		return m, nil
	}
	switch msg := msg.(type) {
	case installsteps.StepOutputMsg:
		m.Logs = append(m.Logs, string(msg))
		return m, readFromCh(m.ch)

	case installsteps.StepDoneMsg:
		if msg.Err != nil {
			m.Logs = append(m.Logs, fmt.Sprintf("✗ %s: %s", msg.Label, msg.Err.Error()))
			m.Failed = true
			return m, nil
		}
		m.Logs = append(m.Logs, fmt.Sprintf("✓ %s", msg.Label))
		m.Idx++
		if m.Idx >= len(m.Steps) {
			m.Done = true
			return m, nil
		}
		ch, cmd := launch(m.Steps[m.Idx], cfg)
		m.ch = ch
		return m, cmd
	}
	return m, nil
}

// View renders the shared log-scroll shape used by every step-runner
// screen: a title, the tailed log (capped to visibleLines, "tail -f"
// style), and a running/done/failed footer. doneMsg/failedMsg are
// screen-specific copy shown once the pipeline finishes - callers pass them
// ALREADY STYLED, since each flow composes its message from two
// differently-styled parts (e.g. green outcome + muted "Press Esc..."
// hint); View applying its own style on top would flatten that to one
// color. Passed in whole, not hardcoded, since the copy (and where Esc
// returns to) differs per screen.
func (m Model) View(title, doneMsg, failedMsg string, visibleLines int) string {
	s := "\n" + tui.TitleStyle.Render(title) + "\n\n"
	lines := m.Logs
	if len(lines) > visibleLines {
		lines = lines[len(lines)-visibleLines:]
	}
	for _, line := range lines {
		s += line + "\n"
	}
	switch {
	case m.Done:
		s += "\n" + doneMsg + "\n"
	case m.Failed:
		s += "\n" + failedMsg + "\n"
	case m.Idx < len(m.Steps):
		s += "\n" + tui.HelpStyle.Render(fmt.Sprintf("Running: %s...", m.Steps[m.Idx].Label)) + "\n"
	}
	return s
}
