// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/mattn/go-runewidth"
)

// stabilizerFieldScreenInfo returns the title/label/input/help text for
// whichever of the "Adopt to ruddervirt.com" wizard's plain-text input
// screens m.current currently is (screenStabilizerZone/NatsPassword/
// Nebula) - factored out since those screens share an identical
// single-field layout, differing only in these details. Every value here
// is something ruddervirt provides, not something the operator picks or
// needs explained - so the help text is deliberately just "enter the value
// provided by ruddervirt for X", not a description of what X does.
func stabilizerFieldScreenInfo(m model) (title, label string, input textinput.Model, help string) {
	switch m.current {
	case screenStabilizerZone:
		return "Adopt to ruddervirt.com: Zone Name", "Zone name", m.stabilizerZoneInput,
			"Enter the value provided by ruddervirt for zone name.\n\n"
	case screenStabilizerNatsPassword:
		return "Adopt to ruddervirt.com: NATS Password", "NATS password", m.stabilizerNatsPasswordInput,
			"Enter the value provided by ruddervirt for the NATS password.\n\n"
	case screenStabilizerNebula:
		return "Adopt to ruddervirt.com: Nebula Mesh Config", "Path or URL", m.stabilizerNebulaInput,
			"Enter the value (a local file path or http(s):// URL) provided by\nruddervirt for the nebula mesh config.\n\n"
	default:
		return "", "", textinput.Model{}, ""
	}
}

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
	// isStabilizerAction marks the synthetic "Adopt to ruddervirt.com"
	// action row - see settingsRows below.
	isStabilizerAction bool
	// stabilizerDef marks a row backed by LIVE stabilizer cluster state
	// (stabilizerSettingsState, model.go) instead of Config/settingField -
	// non-nil once stabilizer has been adopted, one per entry in
	// stabilizerSettingDefs (stabilizer-settings.yaml). See settingsRows
	// below for exactly where these get inserted, and app_update.go's
	// `row.stabilizerDef != nil` branches for how picking/editing them
	// differs from an ordinary settingField row.
	stabilizerDef *stabilizerSettingDef
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
		case f.key == "system.aileron_ui_enabled" && m.cachedStabilizerDetected:
			// Once stabilizer manages Aileron, this Config-backed field is
			// permanently locked (stabilizerLocked, config.go) - showing it
			// AND a second, live-backed "aileron_ui_enabled" row (from
			// stabilizerSettingDefs) further down would put the same
			// setting in two places, one of them dead. Substitute the live
			// row in this field's own slot instead, so there's exactly one
			// place to find/change it, and it's actually editable again.
			if def, ok := stabilizerSettingByKey("aileron_ui_enabled"); ok {
				plain = append(plain, settingsRow{stabilizerDef: &def})
			}
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
		if m.cachedStabilizerDetected {
			// Every stabilizer setting EXCEPT aileron_ui_enabled, which
			// already has its own row above (in system.aileron_ui_enabled's
			// usual plain-row slot) - never list it twice.
			for i := range stabilizerSettingDefs {
				d := stabilizerSettingDefs[i]
				if d.Key == "aileron_ui_enabled" {
					continue
				}
				rows = append(rows, settingsRow{stabilizerDef: &d, nested: true})
			}
		} else {
			// Nested under Advanced, not a plain row - this is a rare,
			// high-consequence, coordinated-with-ruddervirt action, not
			// something that belongs alongside everyday settings.
			rows = append(rows, settingsRow{isStabilizerAction: true, nested: true})
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
	isOSUpdate   bool
	field        settingField
}

// updateVersionsRows lists the Update screen's rows: ruddervirt-setup and
// the operating system first, then every settingField tagged updateScreen,
// in settingFields' order. Apply isn't one of these rows - same
// fixed-footer treatment as Settings' Apply (see settingsRows), one cursor
// position past the last row here.
func updateVersionsRows() []updateVersionsRow {
	rows := []updateVersionsRow{{isSelfUpdate: true}, {isOSUpdate: true}}
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

	case screenHostnameChange:
		s := "\n" + titleStyle.Render("Set System Hostname") + "\n\n"
		s += "This node is still using the default hostname. Set one that\n"
		s += "identifies it on your network before continuing - the hostname\n"
		s += "cannot be changed once installation proceeds, so this cannot\n"
		s += "be skipped.\n\n"
		s += fmt.Sprintf("Hostname:  %s\n", m.hostnameInput.View())
		if m.hostnameSaving {
			s += "\n" + helpStyle.Render("Saving...") + "\n"
		} else if m.hostnameError != "" {
			s += fmt.Sprintf("\n%s\n", errorStyle.Render("Error: "+m.hostnameError))
		}
		s += "\n" + hintBar([2]string{"Enter", "continue"}, [2]string{"Esc", "cancel"}) + "\n"
		return s

	case screenStabilizerAileronCheck:
		return "\nChecking that aileron is installed and running...\n"

	case screenStabilizerWarning:
		s := "\n" + titleStyle.Render("Adopt to ruddervirt.com") + "\n\n"
		s += errorStyle.Render("This cannot be done without coordination from selfhosted@ruddervirt.com.") + "\n\n"
		s += "ruddervirt needs to provide secrets (zone name, NATS password, and a\n"
		s += "Nebula mesh config) before this will work - contact\n"
		s += "selfhosted@ruddervirt.com first if you haven't already.\n"
		s += "\n" + hintBar([2]string{"Enter", "continue"}, [2]string{"Esc", "cancel"}) + "\n"
		return s

	case screenStabilizerZone, screenStabilizerNatsPassword, screenStabilizerNebula:
		title, label, input, help := stabilizerFieldScreenInfo(m)
		s := "\n" + titleStyle.Render(title) + "\n\n"
		s += help
		s += fmt.Sprintf("%s:  %s\n", label, input.View())
		if m.stabilizerNebulaResolving {
			s += "\n" + helpStyle.Render("Fetching...") + "\n"
		} else if m.stabilizerError != "" {
			s += fmt.Sprintf("\n%s\n", errorStyle.Render("Error: "+m.stabilizerError))
		}
		s += "\n" + hintBar([2]string{"Enter", "continue"}, [2]string{"Esc", "cancel"}) + "\n"
		return s

	case screenStabilizerPlanning:
		return "\nComputing adoption plan...\n"

	case screenStabilizerConfirm:
		s := "\n" + titleStyle.Render("Confirm Adoption to ruddervirt.com") + "\n\n"
		s += fmt.Sprintf("Zone: %s\nNATS: %s\nNATS user: %s (= zone name)\n\n", m.cfg.Stabilizer.Zone, defaultStabilizerNatsURL, m.cfg.Stabilizer.Zone)
		s += fmt.Sprintf("This will create the %s and %s Secrets in %s, then:\n", natsAuthSecretName, nebulaSecretName, stabilizerNamespace)
		if m.stabilizerWillAdopt {
			s += "  adopt the existing standalone Aileron release - its operator,\n"
			s += "  vncgateway, and UI Deployments will be deleted and recreated\n"
			s += "  (a few seconds of console/API disruption; running VMs are\n"
			s += "  unaffected).\n"
		} else {
			s += "  install stabilizer fresh (no standalone Aileron release was found\n"
			s += "  to adopt).\n"
		}
		s += "then apply the stabilizer chart and wait for it to become ready.\n"
		s += fmt.Sprintf("\n%s\n  %s\n", helpStyle.Render("Type \"yes\" to proceed, or Esc to cancel:"), m.stabilizerConfirmInput.View())
		if m.stabilizerConfirmError != "" {
			s += fmt.Sprintf("\n%s\n", errorStyle.Render(m.stabilizerConfirmError))
		}
		return s

	case screenStabilizerAdopt:
		s := "\n" + titleStyle.Render("Adopting to ruddervirt.com...") + "\n\n"
		visible := m.installVisibleLogLines()
		lines := m.stabilizerLogs
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, line := range lines {
			s += line + "\n"
		}
		if m.stabilizerDone {
			s += "\n" + successStyle.Render("This node is now connected to ruddervirt.com.") + " " + helpStyle.Render("Press Esc to return to menu.") + "\n"
		} else if m.stabilizerFailed {
			s += "\n" + errorStyle.Render("Adoption failed.") + " " + helpStyle.Render("Press Esc to return to menu.") + "\n"
		} else if m.stabilizerStepIdx < len(stabilizerSteps) {
			s += "\n" + helpStyle.Render(fmt.Sprintf("Running: %s...", stabilizerSteps[m.stabilizerStepIdx].label)) + "\n"
		}
		return s

	case screenStabilizerSettingsConfirm:
		def := m.stabilizerSettingsPendingDef
		curDisplay := "(not set - chart default)"
		if m.stabilizerSettingsPendingCurrent != nil {
			curDisplay = formatStabilizerSettingValue(def, m.stabilizerSettingsPendingCurrent)
		}
		s := "\n" + titleStyle.Render("Confirm Stabilizer Setting Change") + "\n\n"
		s += fmt.Sprintf("%s:  %s -> %s\n\n", def.Key, curDisplay, formatStabilizerSettingValue(def, m.stabilizerSettingsPendingValue))
		s += "Applying this RESTARTS THE WHOLE RELEASE: stabilizer, vncauthproxy, the\n"
		s += "aileron operator, and the VNC gateway all restart (roughly 30-90 seconds).\n"
		s += "Consoles drop and the zone goes quiet to the cloud UI during that window.\n"
		s += "Running VMs are NOT affected. This is not a hot-reload.\n"
		s += fmt.Sprintf("\n%s\n  %s\n", helpStyle.Render("Type \"yes\" to proceed, or Esc to cancel:"), m.stabilizerSettingsConfirmInput.View())
		if m.stabilizerSettingsConfirmError != "" {
			s += fmt.Sprintf("\n%s\n", errorStyle.Render(m.stabilizerSettingsConfirmError))
		}
		return s

	case screenStabilizerSettingsApply:
		s := "\n" + titleStyle.Render(fmt.Sprintf("Applying %s...", m.stabilizerSettingsPendingDef.Key)) + "\n\n"
		visible := m.installVisibleLogLines()
		lines := m.stabilizerSettingsApplyLogs
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, line := range lines {
			s += line + "\n"
		}
		if m.stabilizerSettingsApplyDone {
			s += "\n" + successStyle.Render("Applied and rolled out.") + " " + helpStyle.Render("Press Esc to return to Settings.") + "\n"
		} else if m.stabilizerSettingsApplyFailed {
			s += "\n" + errorStyle.Render("Failed.") + " " + helpStyle.Render("Press Esc to return to Settings.") + "\n"
		} else if m.stabilizerSettingsApplyStepIdx < len(m.stabilizerSettingsApplyPipeline) {
			s += "\n" + helpStyle.Render(fmt.Sprintf("Running: %s...", m.stabilizerSettingsApplyPipeline[m.stabilizerSettingsApplyStepIdx].label)) + "\n"
		}
		return s

	case screenStabilizerVersionConfirm:
		s := "\n" + titleStyle.Render("Confirm Version Change") + "\n\n"
		current := "(unset)"
		if m.stabilizerSettingsState != nil && m.stabilizerSettingsState.declaredVersion != "" {
			current = m.stabilizerSettingsState.declaredVersion
		}
		s += fmt.Sprintf("%s -> %s\n", current, m.stabilizerVersionTarget)
		if len(m.stabilizerVersionClearedPins) > 0 {
			s += "\nAlso clearing these redundant image pins (restating this release's own tag):\n"
			for _, p := range m.stabilizerVersionClearedPins {
				s += "  " + p + "\n"
			}
		}
		s += "\nApplying this RESTARTS THE WHOLE RELEASE: stabilizer, vncauthproxy, the\n"
		s += "aileron operator, and the VNC gateway all restart (roughly 30-90 seconds).\n"
		s += "Consoles drop and the zone goes quiet to the cloud UI during that window.\n"
		s += "Running VMs are NOT affected. This is not a hot-reload.\n"
		s += fmt.Sprintf("\n%s\n  %s\n", helpStyle.Render("Type \"yes\" to proceed, or Esc to cancel:"), m.stabilizerVersionConfirmInput.View())
		if m.stabilizerVersionConfirmError != "" {
			s += fmt.Sprintf("\n%s\n", errorStyle.Render(m.stabilizerVersionConfirmError))
		}
		return s

	case screenStabilizerVersionApply:
		s := "\n" + titleStyle.Render(fmt.Sprintf("Patching to %s...", m.stabilizerVersionTarget)) + "\n\n"
		visible := m.installVisibleLogLines()
		lines := m.stabilizerVersionApplyLogs
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, line := range lines {
			s += line + "\n"
		}
		if m.stabilizerVersionApplyDone {
			s += "\n" + successStyle.Render("Applied and rolled out.") + " " + helpStyle.Render("Press Esc to return to Update.") + "\n"
		} else if m.stabilizerVersionApplyFailed {
			s += "\n" + errorStyle.Render("Failed.") + " " + helpStyle.Render("Press Esc to return to Update.") + "\n"
		} else if m.stabilizerVersionApplyStepIdx < len(m.stabilizerVersionApplyPipeline) {
			s += "\n" + helpStyle.Render(fmt.Sprintf("Running: %s...", m.stabilizerVersionApplyPipeline[m.stabilizerVersionApplyStepIdx].label)) + "\n"
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

	case screenOSUpdate:
		s := "\n" + titleStyle.Render("Updating operating system...") + "\n\n"
		visible := m.installVisibleLogLines()
		lines := m.osUpdateLogs
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, line := range lines {
			s += line + "\n"
		}
		if m.osUpdateDone {
			s += "\n" + successStyle.Render("Staged. Reboot to switch into the new deployment.") + " " + helpStyle.Render("Press Esc to return to Update.") + "\n"
		} else if m.osUpdateFailed {
			s += "\n" + errorStyle.Render("Update failed.") + " " + helpStyle.Render("Press Esc to return to Update.") + "\n"
		} else if m.osUpdateStepIdx < len(osUpdateSteps) {
			s += "\n" + helpStyle.Render(fmt.Sprintf("Running: %s...", osUpdateSteps[m.osUpdateStepIdx].label)) + "\n"
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

		versions := versionCache{K3s: m.cachedK3sVersions, Aileron: m.cachedAileronVersions, StabilizerDetected: m.cachedStabilizerDetected}

		// rowHasUpgrade reports whether r has something newer than what's
		// currently configured/running to offer - generalized across every
		// settingField row via its own options func (which each already
		// filters to "no downgrades from current", so "any option isn't the
		// current value" means "an upgrade exists") rather than duplicating
		// each component's own version comparator here; the two rows that
		// aren't settingFields (ruddervirt-setup, the OS) and the
		// stabilizer-managed Aileron case (whose options come from a
		// different source entirely - see stabilizerVersionPickerOptions)
		// are special-cased instead.
		rowHasUpgrade := func(r updateVersionsRow) bool {
			if r.isSelfUpdate {
				return m.cachedSelfUpdateAvailable
			}
			if r.isOSUpdate {
				return m.cachedOSUpdateAvailable
			}
			if r.field.key == "versions.aileron" && versions.StabilizerDetected {
				if m.stabilizerSettingsState == nil {
					return false
				}
				for _, o := range stabilizerVersionPickerOptions(m.cachedAileronVersions, m.stabilizerSettingsState.declaredVersion) {
					if strings.TrimPrefix(o, "v") != m.stabilizerSettingsState.declaredVersion {
						return true
					}
				}
				return false
			}
			if r.field.locked != nil {
				if locked, _ := r.field.locked(&m.cfg, versions); locked {
					return false
				}
			}
			if r.field.options == nil {
				return false
			}
			current := r.field.get(&m.cfg)
			for _, o := range r.field.options(&m.cfg, versions) {
				if o != current {
					return true
				}
			}
			return false
		}

		// updateRowIconPrefix is a fixed-width (2 visual columns) plain-text
		// prefix baked into the label BEFORE fitCell/width computation, so
		// alignment never shifts depending on which rows happen to have an
		// upgrade - color is applied after, same "style only after fitCell"
		// rule the rest of this table already follows (see colorToggleArrow
		// below).
		updateRowIconPrefix := func(r updateVersionsRow) string {
			if rowHasUpgrade(r) {
				return "↑ "
			}
			return "  "
		}

		rowLabel := func(r updateVersionsRow) string {
			label := ""
			switch {
			case r.isSelfUpdate:
				label = "ruddervirt-setup"
			case r.isOSUpdate:
				label = "Operating system"
			default:
				label = r.field.label
			}
			return updateRowIconPrefix(r) + label
		}
		rowValue := func(r updateVersionsRow) string {
			if r.isSelfUpdate {
				return version
			}
			if r.isOSUpdate {
				if v := currentOSVersion(); v != "" {
					return v
				}
				return "-"
			}
			if r.field.key == "versions.aileron" && versions.StabilizerDetected {
				// Once stabilizer manages it, this shows the chart version
				// actually installed (CHART_VERSION) as the primary value,
				// with what the HelmChart resource is asking for appended
				// only when a rollout is still landing or has failed - same
				// applied-vs-declared convention as stabilizerSettingRowDisplay
				// (stabilizer_settings_tui.go).
				if m.stabilizerSettingsState == nil {
					return "loading..."
				}
				applied := m.stabilizerSettingsState.appliedChartVersion
				declared := m.stabilizerSettingsState.declaredVersion
				if applied == "" {
					applied = "(unknown)"
				}
				if declared != "" && declared != applied {
					return fmt.Sprintf("%s (rollout pending -> %s)", applied, declared)
				}
				return applied
			}
			if r.field.locked != nil {
				if locked, reason := r.field.locked(&m.cfg, versions); locked {
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
			} else if rowHasUpgrade(r) {
				label = colorUpdateIcon(label)
			}
			s += fmt.Sprintf("%s%s%s %s %s %s %s\n", colorBorders("│"), cursorArrow(selected), colorBorders("│"), label, colorBorders("│"), value, colorBorders("│"))
		}
		s += colorBorders(bottomBorder) + "\n"

		if end < total {
			s += helpStyle.Render(fmt.Sprintf("  ↓ %d more below", total-end)) + "\n"
		}
		s += helpStyle.Render("  ↑ marks an available upgrade") + "\n"

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
			row := m.settingsRows()[m.settingsCursor]
			pickLabel := row.field.label
			if row.stabilizerDef != nil {
				pickLabel = row.stabilizerDef.Key
			}
			options := m.settingsPickOptions

			width := runewidth.StringWidth(pickLabel) + 4
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

			s := "\n" + titleStyle.Render("Settings") + fmt.Sprintf("\n\nSelect %s:\n\n", pickLabel)
			if row.stabilizerDef != nil {
				// Stabilizer/Aileron settings come from stabilizer-settings.yaml
				// with no in-app documentation elsewhere in this screen (unlike
				// a Config-backed field, whose label is usually self-explanatory)
				// - show what it does before asking the operator to pick a
				// value for it.
				s += wrapHelp(row.stabilizerDef.Summary, m.termWidth) + "\n"
				if row.stabilizerDef.Detail != "" && row.stabilizerDef.Detail != row.stabilizerDef.Summary {
					s += wrapHelp(row.stabilizerDef.Detail, m.termWidth) + "\n"
				}
				s += "\n"
			}

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
			case r.isStabilizerAction:
				// Nested (see settingsRows), so indented like every other
				// advanced field for consistent alignment.
				return "  Adopt to ruddervirt.com"
			case r.stabilizerDef != nil && r.nested:
				return "  " + r.stabilizerDef.Key
			case r.stabilizerDef != nil:
				return r.stabilizerDef.Key
			case r.nested:
				return "  " + r.field.label
			default:
				return r.field.label
			}
		}
		versions := versionCache{K3s: m.cachedK3sVersions, Aileron: m.cachedAileronVersions, StabilizerDetected: m.cachedStabilizerDetected}
		rowValue := func(r settingsRow) string {
			if r.isNetworkToggle || r.isToggle || r.isStabilizerAction {
				return ""
			}
			if r.stabilizerDef != nil {
				if m.stabilizerSettingsState == nil {
					return "loading..."
				}
				value, _ := stabilizerSettingListValue(*r.stabilizerDef, m.stabilizerSettingsState)
				return value
			}
			if r.field.locked != nil {
				if locked, reason := r.field.locked(&m.cfg, versions); locked {
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
			editRow := rows[m.settingsCursor]
			if editRow.stabilizerDef != nil {
				def := editRow.stabilizerDef
				hint := ""
				if def.hasUnlimited() {
					hint = " (or \"unlimited\")"
				}
				s += "\n" + wrapHelp(def.Summary, m.termWidth) + "\n"
				if def.Detail != "" && def.Detail != def.Summary {
					s += wrapHelp(def.Detail, m.termWidth) + "\n"
				}
				s += fmt.Sprintf("\nEnter the value provided by ruddervirt for %s%s:\n  %s\n", def.Key, hint, m.settingsInput.View())
			} else {
				s += fmt.Sprintf("\nEditing %s:\n  %s\n", editRow.field.label, m.settingsInput.View())
			}
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
		if m.cachedStabilizerDetected {
			s += helpStyle.Render("This system is connected to and managed by ruddervirt.com") + "\n\n"
		}
		if configSaved() {
			if url := aileronUIURL(m.cfg); url != "" {
				s += fmt.Sprintf("Aileron UI:  %s\n\n", linkStyle.Render(url))
			}
		}
		statusUpdatedAt := m.serviceStatusUpdatedAt
		if m.hostStatsUpdatedAt.After(statusUpdatedAt) {
			statusUpdatedAt = m.hostStatsUpdatedAt
		}
		s += renderHomeStatus(m.serviceStatuses, m.hostStats, statusUpdatedAt, m.termWidth)
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
