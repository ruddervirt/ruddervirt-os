// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/osupdate"
	"ruddervirt-setup/internal/stabilizer"
	"ruddervirt-setup/internal/stabilizer/settings"
	"ruddervirt-setup/internal/tui"
	"ruddervirt-setup/internal/tui/pipeline"
	versionspkg "ruddervirt-setup/internal/versions"
)

// UpdateModel is the update-versions wizard's sub-model
// (screenUpdateVersions, screenUpdateChecking, screenUpdateConfirm,
// screenUpdate) - the "update" menu's landing page (a ruddervirt-setup row
// plus the component-version fields moved out of Settings,
// config.SettingField.UpdateScreen) through to the self-update pipeline
// itself (internal/selfupdate). The second screen group to wire in
// internal/tui/pipeline's generic step-runner: Pipeline backs screenUpdate
// exactly the way InstallModel.Pipeline backs screenInstall, for
// selfupdate.UpdateSteps instead of installSteps.
//
// The guarded Aileron-version special-casing (routing screenUpdateVersions'
// "Aileron version" row into the stabilizer-version-confirm flow once a
// stabilizer HelmChart is detected - see
// TestAileronVersionOnceStabilizerDetected) stays in app.go: it dispatches
// into screens this group doesn't own (screenStabilizerVersionConfirm/
// Apply), so it's cross-group routing. Same reason settingsSaving/
// settingsError (shared with Settings) and the version/stabilizer caches
// (also read by MenuModel's "available upgrade" icons) stay on the root
// Model, threaded through UpdateViewParams instead of duplicated here.
type UpdateModel struct {
	// Cursor/Scroll/Picking/PickCursor/PickScroll/PickOptions back
	// screenUpdateVersions' table/picker - same shape as SettingsModel, for
	// UpdateRows() instead of SettingsModel.Rows(). Unlike Settings, every
	// UpdateScreen field is picker-only, so there's no Editing/Input field.
	Cursor      int
	Scroll      int
	Picking     bool
	PickCursor  int
	PickScroll  int
	PickOptions []string

	// Checking/CheckErr/LatestVersion/BinaryURL/ChecksumHex/AlreadyLatest
	// back screenUpdateChecking's brief background check (checkForUpdateCmd,
	// update.go). CheckErr is write-only - never read back, since app.go's
	// updateCheckMsg handler routes an error to screenResult instead -
	// preserved as-is rather than "fixed" here.
	Checking      bool
	CheckErr      string
	LatestVersion string
	BinaryURL     string
	ChecksumHex   string
	AlreadyLatest bool

	// ConfirmInput/ConfirmError back screenUpdateConfirm's yes/no prompt.
	ConfirmInput textinput.Model
	ConfirmError string

	// Pipeline runs selfupdate.UpdateSteps for screenUpdate itself (see
	// struct doc). Installed mirrors InstallModel/OSUpdateModel's own
	// pipeline-completion flag: main.go's boot loop reads it (once
	// Pipeline.Done) to decide whether to re-exec into the freshly-installed
	// binary, since finishing this pipeline replaces the running binary
	// itself (unlike OSUpdateModel).
	Pipeline  pipeline.Model
	Installed bool
}

// UpdateRow is one row of screenUpdateVersions' landing table: either the
// ruddervirt-setup row (delegates into checkForUpdateCmd/
// screenUpdateChecking), the operating-system row (delegates straight into
// osupdate.OSUpdateSteps via OSUpdateModel, no separate check/confirm
// step), or one of the component version fields moved out of Settings
// (config.SettingField.UpdateScreen).
type UpdateRow struct {
	IsSelfUpdate bool
	IsOSUpdate   bool
	Field        config.SettingField
}

// UpdateRows lists screenUpdateVersions' rows: ruddervirt-setup and the
// operating system first, then every config.SettingField tagged
// UpdateScreen, in config.SettingFields' order. Apply isn't one of these
// rows - same fixed-footer treatment as SettingsModel.Rows' Apply, one
// cursor position past the last row here (see ScrollCursor).
func UpdateRows() []UpdateRow {
	rows := []UpdateRow{{IsSelfUpdate: true}, {IsOSUpdate: true}}
	for _, f := range config.SettingFields {
		if f.UpdateScreen {
			rows = append(rows, UpdateRow{Field: f})
		}
	}
	return rows
}

// UpdateRowHasUpgrade reports whether r has something newer than what's
// currently configured/running, given the same cross-group state
// ViewVersions renders from (p). Generalized across every
// config.SettingField row via its own options func (already filtered to "no
// downgrades from current", so "any option isn't the current value" means
// "an upgrade exists") rather than duplicating each component's version
// comparator. The two non-SettingField rows (ruddervirt-setup, OS) and the
// stabilizer-managed Aileron case (options come from
// stabilizer.StabilizerVersionPickerOptions instead) are special-cased.
// Exported so the home screen's menu can flag the "update" row without
// duplicating this logic (see AnyUpdateAvailable, MenuModel.ViewHome).
func UpdateRowHasUpgrade(r UpdateRow, p UpdateViewParams) bool {
	versions := p.Versions
	if r.IsSelfUpdate {
		return p.SelfUpdateAvailable
	}
	if r.IsOSUpdate {
		return p.OSUpdateAvailable
	}
	if r.Field.Key == "versions.aileron" && versions.StabilizerDetected {
		if p.StabilizerSettingsState == nil {
			return false
		}
		for _, o := range stabilizer.StabilizerVersionPickerOptions(versions.Aileron, p.StabilizerSettingsState.DeclaredVersion) {
			if strings.TrimPrefix(o, "v") != p.StabilizerSettingsState.DeclaredVersion {
				return true
			}
		}
		return false
	}
	if r.Field.Locked != nil {
		if locked, _ := r.Field.Locked(&p.Cfg, versions); locked {
			return false
		}
	}
	if r.Field.Options == nil {
		return false
	}
	current := r.Field.Get(&p.Cfg)
	for _, o := range r.Field.Options(&p.Cfg, versions) {
		if o != current {
			return true
		}
	}
	return false
}

// AnyUpdateAvailable reports whether any screenUpdateVersions row
// (ruddervirt-setup, OS, or a component) has an upgrade available, for the
// home screen's "update" menu row (see MenuModel.ViewHome).
func AnyUpdateAvailable(p UpdateViewParams) bool {
	for _, r := range UpdateRows() {
		if UpdateRowHasUpgrade(r, p) {
			return true
		}
	}
	return false
}

// ScrollCursor caps Cursor at the last real table row for scroll-window
// purposes - mirrors SettingsModel.ScrollCursor.
func (m UpdateModel) ScrollCursor() int {
	if last := len(UpdateRows()) - 1; m.Cursor > last {
		return last
	}
	return m.Cursor
}

// Up moves the cursor (picker or table, whichever is active) up by one,
// re-clamping scroll - called directly from app.go on tea.KeyUp while
// screenUpdateVersions is current, same named-navigation-method shape as
// SettingsModel.Up. No Editing guard, unlike Settings' Up (see struct doc:
// there's no free-text edit mode here).
func (m UpdateModel) Up(visibleRows int) UpdateModel {
	if m.Picking {
		if m.PickCursor > 0 {
			m.PickCursor--
		}
		m.PickScroll = clampScroll(m.PickScroll, m.PickCursor, visibleRows)
		return m
	}
	if m.Cursor > 0 {
		m.Cursor--
	}
	m.Scroll = clampScroll(m.Scroll, m.ScrollCursor(), visibleRows)
	return m
}

// Down mirrors Up - see its doc comment. len(UpdateRows()), not -1: one
// cursor position past the last real row lands on Apply upgrades (see
// ScrollCursor).
func (m UpdateModel) Down(visibleRows int) UpdateModel {
	if m.Picking {
		if m.PickCursor < len(m.PickOptions)-1 {
			m.PickCursor++
		}
		m.PickScroll = clampScroll(m.PickScroll, m.PickCursor, visibleRows)
		return m
	}
	if m.Cursor < len(UpdateRows()) {
		m.Cursor++
	}
	m.Scroll = clampScroll(m.Scroll, m.ScrollCursor(), visibleRows)
	return m
}

// ClearConfirm blanks and blurs ConfirmInput and clears ConfirmError,
// leaving every other field (notably Cursor/Scroll/Picking) untouched -
// app.go calls this when Esc cancels screenUpdateConfirm back into
// screenUpdateVersions, which must keep the operator's table position,
// unlike Reset below.
func (m UpdateModel) ClearConfirm() UpdateModel {
	m.ConfirmInput.SetValue("")
	m.ConfirmInput.Blur()
	m.ConfirmError = ""
	return m
}

// Reset clears every field back to its just-entered zero value, except
// PickOptions (always repopulated before Picking flips true again, see
// ViewVersions' picker branch) and Installed (only ever set true right
// before quitting, so there's no further screen to land back on). app.go
// calls this landing on screenUpdateVersions fresh (from the "update" menu,
// or resuming after a forced hostname declaration) and from the generic
// cross-group Esc handler that abandons any flow back to screenMenu.
func (m UpdateModel) Reset() UpdateModel {
	m.Cursor = 0
	m.Scroll = 0
	m.Picking = false
	m.PickCursor = 0
	m.PickScroll = 0
	m.Checking = false
	m.CheckErr = ""
	m.LatestVersion = ""
	m.BinaryURL = ""
	m.ChecksumHex = ""
	m.AlreadyLatest = false
	m.ConfirmInput.SetValue("")
	m.ConfirmInput.Blur()
	m.ConfirmError = ""
	m.Pipeline = pipeline.Model{}
	return m
}

// Update forwards key presses to ConfirmInput. The yes/no submit logic
// (launching Pipeline via pipeline.New, which needs
// selfupdate.UpdateSteps/config.Config) and confirm/cancel Esc handling
// stay in app.go, called directly against this struct's fields - same
// convention as InstallModel.
func (m UpdateModel) Update(msg tea.Msg) (UpdateModel, tea.Cmd) {
	var cmd tea.Cmd
	m.ConfirmInput, cmd = m.ConfirmInput.Update(msg)
	return m, cmd
}

// ViewChecking renders screenUpdateChecking. Holds no state of its own
// (checking for an update is a brief background step, same as
// InstallModel.ViewPlanning); kept as a method so every screen in this
// group renders from the same place.
func (m UpdateModel) ViewChecking() string {
	return "\n" + tui.HelpStyle.Render("Checking for updates...") + "\n"
}

// ViewConfirm renders screenUpdateConfirm.
func (m UpdateModel) ViewConfirm() string {
	s := "\n" + tui.TitleStyle.Render("Update ruddervirt-setup") + "\n\n"
	s += fmt.Sprintf("Current version: %s\nLatest version:  %s\n", versionspkg.Version, m.LatestVersion)
	s += "\nThis will download and verify (SHA256) the new binary, then\n"
	s += "replace /usr/local/bin/ruddervirt-setup. The menu will restart\n"
	s += "on the new version afterward.\n"
	s += fmt.Sprintf("\n%s\n  %s\n", tui.HelpStyle.Render("Type \"yes\" to proceed, or Esc to cancel:"), m.ConfirmInput.View())
	if m.ConfirmError != "" {
		s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render(m.ConfirmError))
	}
	return s
}

// ViewRunning renders screenUpdate - the running self-update pipeline
// itself - via pipeline.Model.View. Unlike
// InstallModel.ViewInstall/OSUpdateModel.View, the done copy carries no
// "Press Esc..." hint: a successful self-update quits the process
// immediately (see app.go's stepDoneMsg handling) rather than waiting for
// the operator to press Esc.
func (m UpdateModel) ViewRunning(visibleLines int) string {
	return m.Pipeline.View(
		"Updating ruddervirt-setup...",
		tui.SuccessStyle.Render("Update complete. Restarting into the new version..."),
		tui.ErrorStyle.Render("Update failed.")+" "+tui.HelpStyle.Render("Press Esc to return to menu."),
		visibleLines,
	)
}

// UpdateViewParams bundles the cross-group state ViewVersions needs beyond
// m itself - cfg, the version/stabilizer caches, the two
// non-config.SettingField rows' "available" flags, and the shared
// saving/error feedback text (also written by the Settings screen) - so
// they stay on the root Model rather than being duplicated onto this
// struct, same convention as SettingsViewParams.
type UpdateViewParams struct {
	Cfg                     config.Config
	Versions                config.VersionCache
	StabilizerSettingsState *settings.StabilizerSettingsState
	SelfUpdateAvailable     bool
	OSUpdateAvailable       bool
	Error                   string
	Saving                  bool
	TermWidth               int
	VisibleRows             int
}

// ViewVersions renders screenUpdateVersions - both the picker overlay and
// the main table view.
func (m UpdateModel) ViewVersions(p UpdateViewParams) string {
	if m.Picking {
		rows := UpdateRows()
		field := rows[m.Cursor].Field
		options := m.PickOptions

		width := runewidth.StringWidth(field.Label) + 4
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

		s := "\n" + tui.TitleStyle.Render("Update") + fmt.Sprintf("\n\nSelect %s:\n\n", field.Label)

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

	rows := UpdateRows()
	total := len(rows)

	versions := p.Versions
	rowHasUpgrade := func(r UpdateRow) bool { return UpdateRowHasUpgrade(r, p) }

	// updateRowIconPrefix is a fixed-width (2 column) plain-text prefix
	// baked into the label BEFORE fitCell/width computation, so alignment
	// never shifts depending on which rows have an upgrade; color is
	// applied after, same "style only after fitCell" rule as
	// tui.ColorToggleArrow in SettingsModel.View.
	updateRowIconPrefix := func(r UpdateRow) string {
		if rowHasUpgrade(r) {
			return "↑ "
		}
		return "  "
	}

	rowLabel := func(r UpdateRow) string {
		label := ""
		switch {
		case r.IsSelfUpdate:
			label = "ruddervirt-setup"
		case r.IsOSUpdate:
			label = "Operating system"
		default:
			label = r.Field.Label
		}
		return updateRowIconPrefix(r) + label
	}
	rowValue := func(r UpdateRow) string {
		if r.IsSelfUpdate {
			return versionspkg.Version
		}
		if r.IsOSUpdate {
			if v := osupdate.CurrentOSVersion(); v != "" {
				return v
			}
			return "-"
		}
		if r.Field.Key == "versions.aileron" && versions.StabilizerDetected {
			// Once stabilizer manages it, shows the installed chart version
			// (CHART_VERSION) as the primary value, with what the HelmChart
			// resource asks for appended only when a rollout is pending or
			// failed - same applied-vs-declared convention as
			// stabilizerSettingRowDisplay (stabilizer_settings_tui.go).
			if p.StabilizerSettingsState == nil {
				return "loading..."
			}
			applied := p.StabilizerSettingsState.AppliedChartVersion
			declared := p.StabilizerSettingsState.DeclaredVersion
			if applied == "" {
				applied = "(unknown)"
			}
			if declared != "" && declared != applied {
				return fmt.Sprintf("%s (rollout pending -> %s)", applied, declared)
			}
			return applied
		}
		if r.Field.Locked != nil {
			if locked, reason := r.Field.Locked(&p.Cfg, versions); locked {
				return reason
			}
		}
		return r.Field.Get(&p.Cfg)
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

	s := "\n" + tui.TitleStyle.Render("Update") + "\n\n"

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
	s += tui.ColorBorders(fmt.Sprintf("│   │ %s │ %s │", fitCell("Component", labelWidth), fitCell("Version", valueWidth))) + "\n"
	s += tui.ColorBorders(sepBorder) + "\n"
	for i := start; i < end; i++ {
		selected := i == m.Cursor
		r := rows[i]
		label := fitCell(rowLabel(r), labelWidth)
		value := fitCell(rowValue(r), valueWidth)
		if selected {
			label = tui.SelectedStyle.Render(label)
			value = tui.SelectedStyle.Render(value)
		} else if rowHasUpgrade(r) {
			label = tui.ColorUpdateIcon(label)
		}
		s += fmt.Sprintf("%s%s%s %s %s %s %s\n", tui.ColorBorders("│"), tui.CursorArrow(selected), tui.ColorBorders("│"), label, tui.ColorBorders("│"), value, tui.ColorBorders("│"))
	}
	s += tui.ColorBorders(bottomBorder) + "\n"

	if end < total {
		s += tui.HelpStyle.Render(fmt.Sprintf("  ↓ %d more below", total-end)) + "\n"
	}
	s += tui.HelpStyle.Render("  ↑ marks an available upgrade") + "\n"

	// Apply upgrades: same fixed-footer treatment as SettingsModel.View's
	// Apply - pushes whatever versions are picked above through the same
	// install pipeline.
	s += tui.RenderApplyBar(tableWidth, "Apply upgrades", m.Cursor == total)

	if p.Saving {
		s += "\n" + tui.HelpStyle.Render("Saving...") + "\n"
	} else if p.Error != "" {
		s += fmt.Sprintf("\n%s\n", tui.ErrorStyle.Render("Error: "+p.Error))
	}
	s += "\n" + tui.HintBar([2]string{"↑/↓", "navigate"}, [2]string{"Enter", "check / choose / apply"}, [2]string{"Esc", "back"}) + "\n"
	return s
}
