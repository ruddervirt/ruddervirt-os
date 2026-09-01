// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/stabilizer/settings"
	"ruddervirt-setup/internal/tui"
)

// SettingsModel is the Settings screen's sub-model (screenSettings) - the
// cursor/scroll/editing/picking UI state for browsing and editing
// config.SettingFields (plus, in situ, live stabilizer-cluster-backed
// settings once adopted - see Rows below).
//
// Deliberately has no Saving/Error fields, unlike PasswordModel/
// HostnameModel: screenUpdateVersions (UpdateModel) reads AND writes the
// same "saving.../error" feedback (settingsSaving/settingsError in
// model.go). Anything read by another screen stays on the root Model, so
// those two stay root-owned and are threaded through View via
// SettingsViewParams instead of duplicated here.
type SettingsModel struct {
	Cursor       int
	Scroll       int
	Editing      bool
	ShowAdvanced bool
	ShowNetwork  bool
	Input        textinput.Model

	// Picker sub-mode for select-type fields (config.SettingField.Options !=
	// nil) and boolean stabilizer settings: Enter opens it instead of the
	// free-text edit box above.
	Picking     bool
	PickCursor  int
	PickScroll  int
	PickOptions []string
}

// SettingsRow is one row in the Settings table: either a real
// config.SettingField (nested, i.e. indented, if it belongs to a
// collapsible group), a row backed by LIVE stabilizer cluster state
// (StabilizerDef - non-nil once stabilizer has been adopted, one per entry
// in settings.StabilizerSettingDefs) instead of config.Config/
// config.SettingField, or one of the synthetic toggle/action rows.
type SettingsRow struct {
	Field           config.SettingField
	IsNetworkToggle bool
	IsToggle        bool
	Nested          bool
	// IsStabilizerAction marks the synthetic "Adopt to ruddervirt.com"
	// action row - see Rows below.
	IsStabilizerAction bool
	StabilizerDef      *settings.StabilizerSettingDef
}

// NetworkSetupLabel is the toggle row that expands/collapses the local
// physical network fields (interface, addressing, and static IP details)
// nested beneath it.
func NetworkSetupLabel(expanded bool) string {
	if expanded {
		return "▼ Local physical network setup"
	}
	return "▶ Local physical network setup"
}

// AdvancedSettingsLabel is the toggle row that expands/collapses the
// advanced (rarely-changed) fields nested beneath it.
func AdvancedSettingsLabel(expanded bool) string {
	if expanded {
		return "▼ Advanced settings"
	}
	return "▶ Advanced settings"
}

// Rows lays out the Settings table in display order, taking
// cfg/stabilizerDetected as parameters instead of reaching for root Model
// fields directly (both are read by other screen groups too - see this
// file's doc comment):
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
// bar below the table (see View), one cursor position past the last row
// here (ScrollCursor). That keeps it reachable by pressing Down past the
// end of the list while always staying visible at the bottom of the page,
// regardless of scroll.
func (m SettingsModel) Rows(cfg config.Config, stabilizerDetected bool) []SettingsRow {
	var plain []SettingsRow
	var network []SettingsRow
	var advanced []config.SettingField

	for _, f := range config.SettingFields {
		switch {
		case f.UpdateScreen:
			continue // lives on the Update screen instead
		case f.Key == "system.aileron_ui_enabled" && stabilizerDetected:
			// Once stabilizer manages Aileron, this config.Config-backed
			// field is permanently locked - substitute the live row in
			// this field's own slot instead, so there's exactly one place
			// to find/change it, and it's actually editable again.
			if def, ok := settings.StabilizerSettingByKey("aileron_ui_enabled"); ok {
				plain = append(plain, SettingsRow{StabilizerDef: &def})
			}
		case f.Advanced:
			advanced = append(advanced, f)
		case f.NetworkSetup:
			network = append(network, SettingsRow{Field: f, Nested: true})
		case f.StaticOnly:
			if cfg.Network.Addressing == "static" {
				network = append(network, SettingsRow{Field: f, Nested: true})
			}
		default:
			plain = append(plain, SettingsRow{Field: f})
		}
	}

	rows := []SettingsRow{{IsNetworkToggle: true}}
	if m.ShowNetwork {
		rows = append(rows, network...)
	}
	rows = append(rows, plain...)
	rows = append(rows, SettingsRow{IsToggle: true})
	if m.ShowAdvanced {
		for _, f := range advanced {
			rows = append(rows, SettingsRow{Field: f, Nested: true})
		}
		if stabilizerDetected {
			// Every stabilizer setting EXCEPT aileron_ui_enabled, which
			// already has its own row above - never list it twice.
			for i := range settings.StabilizerSettingDefs {
				d := settings.StabilizerSettingDefs[i]
				if d.Key == "aileron_ui_enabled" {
					continue
				}
				rows = append(rows, SettingsRow{StabilizerDef: &d, Nested: true})
			}
		} else {
			// Nested under Advanced, not a plain row - this is a rare,
			// high-consequence, coordinated-with-ruddervirt action, not
			// something that belongs alongside everyday settings.
			rows = append(rows, SettingsRow{IsStabilizerAction: true, Nested: true})
		}
	}
	return rows
}

// ScrollCursor caps Cursor at the last real table row for scroll-window
// purposes - Apply lives in its own fixed footer below the table (see
// Rows), never inside the scrolling window, so it should never pull the
// table into scrolling one row further than its content requires.
func (m SettingsModel) ScrollCursor(cfg config.Config, stabilizerDetected bool) int {
	if last := len(m.Rows(cfg, stabilizerDetected)) - 1; m.Cursor > last {
		return last
	}
	return m.Cursor
}

// Up moves the cursor (picker or table, whichever is active) up by one,
// re-clamping scroll - called directly from app.go on tea.KeyUp while
// screenSettings is current, same named-navigation-method shape as
// MenuModel.Up. A no-op while Editing (the free-text input owns arrow keys
// then, same as every other edit box in this app).
func (m SettingsModel) Up(cfg config.Config, stabilizerDetected bool, visibleRows int) SettingsModel {
	if m.Picking {
		if m.PickCursor > 0 {
			m.PickCursor--
		}
		m.PickScroll = clampScroll(m.PickScroll, m.PickCursor, visibleRows)
		return m
	}
	if !m.Editing {
		if m.Cursor > 0 {
			m.Cursor--
		}
		m.Scroll = clampScroll(m.Scroll, m.ScrollCursor(cfg, stabilizerDetected), visibleRows)
	}
	return m
}

// Down mirrors Up - see its doc comment. len(Rows(...)), not -1: one
// cursor position past the last real row lands on Apply, the fixed action
// bar pinned below the table (see ScrollCursor).
func (m SettingsModel) Down(cfg config.Config, stabilizerDetected bool, visibleRows int) SettingsModel {
	if m.Picking {
		if m.PickCursor < len(m.PickOptions)-1 {
			m.PickCursor++
		}
		m.PickScroll = clampScroll(m.PickScroll, m.PickCursor, visibleRows)
		return m
	}
	if !m.Editing {
		if m.Cursor < len(m.Rows(cfg, stabilizerDetected)) {
			m.Cursor++
		}
		m.Scroll = clampScroll(m.Scroll, m.ScrollCursor(cfg, stabilizerDetected), visibleRows)
	}
	return m
}

// Reset clears Cursor/Scroll/Editing/ShowAdvanced/ShowNetwork back to the
// screen's just-entered state - called from app.go every time Settings is
// (re)entered from elsewhere (a fresh landing, not the in-place
// Picking/Editing cancel Esc already handles). Deliberately leaves
// Picking/PickCursor/PickScroll/Input/PickOptions untouched: every call
// site reaches this only after those were already zeroed by an earlier
// full reset (see package doc on the generic cross-group Esc handler),
// which clears them explicitly alongside this call instead.
func (m SettingsModel) Reset() SettingsModel {
	m.Cursor = 0
	m.Scroll = 0
	m.Editing = false
	m.ShowAdvanced = false
	m.ShowNetwork = false
	return m
}

// Update forwards key presses to Input. screenSettings' own arrow-key
// list/picker navigation (Up/Down above) and its Enter-key dispatch (which
// touches cross-group state: cfg, the version/stabilizer caches, and other
// screens' confirm flows - installConfirmOrigin,
// screenStabilizerAileronCheck, screenStabilizerSettingsConfirm) stay in
// app.go, called directly against this struct's fields - same convention
// as PasswordModel's Esc handling.
func (m SettingsModel) Update(msg tea.Msg) (SettingsModel, tea.Cmd) {
	var cmd tea.Cmd
	m.Input, cmd = m.Input.Update(msg)
	return m, cmd
}

// SettingsViewParams bundles the cross-group state View needs beyond m
// itself - cfg, the version/stabilizer caches, and the shared saving/error
// feedback text (also written by screenUpdateVersions, see SettingsModel's
// doc comment) - so it stays on the root Model rather than being
// duplicated onto this struct.
type SettingsViewParams struct {
	Cfg                     config.Config
	Versions                config.VersionCache
	StabilizerSettingsState *settings.StabilizerSettingsState
	// StabilizerValue computes a StabilizerDef row's display value - a
	// callback rather than a direct internal/stabilizer/settings call, since
	// the underlying logic (stabilizerSettingListValue) deliberately stays a
	// package-main bridge function in stabilizer_settings_tui.go (see
	// StabilizerSettingsModel's doc comment for why).
	StabilizerValue func(settings.StabilizerSettingDef, *settings.StabilizerSettingsState) string
	Error           string
	Saving          bool
	TermWidth       int
	VisibleRows     int
}

// View renders screenSettings - both the picker overlay and the main
// table/edit-prompt view.
func (m SettingsModel) View(p SettingsViewParams) string {
	if m.Picking {
		row := m.Rows(p.Cfg, p.Versions.StabilizerDetected)[m.Cursor]
		pickLabel := row.Field.Label
		if row.StabilizerDef != nil {
			pickLabel = row.StabilizerDef.Key
		}
		options := m.PickOptions

		width := runewidth.StringWidth(pickLabel) + 4
		for _, o := range options {
			if l := runewidth.StringWidth(o) + 4; l > width {
				width = l
			}
		}
		termWidth := p.TermWidth
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

		s := "\n" + tui.TitleStyle.Render("Settings") + fmt.Sprintf("\n\nSelect %s:\n\n", pickLabel)
		if row.StabilizerDef != nil {
			// Stabilizer/Aileron settings come from stabilizer-settings.yaml
			// with no in-app documentation elsewhere in this screen (unlike
			// a config.Config-backed field, whose label is usually
			// self-explanatory) - show what it does before asking the
			// operator to pick a value for it.
			s += tui.WrapHelp(row.StabilizerDef.Summary, p.TermWidth) + "\n"
			if row.StabilizerDef.Detail != "" && row.StabilizerDef.Detail != row.StabilizerDef.Summary {
				s += tui.WrapHelp(row.StabilizerDef.Detail, p.TermWidth) + "\n"
			}
			s += "\n"
		}

		visible := p.VisibleRows
		start := m.PickScroll
		end := start + visible
		if end > len(options) {
			end = len(options)
		}
		if start > end {
			start = end
		}
		if start > 0 {
			s += tui.HelpStyle.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n"
		}

		s += tui.ColorBorders(topBorder) + "\n"
		for i := start; i < end; i++ {
			selected := i == m.PickCursor
			cursor := "  "
			cell := fitCell(options[i], width-2)
			if selected {
				cursor = tui.CursorStyle.Render(">") + " "
				cell = tui.SelectedStyle.Render(cell)
			}
			s += fmt.Sprintf("%s%s%s%s\n", tui.ColorBorders("│"), cursor, cell, tui.ColorBorders("│"))
		}
		s += tui.ColorBorders(bottomBorder) + "\n"
		if end < len(options) {
			s += tui.HelpStyle.Render(fmt.Sprintf("  ↓ %d more below", len(options)-end)) + "\n"
		}

		if p.Error != "" {
			s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render("Error: "+p.Error))
		}
		s += "\n" + tui.HintBar([2]string{"↑/↓", "navigate"}, [2]string{"Enter", "select"}, [2]string{"Esc", "cancel"}) + "\n"
		return s
	}

	rows := m.Rows(p.Cfg, p.Versions.StabilizerDetected)
	total := len(rows)

	rowLabel := func(r SettingsRow) string {
		switch {
		case r.IsNetworkToggle:
			return NetworkSetupLabel(m.ShowNetwork)
		case r.IsToggle:
			return AdvancedSettingsLabel(m.ShowAdvanced)
		case r.IsStabilizerAction:
			// Nested (see Rows), so indented like every other advanced
			// field for consistent alignment.
			return "  Adopt to ruddervirt.com"
		case r.StabilizerDef != nil && r.Nested:
			return "  " + r.StabilizerDef.Key
		case r.StabilizerDef != nil:
			return r.StabilizerDef.Key
		case r.Nested:
			return "  " + r.Field.Label
		default:
			return r.Field.Label
		}
	}
	cfg := p.Cfg
	rowValue := func(r SettingsRow) string {
		if r.IsNetworkToggle || r.IsToggle || r.IsStabilizerAction {
			return ""
		}
		if r.StabilizerDef != nil {
			if p.StabilizerSettingsState == nil {
				return "loading..."
			}
			return p.StabilizerValue(*r.StabilizerDef, p.StabilizerSettingsState)
		}
		if r.Field.Locked != nil {
			if locked, reason := r.Field.Locked(&cfg, p.Versions); locked {
				return reason
			}
		}
		return r.Field.Get(&cfg)
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

	termWidth := p.TermWidth
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

	s := "\n" + tui.TitleStyle.Render("Settings") + "\n\n"

	visible := p.VisibleRows
	start := m.Scroll
	end := start + visible
	if end > total {
		end = total
	}
	if start > end {
		start = end
	}
	if start > 0 {
		s += tui.HelpStyle.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n"
	}

	s += tui.ColorBorders(topBorder) + "\n"
	s += tui.ColorBorders(fmt.Sprintf("│   │ %s │ %s │", fitCell("Setting", labelWidth), fitCell("Value", valueWidth))) + "\n"
	s += tui.ColorBorders(sepBorder) + "\n"
	for i := start; i < end; i++ {
		selected := i == m.Cursor
		r := rows[i]
		label := fitCell(rowLabel(r), labelWidth)
		value := fitCell(rowValue(r), valueWidth)
		if selected {
			label = tui.SelectedStyle.Render(label)
			value = tui.SelectedStyle.Render(value)
		} else if r.IsNetworkToggle || r.IsToggle {
			label = tui.ColorToggleArrow(label)
		}
		s += fmt.Sprintf("%s%s%s %s %s %s %s\n", tui.ColorBorders("│"), tui.CursorArrow(selected), tui.ColorBorders("│"), label, tui.ColorBorders("│"), value, tui.ColorBorders("│"))
	}
	s += tui.ColorBorders(bottomBorder) + "\n"

	if end < total {
		s += tui.HelpStyle.Render(fmt.Sprintf("  ↓ %d more below", total-end)) + "\n"
	}

	// Apply: a fixed action bar below the table, not one of its rows -
	// always visible at the bottom of the page regardless of scroll. One
	// cursor position past the last real row selects it.
	s += tui.RenderApplyBar(tableWidth, "Apply (install / re-apply)", m.Cursor == total)

	if m.Editing {
		editRow := rows[m.Cursor]
		if editRow.StabilizerDef != nil {
			def := editRow.StabilizerDef
			hint := ""
			if def.HasUnlimited() {
				hint = " (or \"unlimited\")"
			}
			s += "\n" + tui.WrapHelp(def.Summary, p.TermWidth) + "\n"
			if def.Detail != "" && def.Detail != def.Summary {
				s += tui.WrapHelp(def.Detail, p.TermWidth) + "\n"
			}
			s += fmt.Sprintf("\nEnter the value provided by ruddervirt for %s%s:\n  %s\n", def.Key, hint, m.Input.View())
		} else {
			s += fmt.Sprintf("\nEditing %s:\n  %s\n", editRow.Field.Label, m.Input.View())
		}
		if p.Error != "" {
			s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render("Error: "+p.Error))
		}
		s += "\n" + tui.HintBar([2]string{"Enter", "save"}, [2]string{"Esc", "cancel"}) + "\n"
	} else {
		if p.Saving {
			s += "\n" + tui.HelpStyle.Render("Saving...") + "\n"
		} else if p.Error != "" {
			s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render("Error saving: "+p.Error))
		}
		s += "\n" + tui.HintBar([2]string{"↑/↓", "navigate"}, [2]string{"Enter", "edit / choose / apply"}, [2]string{"Esc", "back"}) + "\n"
	}
	return s
}

// clampScroll adjusts scroll so cursor stays within the visible window -
// duplicated from package main's view.go, which has its own identical copy
// for other screens' scroll handling. Not yet reconciled into one shared
// helper.
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
// columns aligned regardless of content length - duplicated from package
// main's view.go, see clampScroll's doc comment for why.
func fitCell(s string, width int) string {
	w := runewidth.StringWidth(s)
	if w > width {
		return runewidth.Truncate(s, width, "…")
	}
	return s + strings.Repeat(" ", width-w)
}
