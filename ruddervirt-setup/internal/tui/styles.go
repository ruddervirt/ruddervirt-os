// SPDX-License-Identifier: GPL-3.0-only

package tui

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
	TitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	HelpStyle     = lipgloss.NewStyle().Foreground(colorMuted)
	ErrorStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorDanger)
	SuccessStyle  = lipgloss.NewStyle().Foreground(colorSuccess)
	warningStyle  = lipgloss.NewStyle().Foreground(colorWarning)
	LinkStyle     = lipgloss.NewStyle().Foreground(colorAccent).Underline(true)
	CursorStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	SelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	borderStyle   = lipgloss.NewStyle().Foreground(colorBorder)
	MenuKeyStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	PromptStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)

	// ctaStyle/ctaSelectedStyle/ctaBorderStyle style the Settings/Update
	// screens' Apply action bar with its own green accent, distinct from
	// regular row selection.
	ctaStyle         = lipgloss.NewStyle().Bold(true).Foreground(colorSuccess)
	ctaSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(colorSuccess)
	ctaBorderStyle   = lipgloss.NewStyle().Foreground(colorSuccess)

	// toggleStyle colors a collapsible row's leading ▶/▼ so it reads as
	// expandable even when not selected.
	toggleStyle = lipgloss.NewStyle().Foreground(colorAccent)
)

// ColorToggleArrow re-colors just the first rune of s (a settings row
// label's leading ▶/▼) with toggleStyle, leaving the rest plain - the whole
// row isn't styled since SelectedStyle owns that when it's the cursor row.
func ColorToggleArrow(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return toggleStyle.Render(string(r[0])) + string(r[1:])
}

// RenderApplyBar renders the fixed action-bar footer shared by the Settings
// and Update screens: a bordered, centered call-to-action button below their
// tables, tableWidth columns wide (matching the table's outer width) and
// filled solid when selected.
func RenderApplyBar(tableWidth int, label string, selected bool) string {
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

// HintBar renders a bottom-of-screen key cheat sheet, e.g.
// HintBar([2]string{"Enter", "edit"}, [2]string{"Esc", "cancel"}) ->
// "Enter edit  •  Esc cancel", with each key bolded and the rest muted.
func HintBar(pairs ...[2]string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = MenuKeyStyle.Render(p[0]) + " " + HelpStyle.Render(p[1])
	}
	return strings.Join(parts, HelpStyle.Render("  •  "))
}

// ColorBorders re-colors every box-drawing character in s - used on table
// rows that mix plain content with "│" separators, since styling the whole
// row would also recolor the content.
func ColorBorders(s string) string {
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

// ColorUpdateIcon re-colors just the first rune of s (the Update screen's
// leading ↑/space upgrade-available indicator, see updateRowIconPrefix in
// view.go) with warningStyle - same shape as ColorToggleArrow, but amber for
// "worth a look" instead of the toggle's accent purple.
func ColorUpdateIcon(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return warningStyle.Render(string(r[0])) + string(r[1:])
}

// WrapHelp renders text in HelpStyle, word-wrapped to width (lipgloss wraps
// automatically once a style has Width set) - used for the stabilizer/
// aileron setting descriptions on the picker/edit-value screens, since
// detail text can run to a couple of sentences. termWidth <= 0 (not yet
// known) falls back to 80 columns, matching view.go's other width defaults.
func WrapHelp(text string, termWidth int) string {
	if termWidth <= 0 {
		termWidth = 80
	}
	width := termWidth - 2
	if width < 20 {
		width = 20
	}
	return HelpStyle.Width(width).Render(text)
}

// WrapIndented word-wraps text to termWidth, left-padding every line
// (including wrapped continuations) by indent spaces - used for the home
// screen's Status block, whose service/system summary lines are plain
// space-joined strings with no width awareness of their own. Deliberately
// width-only, no color: text may carry its own per-segment ANSI styling
// (StateBullet's "●", StyleUsagePercent's percentages) which lipgloss's
// wrapping preserves, but a Foreground here would override it.
func WrapIndented(text string, indent, termWidth int) string {
	if termWidth <= 0 {
		termWidth = 80
	}
	// Width is the OUTER width (lipgloss subtracts the style's own padding
	// when wrapping, same box model RenderApplyBar relies on) - indent is
	// NOT subtracted again here.
	width := termWidth - 1
	if width < indent+20 {
		width = indent + 20
	}
	return lipgloss.NewStyle().Width(width).PaddingLeft(indent).Render(text)
}

// StateBullet is the colored "●" marker next to a service's name - same
// color-coding as styleState, split out so callers can place the dot ahead
// of a fixed-width, left-aligned name column.
func StateBullet(state string) string {
	switch state {
	case "running", "ready":
		return SuccessStyle.Render("●")
	case "not running", "not ready":
		return ErrorStyle.Render("●")
	default:
		return HelpStyle.Render("●")
	}
}

// styleState color-codes a service/row status word - green for healthy,
// red/yellow for trouble, muted gray when it couldn't be determined.
func styleState(state string) string {
	switch state {
	case "running", "ready":
		return SuccessStyle.Render(state)
	case "not running", "not ready":
		return ErrorStyle.Render(state)
	case "unknown":
		return HelpStyle.Render(state)
	default:
		return state
	}
}

// StyleUsagePercent color-codes a CPU/memory load percentage on the home
// screen's "System" summary - green under 75%, yellow up to 90%, red above -
// so a saturated host stands out at a glance.
func StyleUsagePercent(pct float64) string {
	text := fmt.Sprintf("%.0f%%", pct)
	switch {
	case pct >= 90:
		return ErrorStyle.Render(text)
	case pct >= 75:
		return warningStyle.Render(text)
	default:
		return SuccessStyle.Render(text)
	}
}

// CursorArrow renders the fixed-width "   "/" > " cursor gutter used in the
// settings/pick-list tables, coloring only the arrow glyph.
func CursorArrow(selected bool) string {
	if !selected {
		return "   "
	}
	return " " + CursorStyle.Render(">") + " "
}
