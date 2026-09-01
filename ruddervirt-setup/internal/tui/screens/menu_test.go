// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"strings"
	"testing"
)

func TestResolveInput(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{"number", "1", "configure", true},
		{"word", "configure", "configure", true},
		{"case/whitespace insensitive", "  ConFigure  ", "configure", true},
		{"install alias resolves to configure", "install", "configure", true},
		{"update is its own real item, not aliased", "update", "update", true},
		{"unknown input", "nope", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ResolveInput(c.input)
			if ok != c.ok || got != c.want {
				t.Errorf("ResolveInput(%q) = (%q, %v), want (%q, %v)", c.input, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestMenuModelResetClearsInputAndResultButNotCursor(t *testing.T) {
	m := MenuModel{Input: "conf", Result: "some result", ResultSource: "src", Cursor: 3}
	got := m.Reset()
	if got.Input != "" {
		t.Errorf("Input = %q, want cleared", got.Input)
	}
	if got.Result != "" {
		t.Errorf("Result = %q, want cleared", got.Result)
	}
	if got.ResultSource != "" {
		t.Errorf("ResultSource = %q, want cleared", got.ResultSource)
	}
	if got.Cursor != 3 {
		t.Errorf("Cursor = %d, want unchanged at 3", got.Cursor)
	}
}

func TestMenuModelUpDownClamped(t *testing.T) {
	m := MenuModel{}
	m = m.Up()
	if m.Cursor != 0 {
		t.Fatalf("Cursor after Up at 0 = %d, want 0 (clamped)", m.Cursor)
	}
	for i := 0; i < len(MenuOrder)+2; i++ {
		m = m.Down()
	}
	if m.Cursor != len(MenuOrder)-1 {
		t.Fatalf("Cursor after Down x%d = %d, want %d (clamped)", len(MenuOrder)+2, m.Cursor, len(MenuOrder)-1)
	}
	m = m.Up()
	if m.Cursor != len(MenuOrder)-2 {
		t.Fatalf("Cursor after one Up = %d, want %d", m.Cursor, len(MenuOrder)-2)
	}
}

func TestMenuModelBackspaceAndTypeRune(t *testing.T) {
	m := MenuModel{}
	m = m.Backspace() // no-op on empty input
	if m.Input != "" {
		t.Fatalf("Input after Backspace on empty = %q, want empty", m.Input)
	}
	m = m.TypeRune("u")
	m = m.TypeRune("p")
	if m.Input != "up" {
		t.Fatalf("Input = %q, want %q", m.Input, "up")
	}
	m = m.Backspace()
	if m.Input != "u" {
		t.Fatalf("Input after Backspace = %q, want %q", m.Input, "u")
	}
}

func TestMenuModelViewHomeShowsMenuItemsAndURL(t *testing.T) {
	m := MenuModel{}
	out := m.ViewHome(HomeParams{TermWidth: 100, AileronUIURL: "http://example.invalid"})
	if !strings.Contains(out, "http://example.invalid") {
		t.Errorf("ViewHome() = %q, want it to show the Aileron UI URL", out)
	}
	for _, item := range MenuOrder {
		if !strings.Contains(out, item) {
			t.Errorf("ViewHome() missing menu item %q:\n%s", item, out)
		}
	}
}

func TestMenuModelViewHomeOmitsURLWhenEmpty(t *testing.T) {
	m := MenuModel{}
	out := m.ViewHome(HomeParams{TermWidth: 100})
	if strings.Contains(out, "Aileron UI:") {
		t.Errorf("ViewHome() = %q, want no Aileron UI line when AileronUIURL is empty", out)
	}
}

func TestMenuModelViewResultShowsResult(t *testing.T) {
	m := MenuModel{Result: "something happened"}
	out := m.ViewResult()
	if !strings.Contains(out, "something happened") {
		t.Errorf("ViewResult() = %q, want it to contain the result text", out)
	}
}
