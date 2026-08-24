package main

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"
)

// settingsChromeLines is the number of lines the Settings screen spends on
// its header, footer/help text, scroll indicators, and table borders/header
// row - i.e. everything around the field rows themselves. Subtracted from
// the terminal height to decide how many fields can be shown at once.
const settingsChromeLines = 16

// networkSetupLabel is the toggle row that expands/collapses the local
// physical network fields (interface, addressing, and static IP details)
// nested beneath it.
func networkSetupLabel(expanded bool) string {
	if expanded {
		return "▾ Local physical network setup"
	}
	return "▸ Local physical network setup"
}

// advancedSettingsLabel is the toggle row that expands/collapses the
// advanced (rarely-changed) fields nested beneath it.
func advancedSettingsLabel(expanded bool) string {
	if expanded {
		return "▾ Advanced settings"
	}
	return "▸ Advanced settings"
}

// settingsRow is one row in the Settings table: either a real settingField
// (nested, i.e. indented, if it belongs to a collapsible group) or one of
// the synthetic toggle/action rows.
type settingsRow struct {
	field           settingField
	isNetworkToggle bool
	isToggle        bool
	isApply         bool
	nested          bool
}

// settingsRows lays out the Settings table in display order:
//
//  1. The "Apply" row, always first - "configure"'s equivalent of the old
//     standalone "install" menu item. It sits above the rest of the fields
//     so it's always visible right away instead of buried at the bottom,
//     behind however many fields happen to be expanded.
//  2. The "Local physical network setup" toggle row (where
//     interface_name/addressing used to sit directly) - expanding it
//     reveals the interface and addressing fields, plus (only when
//     addressing is static) the static IP/prefix/gateway/DNS fields, all
//     nested one level.
//  3. Any remaining plain fields (automatic updates, k3s version, ...).
//  4. The "Advanced settings" toggle row, then its fields when expanded.
//
// Every toggle/action row's own position is fixed regardless of its
// group's expand state - only what's nested beneath it grows or shrinks -
// so toggling one never shifts the cursor onto a different field.
func (m model) settingsRows() []settingsRow {
	var plain []settingsRow
	var network []settingsRow
	var advanced []settingField

	for _, f := range settingFields {
		switch {
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

	rows := []settingsRow{{isApply: true}, {isNetworkToggle: true}}
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
		s := "\nConfirm Install\n\n"
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
			s += fmt.Sprintf("  %s  %s\n", fitCell(step.label, labelWidth), line)
		}
		s += fmt.Sprintf("\nType \"yes\" to proceed, or Esc to cancel:\n  %s\n", m.installConfirmInput.View())
		if m.installConfirmError != "" {
			s += fmt.Sprintf("\n%s\n", m.installConfirmError)
		}
		return s

	case screenInstall:
		s := "\nInstalling RudderVirt...\n\n"
		visible := m.installVisibleLogLines()
		lines := m.installLogs
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, line := range lines {
			s += line + "\n"
		}
		if m.installDone {
			s += "\nInstall complete. Press Esc to return to menu.\n"
		} else if m.installFailed {
			s += "\nInstall failed. Press Esc to return to menu.\n"
		} else if m.installStepIdx < len(installSteps) {
			s += fmt.Sprintf("\nRunning: %s...\n", installSteps[m.installStepIdx].label)
		}
		return s

	case screenResult:
		return fmt.Sprintf("\n%s\n\nPress Esc to go back, Ctrl+C to quit.\n", m.result)

	case screenUpdateChecking:
		return "\nChecking for updates...\n"

	case screenPasswordCheck:
		s := "\nChecking admin account credentials...\n"
		if m.passwordError != "" {
			s += fmt.Sprintf("\nError: %s\n\nPress Esc to go back, Ctrl+C to quit.\n", m.passwordError)
		}
		return s

	case screenPasswordChange:
		s := "\nChange Admin Password\n\n"
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
			s += "\nSaving...\n"
		} else if m.passwordError != "" {
			s += fmt.Sprintf("\nError: %s\n", m.passwordError)
		}
		s += "\nEnter to confirm each field, Esc to cancel.\n"
		return s

	case screenUpdateConfirm:
		s := "\nUpdate ruddervirt-setup\n\n"
		s += fmt.Sprintf("Current version: %s\nLatest version:  %s\n", version, m.updateLatestVersion)
		s += "\nThis will download and verify (SHA256) the new binary, then\n"
		s += "replace /usr/local/bin/ruddervirt-setup. The menu will restart\n"
		s += "on the new version afterward.\n"
		s += fmt.Sprintf("\nType \"yes\" to proceed, or Esc to cancel:\n  %s\n", m.updateConfirmInput.View())
		if m.updateConfirmError != "" {
			s += fmt.Sprintf("\n%s\n", m.updateConfirmError)
		}
		return s

	case screenUpdate:
		s := "\nUpdating ruddervirt-setup...\n\n"
		visible := m.installVisibleLogLines()
		lines := m.updateLogs
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, line := range lines {
			s += line + "\n"
		}
		if m.updateDone {
			s += "\nUpdate complete. Restarting into the new version...\n"
		} else if m.updateFailed {
			s += "\nUpdate failed. Press Esc to return to menu.\n"
		} else if m.updateStepIdx < len(updateSteps) {
			s += fmt.Sprintf("\nRunning: %s...\n", updateSteps[m.updateStepIdx].label)
		}
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

			topBorder := "┌" + strings.Repeat("─", width) + "┐"
			bottomBorder := "└" + strings.Repeat("─", width) + "┘"

			s := fmt.Sprintf("\nSettings\n\nSelect %s:\n\n", field.label)

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
				s += fmt.Sprintf("  ↑ %d more above\n", start)
			}

			s += topBorder + "\n"
			for i := start; i < end; i++ {
				cursor := "  "
				if i == m.settingsPickCursor {
					cursor = "> "
				}
				s += fmt.Sprintf("│%s%s│\n", cursor, fitCell(options[i], width-2))
			}
			s += bottomBorder + "\n"
			if end < len(options) {
				s += fmt.Sprintf("  ↓ %d more below\n", len(options)-end)
			}

			if m.settingsError != "" {
				s += fmt.Sprintf("\nError: %s\n", m.settingsError)
			}
			s += "\nEnter to select, Esc to cancel.\n"
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
			case r.isApply:
				return "▶ Apply (install/re-apply)"
			case r.nested:
				return "  " + r.field.label
			default:
				return r.field.label
			}
		}
		rowValue := func(r settingsRow) string {
			if r.isNetworkToggle || r.isToggle || r.isApply {
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

		topBorder := "┌───┬" + strings.Repeat("─", labelWidth+2) + "┬" + strings.Repeat("─", valueWidth+2) + "┐"
		sepBorder := "├───┼" + strings.Repeat("─", labelWidth+2) + "┼" + strings.Repeat("─", valueWidth+2) + "┤"
		bottomBorder := "└───┴" + strings.Repeat("─", labelWidth+2) + "┴" + strings.Repeat("─", valueWidth+2) + "┘"

		s := "\nSettings\n\n"

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
			s += fmt.Sprintf("  ↑ %d more above\n", start)
		}

		s += topBorder + "\n"
		s += fmt.Sprintf("│   │ %s │ %s │\n", fitCell("Setting", labelWidth), fitCell("Value", valueWidth))
		s += sepBorder + "\n"
		for i := start; i < end; i++ {
			cursor := "   "
			if i == m.settingsCursor {
				cursor = " > "
			}
			r := rows[i]
			s += fmt.Sprintf("│%s│ %s │ %s │\n", cursor, fitCell(rowLabel(r), labelWidth), fitCell(rowValue(r), valueWidth))
		}
		s += bottomBorder + "\n"

		if end < total {
			s += fmt.Sprintf("  ↓ %d more below\n", total-end)
		}

		if m.settingsEditing {
			s += fmt.Sprintf("\nEditing %s:\n  %s\n", rows[m.settingsCursor].field.label, m.settingsInput.View())
			if m.settingsError != "" {
				s += fmt.Sprintf("\nError: %s\n", m.settingsError)
			}
			s += "\nEnter to save, Esc to cancel.\n"
		} else {
			if m.settingsSaving {
				s += "\nSaving...\n"
			} else if m.settingsError != "" {
				s += fmt.Sprintf("\nError saving: %s\n", m.settingsError)
			}
			s += "\nUp/Down to select, Enter to edit/choose, Esc to go back.\n"
		}
		return s

	default:
		s := "\nRudderVirt OS\n\n"
		if configSaved() {
			if url := aileronUIURL(m.cfg); url != "" {
				s += fmt.Sprintf("Aileron UI:  %s\n\n", url)
			}
		}
		s += renderServiceStatuses(m.serviceStatuses)
		s += "  1. configure\n"
		s += "  2. k9s\n"
		s += "  3. shell\n"
		s += "  4. update\n"
		s += "  5. logout\n"
		s += fmt.Sprintf("\n> %s_\n\n", m.input)
		s += "Press ctrl+c to quit.\n"
		return s
	}
}
