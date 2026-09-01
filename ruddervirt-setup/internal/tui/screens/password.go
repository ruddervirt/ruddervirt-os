// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"ruddervirt-setup/internal/password"
	"ruddervirt-setup/internal/tui"
)

// PasswordModel is the forced admin-password-change screen's sub-model
// (screenPasswordChange - screenPasswordCheck, the brief background check
// deciding whether to show this screen, has no UI state of its own beyond
// Error, which this struct also backs there; the screenPasswordCheck <->
// screenPasswordChange transition itself stays root-owned, see app.go).
type PasswordModel struct {
	NewInput     textinput.Model
	ConfirmInput textinput.Model
	// ConfirmFocus is true once the new-password field has validated and
	// focus has moved to the confirm field - false means NewInput has
	// focus (or, on screenPasswordCheck, that this struct's fields besides
	// Error aren't in play at all).
	ConfirmFocus bool
	Saving       bool
	Error        string
}

// PasswordSetMsg carries setAdminPasswordCmd's result back into Update -
// purely screen-local (Saving/Error only). On success, persisting
// cfg.System.PasswordChanged and moving on to Settings is cross-group
// routing that stays in package main (see password.go's
// finalizePasswordChangeCmd and app.go's passwordFinalizedMsg handling).
type PasswordSetMsg struct {
	Err error
}

func setAdminPasswordCmd(newPassword string) tea.Cmd {
	return func() tea.Msg {
		return PasswordSetMsg{Err: password.SetAdminPassword(newPassword)}
	}
}

// Update handles screenPasswordChange's own Enter-key validate/advance/
// submit sequence (length-check the new password, advance focus to
// confirm, match-check confirm, submit), plus forwarding other key presses
// to whichever of NewInput/ConfirmInput currently has focus.
//
// Deliberately does NOT handle Esc: the confirm-field "step back to
// new-password field" behavior is conditional on ConfirmFocus, while Esc
// from the new-password field falls all the way out to the main menu (a
// cross-group reset) - app.go keeps that guard itself rather than risk
// Update swallowing an Esc it shouldn't.
func (m PasswordModel) Update(msg tea.Msg) (PasswordModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyEnter {
		if !m.ConfirmFocus {
			// Validated before confirm gets focus, so a bad password is
			// caught immediately rather than only surfacing after confirm.
			if m.NewInput.Value() == "" {
				m.Error = "password must not be empty"
				return m, nil
			}
			if len(m.NewInput.Value()) < password.MinAdminPasswordLength {
				m.Error = fmt.Sprintf("password must be at least %d characters", password.MinAdminPasswordLength)
				return m, nil
			}
			m.NewInput.Blur()
			m.ConfirmInput.Focus()
			m.ConfirmFocus = true
			m.Error = ""
			return m, nil
		}
		newVal := m.NewInput.Value()
		if newVal != m.ConfirmInput.Value() {
			m.Error = "passwords do not match"
			m.ConfirmInput.SetValue("")
			return m, nil
		}
		m.Saving = true
		m.Error = ""
		return m, setAdminPasswordCmd(newVal)
	}
	var cmd tea.Cmd
	if m.ConfirmFocus {
		m.ConfirmInput, cmd = m.ConfirmInput.Update(msg)
	} else {
		m.NewInput, cmd = m.NewInput.Update(msg)
	}
	return m, cmd
}

// ClearInputs blanks and blurs both NewInput/ConfirmInput, leaving
// ConfirmFocus/Saving/Error untouched - used by app.go once the flow
// finishes successfully, and as a building block for Reset below.
func (m PasswordModel) ClearInputs() PasswordModel {
	m.NewInput.SetValue("")
	m.NewInput.Blur()
	m.ConfirmInput.SetValue("")
	m.ConfirmInput.Blur()
	return m
}

// Reset clears every field back to its zero value - called by app.go's
// generic cross-group Esc handler when abandoning a flow back to
// screenMenu.
func (m PasswordModel) Reset() PasswordModel {
	m = m.ClearInputs()
	m.Saving = false
	m.Error = ""
	m.ConfirmFocus = false
	return m
}

// ViewCheck renders screenPasswordCheck - moved verbatim from view.go's own
// case. Only Error is read here; NewInput/ConfirmInput/Saving/ConfirmFocus
// belong to screenPasswordChange (ViewChange below).
func (m PasswordModel) ViewCheck() string {
	s := "\n" + tui.HelpStyle.Render("Checking admin account credentials...") + "\n"
	if m.Error != "" {
		s += fmt.Sprintf("\n%s\n\n%s\n", tui.ErrorStyle.Render("Error: "+m.Error), tui.HelpStyle.Render("Press Esc to go back, Ctrl+C to quit."))
	}
	return s
}

// ViewChange renders screenPasswordChange - moved verbatim from view.go's
// own case.
func (m PasswordModel) ViewChange() string {
	s := "\n" + tui.TitleStyle.Render("Change Admin Password") + "\n\n"
	s += "This node is still using the well-known default password. Set a\n"
	s += "new admin password before continuing to configure.\n\n"
	if !m.ConfirmFocus {
		s += fmt.Sprintf("New password:      %s\n", m.NewInput.View())
		s += "Confirm password:\n"
	} else {
		s += fmt.Sprintf("New password:      %s\n", strings.Repeat("•", len(m.NewInput.Value())))
		s += fmt.Sprintf("Confirm password:  %s\n", m.ConfirmInput.View())
	}
	if m.Saving {
		s += "\n" + tui.HelpStyle.Render("Saving...") + "\n"
	} else if m.Error != "" {
		s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render("Error: "+m.Error))
	}
	if m.ConfirmFocus {
		s += "\n" + tui.HintBar([2]string{"Enter", "confirm"}, [2]string{"Esc", "back to new password"}, [2]string{"Ctrl+S", "skip for now"}) + "\n"
	} else {
		s += "\n" + tui.HintBar([2]string{"Enter", "continue"}, [2]string{"Esc", "cancel"}, [2]string{"Ctrl+S", "skip for now"}) + "\n"
	}
	return s
}
