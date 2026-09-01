// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"fmt"
	"strings"
	"time"

	"ruddervirt-setup/internal/status"
	"ruddervirt-setup/internal/tui"
)

// MenuModel is the main menu / result screen sub-model (screenMenu,
// screenResult) - the app's home screen, plus a single-line pass-through
// "result" screen for any resolved menu label app.go doesn't route
// elsewhere (dead in practice - every menuOptions label is handled
// explicitly - but kept as a fallback).
type MenuModel struct {
	// Input is what the operator has typed so far - a number ("1") or word
	// ("configure") - an alternative to Cursor. Enter submits Input if it's
	// non-empty, otherwise MenuOrder[Cursor].
	Input  string
	Result string
	// ResultSource is unused (only ever assigned "" by the generic Esc reset
	// in app.go) - dead but harmless, not worth a behavior-risking cleanup.
	ResultSource string
	// Cursor is the ↑/↓ arrow-key selection into MenuOrder.
	Cursor int
}

// MenuOptions maps every accepted typed input (numbers and words) to its
// canonical label - moved here verbatim from view.go.
var MenuOptions = map[string]string{
	"1": "configure",
	"2": "k9s",
	"3": "shell",
	"4": "update",
	"5": "power options",
}

// MenuOrder is MenuOptions in display/selection order - the arrow-key
// alternative to typing a number needs an ordered list, which a map can't
// give it.
var MenuOrder = []string{"configure", "k9s", "shell", "update", "power options"}

// ResolveInput normalizes input (case/whitespace) and resolves it to a
// canonical menuOptions label, accepting either the number or the word
// itself.
func ResolveInput(input string) (string, bool) {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "install" {
		// Historical alias: applying settings now lives inside "configure"'s
		// Apply row. "update" is NOT aliased - it's its own real menu item
		// (self-updating from the latest GitHub release).
		input = "configure"
	}
	if label, ok := MenuOptions[input]; ok {
		return label, true
	}
	for _, label := range MenuOptions {
		if input == label {
			return label, true
		}
	}
	return "", false
}

// Up moves Cursor up by one, clamped at 0.
func (m MenuModel) Up() MenuModel {
	if m.Cursor > 0 {
		m.Cursor--
	}
	return m
}

// Down moves Cursor down by one, clamped at the last MenuOrder entry.
func (m MenuModel) Down() MenuModel {
	if m.Cursor < len(MenuOrder)-1 {
		m.Cursor++
	}
	return m
}

// Reset clears Input/Result/ResultSource to their zero values - called by
// app.go's generic cross-group Esc handler when abandoning a flow back to
// screenMenu. Cursor deliberately survives - no reason to lose the
// operator's arrow-key position because some other screen was cancelled.
func (m MenuModel) Reset() MenuModel {
	m.Input = ""
	m.Result = ""
	m.ResultSource = ""
	return m
}

// Backspace deletes the last typed rune from Input, if any.
func (m MenuModel) Backspace() MenuModel {
	if len(m.Input) > 0 {
		m.Input = m.Input[:len(m.Input)-1]
	}
	return m
}

// TypeRune appends s (a tea.KeyMsg.String() for a KeyRunes press) to Input.
func (m MenuModel) TypeRune(s string) MenuModel {
	m.Input += s
	return m
}

// HomeParams bundles the cross-group cache/derived state the home screen
// (screenMenu's default View) needs to render - service status, host
// stats, and the "Aileron UI" link - all populated by other screens'/Init's
// background fetches, so it stays on the root Model rather than being
// duplicated into MenuModel. View takes it as an explicit parameter since
// MenuModel can't reach package-level globals or package main's
// aileronUIURL bridge function.
type HomeParams struct {
	TermWidth          int
	StabilizerDetected bool
	// AileronUIURL is "" if config isn't saved yet, or Aileron has no URL to
	// show - computed by package main's aileronUIURL, which this package
	// can't call (domain packages never depend on package main).
	AileronUIURL    string
	ServiceStatuses []status.ServiceStatus
	HostStats       status.HostStats
	StatusUpdatedAt time.Time
	// UpdateAvailable marks the "update" row with an ↑ (see
	// screens.AnyUpdateAvailable) - computed from the same caches
	// screenUpdateVersions itself renders from, so this flag matches
	// whatever that screen would show.
	UpdateAvailable bool
}

// ViewHome renders screenMenu's default (home) view - moved verbatim from
// view.go's own default: case.
func (m MenuModel) ViewHome(p HomeParams) string {
	s := "\n" + tui.BigTitle(p.TermWidth) + "\n\n"
	if p.StabilizerDetected {
		s += tui.HelpStyle.Render("This system is connected to and managed by ruddervirt.com") + "\n\n"
	}
	if p.AileronUIURL != "" {
		s += fmt.Sprintf("Aileron UI:  %s\n\n", tui.LinkStyle.Render(p.AileronUIURL))
	}
	s += tui.RenderHomeStatus(p.ServiceStatuses, p.HostStats, p.StatusUpdatedAt, p.TermWidth)
	// The ↑/↓ cursor only shows while Input's empty - once typing starts,
	// Enter submits Input instead (app.go's screenMenu case), so a
	// highlighted cursor row would be misleading.
	showCursor := m.Input == ""
	for i, item := range MenuOrder {
		cursor := "  "
		// display bakes the ↑ prefix in before styling, same "style only
		// after the plain text is final" rule as updateRowIconPrefix
		// (internal/tui/screens/update.go), so it survives either branch
		// below untouched by the other.
		display := item
		if item == "update" && p.UpdateAvailable {
			display = "↑ " + item
		}
		label := display
		if showCursor && i == m.Cursor {
			cursor = tui.CursorStyle.Render(">") + " "
			label = tui.SelectedStyle.Render(display)
		} else if item == "update" && p.UpdateAvailable {
			label = tui.ColorUpdateIcon(display)
		}
		s += fmt.Sprintf("  %s%s %s\n", cursor, tui.MenuKeyStyle.Render(fmt.Sprintf("%d.", i+1)), label)
	}
	s += fmt.Sprintf("\n%s %s_\n\n", tui.PromptStyle.Render(">"), m.Input)
	s += tui.HintBar([2]string{"↑/↓", "navigate"}, [2]string{"Enter", "select"}, [2]string{"Ctrl+C", "quit"}) + "\n"
	return s
}

// ViewResult renders screenResult - moved verbatim from view.go's own case.
func (m MenuModel) ViewResult() string {
	return fmt.Sprintf("\n%s\n\n%s\n", m.Result, tui.HelpStyle.Render("Press Esc to go back, Ctrl+C to quit."))
}
