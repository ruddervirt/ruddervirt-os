// SPDX-License-Identifier: GPL-3.0-only

package tui

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

// numpadKeySS3 maps the third byte of the VT220/xterm "application keypad
// mode" SS3 sequence (ESC O <byte>) for the numpad's digits and Enter to
// the plain ASCII byte bubbletea already parses as that key from the main
// keyboard. bubbletea's sequence table (key.go) only maps ESC O A-D
// (arrows) and P-S (F1-F4), so on a terminal left in application keypad
// mode (e.g. by a prior ncurses program that didn't reset it), a numpad
// digit or Enter would otherwise decode as garbage - see
// rewriteNumpadSequences/numpadKeyStdin.
var numpadKeySS3 = map[byte]byte{
	'p': '0', 'q': '1', 'r': '2', 's': '3', 't': '4',
	'u': '5', 'v': '6', 'w': '7', 'x': '8', 'y': '9',
	'M': '\r', // keypad Enter
}

// rewriteNumpadSequences rewrites every ESC O <byte> run in b that
// numpadKeySS3 recognizes into its single-byte replacement, compacting b in
// place, and returns the new length. A sequence split across two Read calls
// (not expected from a real terminal) is left untouched and falls through to
// bubbletea's own parser.
func rewriteNumpadSequences(b []byte) int {
	w := 0
	for r := 0; r < len(b); {
		if b[r] == 0x1b && r+2 < len(b) && b[r+1] == 'O' {
			if repl, ok := numpadKeySS3[b[r+2]]; ok {
				b[w] = repl
				w++
				r += 3
				continue
			}
		}
		b[w] = b[r]
		w++
		r++
	}
	return w
}

// numpadKeyStdin wraps a terminal file, rewriting numpad SS3 sequences (see
// rewriteNumpadSequences) before bubbletea's ANSI parser sees them. Embeds
// *os.File so it still satisfies the io.ReadWriteCloser+Fd() interfaces
// bubbletea/cancelreader need for raw mode and cancelable reads - only Read
// is overridden.
type numpadKeyStdin struct {
	*os.File
}

func (s numpadKeyStdin) Read(p []byte) (int, error) {
	n, err := s.File.Read(p)
	if n > 0 {
		n = rewriteNumpadSequences(p[:n])
	}
	return n, err
}

// ProgramOptions wraps stdin in numpadKeyStdin only when it's actually a
// terminal; otherwise returns nil so bubbletea's default input handling
// (opening /dev/tty as needed) applies unchanged.
func ProgramOptions() []tea.ProgramOption {
	if term.IsTerminal(os.Stdin.Fd()) {
		return []tea.ProgramOption{tea.WithInput(numpadKeyStdin{os.Stdin})}
	}
	return nil
}
