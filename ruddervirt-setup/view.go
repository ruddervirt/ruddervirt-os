// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"
)

// settingsChromeLines is the number of lines the Settings screen spends on
// its header, footer/help text, scroll indicators, table borders/header
// row, and the fixed Apply action bar below the table - i.e. everything
// around the field rows themselves. Subtracted from the terminal height to
// decide how many fields can be shown at once.
const settingsChromeLines = 20

// networkSetupLabel is the toggle row that expands/collapses the local
// physical network fields (interface, addressing, and static IP details)
// nested beneath it. Uses the full-size ▶/▼ triangles (same family as the
// Apply button's ▶), not the small ▸/▾ variants - those render inconsistently
// across terminal fonts (some render them tiny/off-baseline, or double-width
// under East Asian locale settings), throwing off the table's alignment.
func networkSetupLabel(expanded bool) string {
	if expanded {
		return "▼ Local physical network setup"
	}
	return "▶ Local physical network setup"
}

// advancedSettingsLabel is the toggle row that expands/collapses the
// advanced (rarely-changed) fields nested beneath it.
func advancedSettingsLabel(expanded bool) string {
	if expanded {
		return "▼ Advanced settings"
	}
	return "▶ Advanced settings"
}

// settingsRow is one row in the Settings table: either a real settingField
// (nested, i.e. indented, if it belongs to a collapsible group) or one of
// the synthetic toggle rows.
type settingsRow struct {
	field           settingField
	isNetworkToggle bool
	isToggle        bool
	nested          bool
}

// settingsRows lays out the Settings table in display order:
//
//  1. The "Local physical network setup" toggle row (where
//     interface_name/addressing used to sit directly) - expanding it
//     reveals the interface and addressing fields, plus (only when
//     addressing is static) the static IP/prefix/gateway/DNS fields, all
//     nested one level.
//  2. Any remaining plain fields (automatic updates, k3s version, ...).
//  3. The "Advanced settings" toggle row, then its fields when expanded.
//
// Apply isn't one of these rows - it's rendered as its own fixed action
// bar below the table (see the screenSettings case in View), one cursor
// position past the last row here (settingsScrollCursor). That keeps it
// reachable by pressing Down past the end of the list while always
// staying visible at the bottom of the page, regardless of scroll.
//
// Every toggle row's own position is fixed regardless of its group's
// expand state - only what's nested beneath it grows or shrinks - so
// toggling one never shifts the cursor onto a different field.
func (m model) settingsRows() []settingsRow {
	var plain []settingsRow
	var network []settingsRow
	var advanced []settingField

	for _, f := range settingFields {
		switch {
		case f.updateScreen:
			continue // lives on the Update screen instead - see updateVersionsRows
		case f.advanced:
			advanced = append(advanced, f)
		case f.networkSetup:
			network = append(network, settingsRow{field: f, nested: true})
		case f.staticOnly:
			if m.cfg.Network.Addressing == "static" {
				network = append(network, settingsRow{field: f, nested: true})
			}
		default:
			plain = append(plain, settingsRow{field: f})
		}
	}

	rows := []settingsRow{{isNetworkToggle: true}}
	if m.settingsShowNetwork {
		rows = append(rows, network...)
	}
	rows = append(rows, plain...)
	rows = append(rows, settingsRow{isToggle: true})
	if m.settingsShowAdvanced {
		for _, f := range advanced {
			rows = append(rows, settingsRow{field: f, nested: true})
		}
	}
	return rows
}

// settingsScrollCursor caps settingsCursor at the last real table row for
// scroll-window purposes - Apply lives in its own fixed footer below the
// table (see settingsRows), never inside the scrolling window, so it
// should never pull the table into scrolling one row further than its
// content requires.
func (m model) settingsScrollCursor() int {
	if last := len(m.settingsRows()) - 1; m.settingsCursor > last {
		return last
	}
	return m.settingsCursor
}

// updateVersionsRow is one row of the Update screen's landing table:
// either the ruddervirt-setup row (delegates into the existing
// checkForUpdateCmd/screenUpdateChecking flow) or one of the component
// version fields moved out of Settings (settingField.updateScreen).
type updateVersionsRow struct {
	isSelfUpdate bool
	field        settingField
}

// updateVersionsRows lists the Update screen's rows: ruddervirt-setup
// first, then every settingField tagged updateScreen, in settingFields'
// order. Apply isn't one of these rows - same fixed-footer treatment as
// Settings' Apply (see settingsRows), one cursor position past the last
// row here.
func updateVersionsRows() []updateVersionsRow {
	rows := []updateVersionsRow{{isSelfUpdate: true}}
	for _, f := range settingFields {
		if f.updateScreen {
			rows = append(rows, updateVersionsRow{field: f})
		}
	}
	return rows
}

// updateVersionsScrollCursor mirrors settingsScrollCursor for the Update
// screen's own cursor/row list.
func (m model) updateVersionsScrollCursor() int {
	if last := len(updateVersionsRows()) - 1; m.updateVersionsCursor > last {
		return last
	}
	return m.updateVersionsCursor
}

// settingsVisibleRows returns how many setting fields fit in the current
// terminal height. Falls back to a conservative default before the first
// tea.WindowSizeMsg arrives.
func (m model) settingsVisibleRows() int {
	h := m.termHeight
	if h <= 0 {
		h = 24
	}
	visible := h - settingsChromeLines
	if visible < 1 {
		visible = 1
	}
	return visible
}

// clampScroll adjusts scroll so cursor stays within the visible window.
func clampScroll(scroll, cursor, visible int) int {
	if cursor < scroll {
		scroll = cursor
	}
	if cursor >= scroll+visible {
		scroll = cursor - visible + 1
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll
}

// fitCell truncates s with an ellipsis if it's longer than width, or pads
// it with spaces to exactly width otherwise, keeping the Settings table's
// columns aligned regardless of content length.
func fitCell(s string, width int) string {
	w := runewidth.StringWidth(s)
	if w > width {
		return runewidth.Truncate(s, width, "…")
	}
	return s + strings.Repeat(" ", width-w)
}

// installChromeLines mirrors settingsChromeLines for the install log screen.
const installChromeLines = 5

// installVisibleLogLines returns how many log lines fit in the current
// terminal height, so the view can tail the log like `tail -f` instead of
// printing lines the terminal can't show.
func (m model) installVisibleLogLines() int {
	h := m.termHeight
	if h <= 0 {
		h = 24
	}
	visible := h - installChromeLines
	if visible < 1 {
		visible = 1
	}
	return visible
}

var menuOptions = map[string]string{
	"1": "configure",
	"2": "k9s",
	"3": "shell",
	"4": "update",
	"5": "logout",
}

// menuOrder is menuOptions in display/selection order - the arrow-key
// alternative to typing a number (see model.menuCursor) needs an ordered
// list, which a map can't give it.
var menuOrder = []string{"configure", "k9s", "shell", "update", "logout"}

// saveSettingCmd persists cfg after a settings-field edit - nothing more.
// Settings only checks and records intent; install is the one place that
// ever actually applies it to the running system (see the "Applying
// settings" install step below). Runs as a tea.Cmd (its own goroutine)
// since saveConfig shells out via sudo and would otherwise block the UI
// loop.

func resolveInput(input string) (string, bool) {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "install" {
		// "install" is a historical alias only - applying settings (the
		// old standalone "install" menu item) now lives inside the
		// "configure" flow's Apply row. "update" is NOT aliased here:
		// it's now its own real menu item (self-updating ruddervirt-setup
		// from the latest GitHub release), so it falls through to the
		// generic label match below instead.
		input = "configure"
	}
	if label, ok := menuOptions[input]; ok {
		return label, true
	}
	for _, label := range menuOptions {
		if input == label {
			return label, true
		}
	}
	return "", false
}

func (m model) View() string {
	switch m.current {

	case screenInstallPlanning:
		return "\nComputing install plan...\n"

	case screenInstallConfirm:
		s := "\n" + titleStyle.Render("Confirm Install") + "\n\n"
		s += "This will restart k3s, causing a brief interruption to the\n"
		s += "Kubernetes API and any running workloads. The full process can\n"
		s += "take 30+ minutes, mostly waiting for storage to become ready.\n"
		s += "\nPlan:\n"
		labelWidth := 0
		for _, step := range installSteps {
			if l := runewidth.StringWidth(step.label); l > labelWidth {
				labelWidth = l
			}
		}
		for i, step := range installSteps {
			line := "will run"
			if i < len(m.installPlanLines) && m.installPlanLines[i] != "" {
				line = m.installPlanLines[i]
			}
			if line == "will run" {
				line = helpStyle.Render(line)
			}
			s += fmt.Sprintf("  %s  %s\n", fitCell(step.label, labelWidth), line)
		}
		s += fmt.Sprintf("\n%s\n  %s\n", helpStyle.Render("Type \"yes\" to proceed, or Esc to cancel:"), m.installConfirmInput.View())
		if m.installConfirmError != "" {
			s += fmt.Sprintf("\n%s\n", errorStyle.Render(m.installConfirmError))
		}
		return s

	case screenInstall:
		s := "\n" + titleStyle.Render("Installing RudderVirt...") + "\n\n"
		visible := m.installVisibleLogLines()
		lines := m.installLogs
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, line := range lines {
			s += line + "\n"
		}
		if m.installDone {
			s += "\n" + successStyle.Render("Install complete.") + " " + helpStyle.Render("Press Esc to return to menu.") + "\n"
		} else if m.installFailed {
			s += "\n" + errorStyle.Render("Install failed.") + " " + helpStyle.Render("Press Esc to return to menu.") + "\n"
		} else if m.installStepIdx < len(installSteps) {
			s += "\n" + helpStyle.Render(fmt.Sprintf("Running: %s...", installSteps[m.installStepIdx].label)) + "\n"
		}
		return s

	case screenResult:
		return fmt.Sprintf("\n%s\n\n%s\n", m.result, helpStyle.Render("Press Esc to go back, Ctrl+C to quit."))

	case screenUpdateChecking:
		return "\n" + helpStyle.Render("Checking for updates...") + "\n"

	case screenPasswordCheck:
		s := "\n" + helpStyle.Render("Checking admin account credentials...") + "\n"
		if m.passwordError != "" {
			s += fmt.Sprintf("\n%s\n\n%s\n", errorStyle.Render("Error: "+m.passwordError), helpStyle.Render("Press Esc to go back, Ctrl+C to quit."))
		}
		return s

	case screenPasswordChange:
		s := "\n" + titleStyle.Render("Change Admin Password") + "\n\n"
		s += "This node is still using the well-known default password. Set a\n"
		s += "new admin password before continuing to configure.\n\n"
		if !m.passwordConfirmFocus {
			s += fmt.Sprintf("New password:      %s\n", m.passwordNewInput.View())
			s += "Confirm password:\n"
		} else {
			s += fmt.Sprintf("New password:      %s\n", strings.Repeat("•", len(m.passwordNewInput.Value())))
			s += fmt.Sprintf("Confirm password:  %s\n", m.passwordConfirmInput.View())
		}
		if m.passwordSaving {
			s += "\n" + helpStyle.Render("Saving...") + "\n"
		} else if m.passwordError != "" {
			s += fmt.Sprintf("\n%s\n", errorStyle.Render("Error: "+m.passwordError))
		}
		if m.passwordConfirmFocus {
			s += "\n" + hintBar([2]string{"Enter", "confirm"}, [2]string{"Esc", "back to new password"}, [2]string{"Ctrl+S", "skip for now"}) + "\n"
		} else {
			s += "\n" + hintBar([2]string{"Enter", "continue"}, [2]string{"Esc", "cancel"}, [2]string{"Ctrl+S", "skip for now"}) + "\n"
		}
		return s

	case screenUpdateConfirm:
		s := "\n" + titleStyle.Render("Update ruddervirt-setup") + "\n\n"
		s += fmt.Sprintf("Current version: %s\nLatest version:  %s\n", version, m.updateLatestVersion)
		s += "\nThis will download and verify (SHA256) the new binary, then\n"
		s += "replace /usr/local/bin/ruddervirt-setup. The menu will restart\n"
		s += "on the new version afterward.\n"
		s += fmt.Sprintf("\n%s\n  %s\n", helpStyle.Render("Type \"yes\" to proceed, or Esc to cancel:"), m.updateConfirmInput.View())
		if m.updateConfirmError != "" {
			s += fmt.Sprintf("\n%s\n", errorStyle.Render(m.updateConfirmError))
		}
		return s

	case screenUpdate:
		s := "\n" + titleStyle.Render("Updating ruddervirt-setup...") + "\n\n"
		visible := m.installVisibleLogLines()
		lines := m.updateLogs
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, line := range lines {
			s += line + "\n"
		}
		if m.updateDone {
			s += "\n" + successStyle.Render("Update complete. Restarting into the new version...") + "\n"
		} else if m.updateFailed {
			s += "\n" + errorStyle.Render("Update failed.") + " " + helpStyle.Render("Press Esc to return to menu.") + "\n"
		} else if m.updateStepIdx < len(updateSteps) {
			s += "\n" + helpStyle.Render(fmt.Sprintf("Running: %s...", updateSteps[m.updateStepIdx].label)) + "\n"
		}
		return s

	case screenUpdateVersions:
		if m.updateVersionsPicking {
			rows := updateVersionsRows()
			field := rows[m.updateVersionsCursor].field
			options := m.updateVersionsPickOptions

			width := runewidth.StringWidth(field.label) + 4
			for _, o := range options {
				if l := runewidth.StringWidth(o) + 4; l > width {
					width = l
				}
			}
			termWidth := m.termWidth
			if termWidth <= 0 {
				termWidth = 80
			}
			if width > termWidth-2 {
				width = termWidth - 2
			}
			if width < 20 {
				width = 20
			}

			topBorder := "╭" + strings.Repeat("─", width) + "╮"
			bottomBorder := "╰" + strings.Repeat("─", width) + "╯"

			s := "\n" + titleStyle.Render("Update") + fmt.Sprintf("\n\nSelect %s:\n\n", field.label)

			visible := m.settingsVisibleRows()
			start := m.updateVersionsPickScroll
			end := start + visible
			if end > len(options) {
				end = len(options)
			}
			if start > end {
				start = end
			}
			if start > 0 {
				s += helpStyle.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n"
			}

			s += colorBorders(topBorder) + "\n"
			for i := start; i < end; i++ {
				selected := i == m.updateVersionsPickCursor
				cursor := "  "
				cell := fitCell(options[i], width-2)
				if selected {
					cursor = cursorStyle.Render(">") + " "
					cell = selectedStyle.Render(cell)
				}
				s += fmt.Sprintf("%s%s%s%s\n", colorBorders("│"), cursor, cell, colorBorders("│"))
			}
			s += colorBorders(bottomBorder) + "\n"
			if end < len(options) {
				s += helpStyle.Render(fmt.Sprintf("  ↓ %d more below", len(options)-end)) + "\n"
			}

			if m.settingsError != "" {
				s += fmt.Sprintf("\n%s\n", errorStyle.Render("Error: "+m.settingsError))
			}
			s += "\n" + hintBar([2]string{"↑/↓", "navigate"}, [2]string{"Enter", "select"}, [2]string{"Esc", "cancel"}) + "\n"
			return s
		}

		rows := updateVersionsRows()
		total := len(rows)

		rowLabel := func(r updateVersionsRow) string {
			if r.isSelfUpdate {
				return "ruddervirt-setup"
			}
			return r.field.label
		}
		rowValue := func(r updateVersionsRow) string {
			if r.isSelfUpdate {
				return version
			}
			if r.field.locked != nil {
				if locked, reason := r.field.locked(&m.cfg); locked {
					return reason
				}
			}
			return r.field.get(&m.cfg)
		}

		labelWidth := len("Component")
		valueWidth := len("Version")
		for _, r := range rows {
			if l := runewidth.StringWidth(rowLabel(r)); l > labelWidth {
				labelWidth = l
			}
			if l := runewidth.StringWidth(rowValue(r)); l > valueWidth {
				valueWidth = l
			}
		}

		termWidth := m.termWidth
		if termWidth <= 0 {
			termWidth = 80
		}
		const tableOverhead = 11 // "│" + cursor(3) + "│ " + " │ " + " │"
		for labelWidth+valueWidth+tableOverhead > termWidth && valueWidth > 12 {
			valueWidth--
		}
		for labelWidth+valueWidth+tableOverhead > termWidth && labelWidth > 20 {
			labelWidth--
		}
		tableWidth := labelWidth + valueWidth + tableOverhead

		topBorder := "╭───┬" + strings.Repeat("─", labelWidth+2) + "┬" + strings.Repeat("─", valueWidth+2) + "╮"
		sepBorder := "├───┼" + strings.Repeat("─", labelWidth+2) + "┼" + strings.Repeat("─", valueWidth+2) + "┤"
		bottomBorder := "╰───┴" + strings.Repeat("─", labelWidth+2) + "┴" + strings.Repeat("─", valueWidth+2) + "╯"

		s := "\n" + titleStyle.Render("Update") + "\n\n"

		visible := m.settingsVisibleRows()
		start := m.updateVersionsScroll
		end := start + visible
		if end > total {
			end = total
		}
		if start > end {
			start = end
		}
		if start > 0 {
			s += helpStyle.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n"
		}

		s += colorBorders(topBorder) + "\n"
		s += colorBorders(fmt.Sprintf("│   │ %s │ %s │", fitCell("Component", labelWidth), fitCell("Version", valueWidth))) + "\n"
		s += colorBorders(sepBorder) + "\n"
		for i := start; i < end; i++ {
			selected := i == m.updateVersionsCursor
			r := rows[i]
			label := fitCell(rowLabel(r), labelWidth)
			value := fitCell(rowValue(r), valueWidth)
			if selected {
				label = selectedStyle.Render(label)
				value = selectedStyle.Render(value)
			}
			s += fmt.Sprintf("%s%s%s %s %s %s %s\n", colorBorders("│"), cursorArrow(selected), colorBorders("│"), label, colorBorders("│"), value, colorBorders("│"))
		}
		s += colorBorders(bottomBorder) + "\n"

		if end < total {
			s += helpStyle.Render(fmt.Sprintf("  ↓ %d more below", total-end)) + "\n"
		}

		// Apply upgrades: same fixed-footer treatment as Settings' Apply
		// (see settingsRows) - pushes whatever versions are picked above
		// through the same install pipeline.
		s += renderApplyBar(tableWidth, "Apply upgrades", m.updateVersionsCursor == total)

		if m.settingsSaving {
			s += "\n" + helpStyle.Render("Saving...") + "\n"
		} else if m.settingsError != "" {
			s += fmt.Sprintf("\n%s\n", errorStyle.Render("Error: "+m.settingsError))
		}
		s += "\n" + hintBar([2]string{"↑/↓", "navigate"}, [2]string{"Enter", "check / choose / apply"}, [2]string{"Esc", "back"}) + "\n"
		return s

	case screenSettings:
		if m.settingsPicking {
			field := m.settingsRows()[m.settingsCursor].field
			options := m.settingsPickOptions

			width := runewidth.StringWidth(field.label) + 4
			for _, o := range options {
				if l := runewidth.StringWidth(o) + 4; l > width {
					width = l
				}
			}
			termWidth := m.termWidth
			if termWidth <= 0 {
				termWidth = 80
			}
			if width > termWidth-2 {
				width = termWidth - 2
			}
			if width < 20 {
				width = 20
			}

			topBorder := "╭" + strings.Repeat("─", width) + "╮"
			bottomBorder := "╰" + strings.Repeat("─", width) + "╯"

			s := "\n" + titleStyle.Render("Settings") + fmt.Sprintf("\n\nSelect %s:\n\n", field.label)

			visible := m.settingsVisibleRows()
			start := m.settingsPickScroll
			end := start + visible
			if end > len(options) {
				end = len(options)
			}
			if start > end {
				start = end
			}
			if start > 0 {
				s += helpStyle.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n"
			}

			s += colorBorders(topBorder) + "\n"
			for i := start; i < end; i++ {
				selected := i == m.settingsPickCursor
				cursor := "  "
				cell := fitCell(options[i], width-2)
				if selected {
					cursor = cursorStyle.Render(">") + " "
					cell = selectedStyle.Render(cell)
				}
				s += fmt.Sprintf("%s%s%s%s\n", colorBorders("│"), cursor, cell, colorBorders("│"))
			}
			s += colorBorders(bottomBorder) + "\n"
			if end < len(options) {
				s += helpStyle.Render(fmt.Sprintf("  ↓ %d more below", len(options)-end)) + "\n"
			}

			if m.settingsError != "" {
				s += fmt.Sprintf("\n%s\n", errorStyle.Render("Error: "+m.settingsError))
			}
			s += "\n" + hintBar([2]string{"↑/↓", "navigate"}, [2]string{"Enter", "select"}, [2]string{"Esc", "cancel"}) + "\n"
			return s
		}

		rows := m.settingsRows()
		total := len(rows)

		rowLabel := func(r settingsRow) string {
			switch {
			case r.isNetworkToggle:
				return networkSetupLabel(m.settingsShowNetwork)
			case r.isToggle:
				return advancedSettingsLabel(m.settingsShowAdvanced)
			case r.nested:
				return "  " + r.field.label
			default:
				return r.field.label
			}
		}
		rowValue := func(r settingsRow) string {
			if r.isNetworkToggle || r.isToggle {
				return ""
			}
			if r.field.locked != nil {
				if locked, reason := r.field.locked(&m.cfg); locked {
					return reason
				}
			}
			return r.field.get(&m.cfg)
		}

		labelWidth := len("Setting")
		valueWidth := len("Value")
		for _, r := range rows {
			if l := runewidth.StringWidth(rowLabel(r)); l > labelWidth {
				labelWidth = l
			}
			if l := runewidth.StringWidth(rowValue(r)); l > valueWidth {
				valueWidth = l
			}
		}

		termWidth := m.termWidth
		if termWidth <= 0 {
			termWidth = 80
		}
		const tableOverhead = 11 // "│" + cursor(3) + "│ " + " │ " + " │"
		for labelWidth+valueWidth+tableOverhead > termWidth && valueWidth > 12 {
			valueWidth--
		}
		for labelWidth+valueWidth+tableOverhead > termWidth && labelWidth > 20 {
			labelWidth--
		}
		tableWidth := labelWidth + valueWidth + tableOverhead

		topBorder := "╭───┬" + strings.Repeat("─", labelWidth+2) + "┬" + strings.Repeat("─", valueWidth+2) + "╮"
		sepBorder := "├───┼" + strings.Repeat("─", labelWidth+2) + "┼" + strings.Repeat("─", valueWidth+2) + "┤"
		bottomBorder := "╰───┴" + strings.Repeat("─", labelWidth+2) + "┴" + strings.Repeat("─", valueWidth+2) + "╯"

		s := "\n" + titleStyle.Render("Settings") + "\n\n"

		visible := m.settingsVisibleRows()
		start := m.settingsScroll
		end := start + visible
		if end > total {
			end = total
		}
		if start > end {
			start = end
		}
		if start > 0 {
			s += helpStyle.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n"
		}

		s += colorBorders(topBorder) + "\n"
		s += colorBorders(fmt.Sprintf("│   │ %s │ %s │", fitCell("Setting", labelWidth), fitCell("Value", valueWidth))) + "\n"
		s += colorBorders(sepBorder) + "\n"
		for i := start; i < end; i++ {
			selected := i == m.settingsCursor
			r := rows[i]
			label := fitCell(rowLabel(r), labelWidth)
			value := fitCell(rowValue(r), valueWidth)
			if selected {
				label = selectedStyle.Render(label)
				value = selectedStyle.Render(value)
			} else if r.isNetworkToggle || r.isToggle {
				label = colorToggleArrow(label)
			}
			s += fmt.Sprintf("%s%s%s %s %s %s %s\n", colorBorders("│"), cursorArrow(selected), colorBorders("│"), label, colorBorders("│"), value, colorBorders("│"))
		}
		s += colorBorders(bottomBorder) + "\n"

		if end < total {
			s += helpStyle.Render(fmt.Sprintf("  ↓ %d more below", total-end)) + "\n"
		}

		// Apply: a fixed action bar below the table, not one of its rows -
		// always visible at the bottom of the page regardless of scroll.
		// One cursor position past the last real row selects it.
		s += renderApplyBar(tableWidth, "Apply (install / re-apply)", m.settingsCursor == total)

		if m.settingsEditing {
			s += fmt.Sprintf("\nEditing %s:\n  %s\n", rows[m.settingsCursor].field.label, m.settingsInput.View())
			if m.settingsError != "" {
				s += fmt.Sprintf("\n%s\n", errorStyle.Render("Error: "+m.settingsError))
			}
			s += "\n" + hintBar([2]string{"Enter", "save"}, [2]string{"Esc", "cancel"}) + "\n"
		} else {
			if m.settingsSaving {
				s += "\n" + helpStyle.Render("Saving...") + "\n"
			} else if m.settingsError != "" {
				s += fmt.Sprintf("\n%s\n", errorStyle.Render("Error saving: "+m.settingsError))
			}
			s += "\n" + hintBar([2]string{"↑/↓", "navigate"}, [2]string{"Enter", "edit / choose / apply"}, [2]string{"Esc", "back"}) + "\n"
		}
		return s

	default:
		s := "\n" + bigTitle(m.termWidth) + "\n\n"
		if configSaved() {
			if url := aileronUIURL(m.cfg); url != "" {
				s += fmt.Sprintf("Aileron UI:  %s\n\n", linkStyle.Render(url))
			}
		}
		s += renderServiceStatuses(m.serviceStatuses, m.serviceStatusUpdatedAt)
		// The ↑/↓ cursor only shows while input's empty - once the operator
		// starts typing a number/word, that takes over on Enter (see the
		// screenMenu case in app_update.go), so highlighting a cursor row
		// that Enter would then ignore would be misleading.
		showCursor := m.input == ""
		for i, item := range menuOrder {
			cursor := "  "
			label := item
			if showCursor && i == m.menuCursor {
				cursor = cursorStyle.Render(">") + " "
				label = selectedStyle.Render(item)
			}
			s += fmt.Sprintf("  %s%s %s\n", cursor, menuKeyStyle.Render(fmt.Sprintf("%d.", i+1)), label)
		}
		s += fmt.Sprintf("\n%s %s_\n\n", promptStyle.Render(">"), m.input)
		s += hintBar([2]string{"↑/↓", "navigate"}, [2]string{"Enter", "select"}, [2]string{"Ctrl+C", "quit"}) + "\n"
		return s
	}
}
