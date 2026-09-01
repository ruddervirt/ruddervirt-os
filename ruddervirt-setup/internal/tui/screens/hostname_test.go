// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"ruddervirt-setup/internal/exec/exectest"
)

func TestHostnameModelEnterWithEmptyInputSetsError(t *testing.T) {
	m := HostnameModel{Input: textinput.New()}
	m.Input.SetValue("")

	got, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got.Error == "" {
		t.Fatal("Error = \"\", want a validation error for an empty hostname")
	}
	if got.Saving {
		t.Fatal("Saving = true, want false - must not attempt to save an invalid hostname")
	}
	if cmd != nil {
		t.Fatal("cmd != nil, want nil - nothing to launch when validation fails")
	}
}

func TestHostnameModelEnterWithValidInputSavesAndClearsError(t *testing.T) {
	// setHostnameCmd's SetHostname shells out via exec.RunPrivileged - fake
	// the runner so this doesn't run hostnamectl for real, and drain the
	// returned cmd so its goroutine can't race a later test's runner swap.
	exectest.WithFakeRunner(&exectest.FakeRunner{}, func() {
		m := HostnameModel{Input: textinput.New(), Error: "stale error"}
		m.Input.SetValue("newhost.example.com")

		got, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

		if !got.Saving {
			t.Fatal("Saving = false, want true once a valid hostname is submitted")
		}
		if got.Error != "" {
			t.Fatalf("Error = %q, want cleared", got.Error)
		}
		if cmd == nil {
			t.Fatal("cmd = nil, want setHostnameCmd's tea.Cmd")
		}
		msg := cmd()
		if setMsg, ok := msg.(HostnameSetMsg); !ok || setMsg.Err != nil {
			t.Fatalf("cmd() = %#v, want a successful HostnameSetMsg", msg)
		}
	})
}

func TestHostnameModelResetClearsSavingErrorAndInput(t *testing.T) {
	m := HostnameModel{Input: textinput.New(), Saving: true, Error: "boom"}
	m.Input.SetValue("stale.example.com")
	m.Input.Focus()

	got := m.Reset()

	if got.Saving {
		t.Error("Saving = true, want false")
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want cleared", got.Error)
	}
	if got.Input.Value() != "" {
		t.Errorf("Input.Value() = %q, want cleared", got.Input.Value())
	}
	if got.Input.Focused() {
		t.Error("Input.Focused() = true, want blurred")
	}
}

func TestHostnameModelForwardsOtherKeysToInput(t *testing.T) {
	m := HostnameModel{Input: textinput.New()}
	m.Input.Focus()

	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})

	if got.Input.Value() != "x" {
		t.Fatalf("Input.Value() = %q, want %q - non-Enter keys must forward to the textinput", got.Input.Value(), "x")
	}
}

func TestHostnameModelViewShowsInputAndError(t *testing.T) {
	m := HostnameModel{Input: textinput.New(), Error: "boom"}
	out := m.View()
	if !strings.Contains(out, "boom") {
		t.Errorf("View() = %q, want it to show the error", out)
	}

	m = HostnameModel{Input: textinput.New(), Saving: true}
	out = m.View()
	if !strings.Contains(out, "Saving...") {
		t.Errorf("View() = %q, want it to show the saving indicator", out)
	}
}
