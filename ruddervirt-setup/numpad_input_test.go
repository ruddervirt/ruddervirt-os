// SPDX-License-Identifier: GPL-3.0-only

package main

import "testing"

func TestRewriteNumpadSequences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain digit untouched", "3", "3"},
		{"single keypad digit", "\x1bOs", "3"},
		{"keypad enter", "\x1bOM", "\r"},
		{"keypad digit then enter, in one read", "\x1bOs\x1bOM", "3\r"},
		{"surrounded by plain text", "a\x1bOqb", "a1b"},
		{"arrow keys pass through untouched", "\x1bOA\x1bOB", "\x1bOA\x1bOB"},
		{"F1-F4 pass through untouched", "\x1bOP", "\x1bOP"},
		{"lone escape at end untouched", "5\x1b", "5\x1b"},
		{"truncated keypad sequence at end untouched", "5\x1bO", "5\x1bO"},
		{"unrecognized ESC O letter untouched", "\x1bOz", "\x1bOz"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := []byte(c.in)
			n := rewriteNumpadSequences(b)
			if got := string(b[:n]); got != c.want {
				t.Errorf("rewriteNumpadSequences(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
