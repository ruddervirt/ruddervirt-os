// SPDX-License-Identifier: GPL-3.0-only

// Package screens holds the app's per-screen-group sub-models: each group
// gets a struct with Update/View methods (matching internal/tui/pipeline's
// shape for the six pipeline-backed flows).
//
// A screen group only holds state local to itself. Anything read/written
// by another group (shared caches, cross-group routing like
// installConfirmOrigin/hostnameChangeForUpdate) stays on the root Model in
// package main, which is also the only place that ever changes which
// screen is current.
package screens

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"ruddervirt-setup/internal/hostname"
	"ruddervirt-setup/internal/tui"
)

// HostnameModel is the forced hostname-declaration screen's sub-model
// (screenHostnameChange) - gates entry into "configure"/"update" the first
// time this node's hostname is still the well-known default (see
// hostname.HostnameIsDefault/package main's hostnameLocked).
//
// Deliberately has no field for "which flow forced this screen open"
// (package main's hostnameChangeForUpdate): that decides which OTHER
// screen group to resume into, so it's cross-group routing state and stays
// on the root Model (see package doc).
type HostnameModel struct {
	Input  textinput.Model
	Saving bool
	Error  string
}

// HostnameSetMsg carries setHostnameCmd's result back into Update - purely
// screen-local (Saving/Error only). On success, persisting
// cfg.System.HostnameDeclared and deciding which screen to resume into is
// cross-group routing that stays in package main (see hostname.go's
// finalizeHostnameDeclaredCmd and app.go's hostnameDeclaredMsg handling).
type HostnameSetMsg struct {
	Err error
}

func setHostnameCmd(newHostname string) tea.Cmd {
	return func() tea.Msg {
		return HostnameSetMsg{Err: hostname.SetHostname(newHostname)}
	}
}

// Reset clears Saving/Error/Input back to their just-entered blank state.
// app.go calls this both when screenHostnameChange finishes successfully
// (Saving is already false by then) and from the generic cross-group Esc
// handler that abandons any flow back to screenMenu.
func (m HostnameModel) Reset() HostnameModel {
	m.Saving = false
	m.Error = ""
	m.Input.SetValue("")
	m.Input.Blur()
	return m
}

// Update handles this screen's own Enter-key validate-and-submit, plus
// forwarding other key presses to Input. Deliberately does NOT handle Esc:
// unlike PasswordModel's confirm-field back-navigation, this flow has no
// "step back", only "cancel out entirely" - handled by app.go's
// cross-group reset.
func (m HostnameModel) Update(msg tea.Msg) (HostnameModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyEnter {
		newHostname, err := hostname.ParseHostname(m.Input.Value())
		if err != nil {
			m.Error = err.Error()
			return m, nil
		}
		m.Saving = true
		m.Error = ""
		return m, setHostnameCmd(newHostname)
	}
	var cmd tea.Cmd
	m.Input, cmd = m.Input.Update(msg)
	return m, cmd
}

// View renders screenHostnameChange - moved verbatim from view.go's own
// case.
func (m HostnameModel) View() string {
	s := "\n" + tui.TitleStyle.Render("Set System Hostname") + "\n\n"
	s += "This node is still using the default hostname. Set one that\n"
	s += "identifies it on your network before continuing - the hostname\n"
	s += "cannot be changed once installation proceeds, so this cannot\n"
	s += "be skipped.\n\n"
	s += fmt.Sprintf("Hostname:  %s\n", m.Input.View())
	if m.Saving {
		s += "\n" + tui.HelpStyle.Render("Saving...") + "\n"
	} else if m.Error != "" {
		s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render("Error: "+m.Error))
	}
	s += "\n" + tui.HintBar([2]string{"Enter", "continue"}, [2]string{"Esc", "cancel"}) + "\n"
	return s
}
