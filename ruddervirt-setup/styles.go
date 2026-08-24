package main

import "github.com/charmbracelet/lipgloss"

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
)

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

// cursorArrow renders the fixed-width "   "/" > " cursor gutter used
// throughout the settings/pick-list tables, coloring only the arrow glyph
// so the surrounding padding stays untouched.
func cursorArrow(selected bool) string {
	if !selected {
		return "   "
	}
	return " " + cursorStyle.Render(">") + " "
}
