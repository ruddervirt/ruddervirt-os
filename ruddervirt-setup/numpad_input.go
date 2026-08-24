package main

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

// numpadKeySS3 maps the third byte of the classic VT220/xterm "application
// keypad mode" SS3 sequence (ESC O <byte>) for the numeric keypad's digits
// and Enter key to the plain ASCII byte bubbletea already knows how to
// parse as that same key from the main keyboard. bubbletea's own sequence
// table (key.go) only maps ESC O A-D (arrows) and ESC O P-S (F1-F4) - it
// has no entry for these, so on a terminal left in application keypad
// mode (e.g. by a prior ncurses program that didn't reset it on exit),
// pressing a numpad digit or Enter decodes as garbage (an "alt+<letter>"
// key event followed by a stray rune) instead of doing anything useful -
// see rewriteNumpadSequences/numpadKeyStdin.
var numpadKeySS3 = map[byte]byte{
	'p': '0', 'q': '1', 'r': '2', 's': '3', 't': '4',
	'u': '5', 'v': '6', 'w': '7', 'x': '8', 'y': '9',
	'M': '\r', // keypad Enter
}

// rewriteNumpadSequences rewrites every ESC O <byte> run in b that
// numpadKeySS3 recognizes into its single-byte replacement, compacting b
// in place, and returns the new length. Only sequences that arrive fully
// intact within b are rewritten - one split across two Read calls (not
// expected from a real terminal, which writes a whole key event in one
// burst) is left untouched and simply falls through to bubbletea's own
// parser, exactly as it would without this rewrite at all.
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

// numpadKeyStdin wraps a terminal file, rewriting numpad SS3 sequences
// (see rewriteNumpadSequences) before bubbletea's own ANSI parser ever
// sees them. Embeds *os.File so it still satisfies the
// io.ReadWriteCloser+Fd() interfaces bubbletea and its cancelreader
// dependency need to detect a real terminal and enable raw mode/cancelable
// reads on it - only Read is overridden.
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

// programOptions wraps stdin in numpadKeyStdin only when it's actually a
// terminal - when it isn't (e.g. piped/redirected input), returning no
// options at all lets bubbletea's own default-input handling (opening
// /dev/tty as needed) apply exactly as if this file didn't exist.
func programOptions() []tea.ProgramOption {
	if term.IsTerminal(os.Stdin.Fd()) {
		return []tea.ProgramOption{tea.WithInput(numpadKeyStdin{os.Stdin})}
	}
	return nil
}
