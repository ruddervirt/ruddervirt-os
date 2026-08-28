// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Color palette - AdaptiveColor picks the Light or Dark value based on the
// terminal's reported background, so the TUI reads well over both a light
// and a dark SSH client without any user configuration.
var (
	colorPrimary = lipgloss.AdaptiveColor{Light: "#0552B5", Dark: "#7AA2F7"}
	colorAccent  = lipgloss.AdaptiveColor{Light: "#8250DF", Dark: "#BB9AF7"}
	colorSuccess = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#9ECE6A"}
	colorWarning = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#E0AF68"}
	colorDanger  = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F7768E"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#565F89"}
	colorBorder  = lipgloss.AdaptiveColor{Light: "#D0D7DE", Dark: "#3B4261"}
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	helpStyle     = lipgloss.NewStyle().Foreground(colorMuted)
	errorStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorDanger)
	successStyle  = lipgloss.NewStyle().Foreground(colorSuccess)
	warningStyle  = lipgloss.NewStyle().Foreground(colorWarning)
	linkStyle     = lipgloss.NewStyle().Foreground(colorAccent).Underline(true)
	cursorStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	borderStyle   = lipgloss.NewStyle().Foreground(colorBorder)
	menuKeyStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	promptStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)

	// ctaStyle/ctaSelectedStyle/ctaBorderStyle style the Settings screen's
	// Apply action bar - a call-to-action button, not just another table
	// row, so it gets its own green accent rather than the primary/accent
	// colors used for regular selection.
	ctaStyle         = lipgloss.NewStyle().Bold(true).Foreground(colorSuccess)
	ctaSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(colorSuccess)
	ctaBorderStyle   = lipgloss.NewStyle().Foreground(colorSuccess)

	// toggleStyle colors a collapsible row's leading ▶/▼ disclosure
	// triangle, marking it as expandable at a glance even when it isn't
	// the currently selected row.
	toggleStyle = lipgloss.NewStyle().Foreground(colorAccent)
)

// colorToggleArrow re-colors just the first rune of s (a settings row
// label's leading ▶/▼) with toggleStyle, leaving the rest of the label
// plain - used instead of styling the whole row, which selectedStyle
// already owns when the row is the current cursor position.
func colorToggleArrow(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return toggleStyle.Render(string(r[0])) + string(r[1:])
}

// renderApplyBar renders the fixed action-bar footer shared by the
// Settings and Update screens - a bordered, centered call-to-action
// button below their tables, tableWidth columns wide (matching the
// table's own outer width) and filled solid when selected.
func renderApplyBar(tableWidth int, label string, selected bool) string {
	inner := tableWidth - 2
	text := "▶  " + label
	if pad := inner - runewidth.StringWidth(text); pad > 0 {
		left := pad / 2
		text = strings.Repeat(" ", left) + text + strings.Repeat(" ", pad-left)
	}
	content := ctaStyle.Render(text)
	if selected {
		content = ctaSelectedStyle.Render(text)
	}
	s := "\n" + ctaBorderStyle.Render("╭"+strings.Repeat("─", inner)+"╮") + "\n"
	s += ctaBorderStyle.Render("│") + content + ctaBorderStyle.Render("│") + "\n"
	s += ctaBorderStyle.Render("╰"+strings.Repeat("─", inner)+"╯") + "\n"
	return s
}

// hintBar renders a bottom-of-screen key cheat sheet, e.g.
// hintBar([2]string{"Enter", "edit"}, [2]string{"Esc", "cancel"}) ->
// "Enter edit  •  Esc cancel", with each key bolded and the rest muted.
func hintBar(pairs ...[2]string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = menuKeyStyle.Render(p[0]) + " " + helpStyle.Render(p[1])
	}
	return strings.Join(parts, helpStyle.Render("  •  "))
}

// colorBorders re-colors every box-drawing character in s - used on table
// rows that mix plain content with "│" column separators, since styling
// the whole row would also recolor the content.
func colorBorders(s string) string {
	return replaceAllRunes(s, "─│┌┬┐├┼┤└┴┘", borderStyle)
}

func replaceAllRunes(s, runes string, style lipgloss.Style) string {
	out := []rune(s)
	set := map[rune]bool{}
	for _, r := range runes {
		set[r] = true
	}
	var b []byte
	for _, r := range out {
		if set[r] {
			b = append(b, []byte(style.Render(string(r)))...)
		} else {
			b = append(b, []byte(string(r))...)
		}
	}
	return string(b)
}

// colorUpdateIcon re-colors just the first rune of s (the Update screen's
// leading ↑/space upgrade-available indicator, see updateRowIconPrefix in
// view.go) with warningStyle - same "recolor the first rune of an
// already-built, already-fitCell'd label" shape as colorToggleArrow above,
// just a different color (amber "worth a look" rather than the toggle's
// accent purple) and a different trigger (an upgrade being available rather
// than a row being expandable).
func colorUpdateIcon(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return warningStyle.Render(string(r[0])) + string(r[1:])
}

// wrapHelp renders text in helpStyle, word-wrapped to width (lipgloss wraps
// automatically once a style has a Width set) - used for the stabilizer/
// aileron setting descriptions (summary/detail, stabilizer-settings.yaml)
// shown on the picker/edit-value screens, since detail in particular can run
// to a couple of sentences and this app has no other wrapping helper.
// termWidth <= 0 (not yet known, e.g. before the first WindowSizeMsg) falls
// back to a reasonable 80-column default, same as every other width
// calculation in view.go.
func wrapHelp(text string, termWidth int) string {
	if termWidth <= 0 {
		termWidth = 80
	}
	width := termWidth - 2
	if width < 20 {
		width = 20
	}
	return helpStyle.Width(width).Render(text)
}

// wrapIndented word-wraps text to termWidth, left-padding every resulting
// line (including wrapped continuation lines, not just the first) by
// indent spaces - used for the home screen's auto-updating Status block
// (renderHomeStatus, status.go), whose service/system summary lines are
// simple space-joined strings with no width awareness of their own, so on a
// narrow terminal they used to run off the edge instead of wrapping.
// Deliberately width-only, no color: text may already carry its own
// per-segment ANSI styling (stateBullet's colored "●", styleUsagePercent's
// colored percentages) which lipgloss's wrapping is ANSI-aware of and
// preserves, but a style setting its own Foreground here would override it.
func wrapIndented(text string, indent, termWidth int) string {
	if termWidth <= 0 {
		termWidth = 80
	}
	// Width is the OUTER width (lipgloss subtracts the style's own padding
	// when wrapping content, same box-model convention renderApplyBar's
	// bordered boxes elsewhere in this file already rely on) - not the
	// content width, so indent is NOT subtracted again here.
	width := termWidth - 1
	if width < indent+20 {
		width = indent + 20
	}
	return lipgloss.NewStyle().Width(width).PaddingLeft(indent).Render(text)
}

// stateBullet is the colored "●" marker shown next to a service's name -
// same color-coding as styleState, split out so callers can put the dot
// ahead of a fixed-width, left-aligned name column.
func stateBullet(state string) string {
	switch state {
	case "running", "ready":
		return successStyle.Render("●")
	case "not running", "not ready":
		return errorStyle.Render("●")
	default:
		return helpStyle.Render("●")
	}
}

// styleState color-codes a service/row status word - green for healthy,
// red/yellow for trouble, muted gray when it couldn't be determined.
func styleState(state string) string {
	switch state {
	case "running", "ready":
		return successStyle.Render(state)
	case "not running", "not ready":
		return errorStyle.Render(state)
	case "unknown":
		return helpStyle.Render(state)
	default:
		return state
	}
}

// styleUsagePercent color-codes a CPU/memory load percentage on the home
// screen's "System" summary - green under 75%, yellow up to 90%, red above -
// so a saturated host stands out without the operator doing the math
// themselves.
func styleUsagePercent(pct float64) string {
	text := fmt.Sprintf("%.0f%%", pct)
	switch {
	case pct >= 90:
		return errorStyle.Render(text)
	case pct >= 75:
		return warningStyle.Render(text)
	default:
		return successStyle.Render(text)
	}
}

// cursorArrow renders the fixed-width "   "/" > " cursor gutter used
// throughout the settings/pick-list tables, coloring only the arrow glyph
// so the surrounding padding stays untouched.
func cursorArrow(selected bool) string {
	if !selected {
		return "   "
	}
	return " " + cursorStyle.Render(">") + " "
}
