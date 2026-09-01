// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"

	"ruddervirt-setup/internal/tui"
	"ruddervirt-setup/internal/tui/pipeline"
)

// PowerModel is the main menu's "power options" submenu's sub-model
// (screenPowerOptions, screenPowerConfirm, screenPowerApply) - reached from
// screenMenu's "power options" row (MenuOrder). Shutdown/Reboot go through
// a guarded type-"yes" confirm (Action set to "shutdown"/"reboot" picks
// which internal/power steps Pipeline runs and which copy ViewConfirm/
// ViewApply show) since either one takes down every VM running on this
// host, not just this session - Disconnect (the old bare "logout" menu
// entry) skips straight to tea.Quit in app.go, same as before, since it
// only ends this TUI session.
type PowerModel struct {
	Cursor       int
	Action       string // "shutdown" or "reboot" - "" until a picked
	ConfirmInput textinput.Model
	ConfirmError string

	Pipeline pipeline.Model
}

// PowerOrder is screenPowerOptions' row order - the arrow-key equivalent of
// MenuOrder, one level down.
var PowerOrder = []string{"Shutdown", "Reboot", "Disconnect"}

// Up moves Cursor up by one, clamped at 0 - mirrors MenuModel.Up.
func (m PowerModel) Up() PowerModel {
	if m.Cursor > 0 {
		m.Cursor--
	}
	return m
}

// Down moves Cursor down by one, clamped at the last PowerOrder entry -
// mirrors MenuModel.Down.
func (m PowerModel) Down() PowerModel {
	if m.Cursor < len(PowerOrder)-1 {
		m.Cursor++
	}
	return m
}

// ClearConfirm blanks and blurs ConfirmInput and clears ConfirmError,
// leaving Action/Cursor/Pipeline untouched - app.go calls this when Esc
// cancels screenPowerConfirm back to screenPowerOptions, same shape as
// UpdateModel.ClearConfirm.
func (m PowerModel) ClearConfirm() PowerModel {
	m.ConfirmInput.SetValue("")
	m.ConfirmInput.Blur()
	m.ConfirmError = ""
	return m
}

// Reset clears every field back to its zero value - app.go calls this
// landing on screenPowerOptions fresh (from the main menu) and from the
// generic cross-group Esc handler that abandons any flow back to
// screenMenu.
func (m PowerModel) Reset() PowerModel {
	m = m.ClearConfirm()
	m.Cursor = 0
	m.Action = ""
	m.Pipeline = pipeline.Model{}
	return m
}

// ViewOptions renders screenPowerOptions - same numbered-list shape as
// MenuModel.ViewHome's menu, one level down.
func (m PowerModel) ViewOptions() string {
	s := "\n" + tui.TitleStyle.Render("Power Options") + "\n\n"
	for i, item := range PowerOrder {
		cursor := "  "
		label := item
		if i == m.Cursor {
			cursor = tui.CursorStyle.Render(">") + " "
			label = tui.SelectedStyle.Render(item)
		}
		s += fmt.Sprintf("  %s%s %s\n", cursor, tui.MenuKeyStyle.Render(fmt.Sprintf("%d.", i+1)), label)
	}
	s += "\n" + tui.HintBar([2]string{"↑/↓", "navigate"}, [2]string{"Enter", "select"}, [2]string{"Esc", "back"}) + "\n"
	return s
}

// ViewConfirm renders screenPowerConfirm - the shared type-"yes" guard for
// both Shutdown and Reboot (see struct doc), copy switched on m.Action.
func (m PowerModel) ViewConfirm() string {
	verb, warning := "shut down", "shut down and powered off"
	if m.Action == "reboot" {
		verb, warning = "reboot", "restarted"
	}
	s := "\n" + tui.TitleStyle.Render("Confirm "+verb) + "\n\n"
	s += fmt.Sprintf("This host will be %s. Every VM running here stops immediately,\n", warning)
	s += "and consoles/the cloud UI go quiet until it's back up.\n"
	s += fmt.Sprintf("\n%s\n  %s\n", tui.HelpStyle.Render(fmt.Sprintf("Type \"yes\" to %s, or Esc to cancel:", verb)), m.ConfirmInput.View())
	if m.ConfirmError != "" {
		s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render(m.ConfirmError))
	}
	return s
}

// ViewApply renders screenPowerApply - the running shutdown/reboot pipeline
// itself - via pipeline.Model.View, copy switched on m.Action same as
// ViewConfirm. Unlike every other pipeline-backed screen, a successful run
// never returns here to report done: the command hands off to systemd and
// this whole process (along with the SSH session driving it) goes down with
// the host, so the done copy is aspirational, shown only in the unlikely
// window before that happens.
func (m PowerModel) ViewApply(visibleLines int) string {
	title, doneVerb := "Shutting down...", "Shutdown"
	if m.Action == "reboot" {
		title, doneVerb = "Rebooting...", "Reboot"
	}
	return m.Pipeline.View(
		title,
		tui.SuccessStyle.Render(doneVerb+" initiated.")+" "+tui.HelpStyle.Render("This session will disconnect shortly."),
		tui.ErrorStyle.Render(doneVerb+" failed.")+" "+tui.HelpStyle.Render("Press Esc to return to Power options."),
		visibleLines,
	)
}
