// SPDX-License-Identifier: GPL-3.0-only

package main

import "strings"

// bigTitleGlyphs is a 5-row block-letter font, defined only for the
// letters "RUDDERVIRT" actually needs.
var bigTitleGlyphs = map[rune][5]string{
	'R': {"████ ", "█   █", "████ ", "█  █ ", "█   █"},
	'U': {"█   █", "█   █", "█   █", "█   █", " ███ "},
	'D': {"████ ", "█   █", "█   █", "█   █", "████ "},
	'E': {"█████", "█    ", "████ ", "█    ", "█████"},
	'V': {"█   █", "█   █", "█   █", " █ █ ", "  █  "},
	'I': {"█████", "  █  ", "  █  ", "  █  ", "█████"},
	'T': {"█████", "  █  ", "  █  ", "  █  ", "  █  "},
}

const bigTitleText = "RUDDERVIRT"

// bigTitleWidth is bigTitle's rendered column width - each glyph is 5
// columns wide with a 1-column gap between letters and none trailing.
var bigTitleWidth = len(bigTitleText)*6 - 1

// bigTitle renders the home screen's masthead as a big block-letter
// banner when the terminal is wide enough for it, falling back to the
// plain bold title (same as every other screen's heading) otherwise -
// termWidth 0 (no tea.WindowSizeMsg yet) is treated as plenty wide, same
// default assumption the settings table makes elsewhere in this package.
func bigTitle(termWidth int) string {
	if termWidth > 0 && termWidth < bigTitleWidth+4 {
		return titleStyle.Render("RudderVirt")
	}
	rows := make([]string, 5)
	for i, ch := range bigTitleText {
		g := bigTitleGlyphs[ch]
		for r := 0; r < 5; r++ {
			rows[r] += g[r]
			if i < len(bigTitleText)-1 {
				rows[r] += " "
			}
		}
	}
	return titleStyle.Render(strings.Join(rows, "\n"))
}
