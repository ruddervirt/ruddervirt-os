// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"ruddervirt-setup/internal/exec/exectest"
)

// passwordChangeModel builds a minimal PasswordModel for
// screenPasswordChange - this screen-local Enter-validation lives entirely
// in PasswordModel.Update, unlike the Esc/Ctrl+S handling (cross-group
// routing) that stays in app.go.
func passwordChangeModel() PasswordModel {
	return PasswordModel{NewInput: textinput.New(), ConfirmInput: textinput.New()}
}

func TestPasswordModelShortPasswordStaysOnField(t *testing.T) {
	m := passwordChangeModel()
	m.NewInput.SetValue("short")
	m.NewInput.Focus()

	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got.ConfirmFocus {
		t.Fatalf("ConfirmFocus = true, want false - a too-short password must not advance to confirm")
	}
	if got.Error == "" {
		t.Fatalf("Error = %q, want a length error", got.Error)
	}
	if got.NewInput.Value() != "short" {
		t.Fatalf("NewInput.Value() = %q, want unchanged %q", got.NewInput.Value(), "short")
	}
}

func TestPasswordModelValidPasswordAdvancesToConfirm(t *testing.T) {
	m := passwordChangeModel()
	m.NewInput.SetValue("longenoughpassword")
	m.NewInput.Focus()

	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if !got.ConfirmFocus {
		t.Fatalf("ConfirmFocus = false, want true - a valid password should advance to confirm")
	}
	if got.Error != "" {
		t.Fatalf("Error = %q, want empty", got.Error)
	}
}

func TestPasswordModelEmptyPasswordIsRejected(t *testing.T) {
	m := passwordChangeModel()

	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got.ConfirmFocus {
		t.Fatal("ConfirmFocus = true, want false - empty password must not advance")
	}
	if got.Error == "" {
		t.Fatal("Error = \"\", want an empty-password error")
	}
}

func TestPasswordModelMismatchedConfirmClearsConfirmField(t *testing.T) {
	m := passwordChangeModel()
	m.NewInput.SetValue("longenoughpassword")
	m.ConfirmInput.SetValue("typo")
	m.ConfirmFocus = true

	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got.Saving {
		t.Fatal("Saving = true, want false - a mismatched confirm must not submit")
	}
	if got.Error == "" {
		t.Fatal("Error = \"\", want a mismatch error")
	}
	if got.ConfirmInput.Value() != "" {
		t.Fatalf("ConfirmInput.Value() = %q, want cleared", got.ConfirmInput.Value())
	}
}

func TestPasswordModelMatchingConfirmSubmits(t *testing.T) {
	// setAdminPasswordCmd's SetAdminPassword shells out via
	// exec.RunPrivileged - fake the runner and drain the returned cmd, same
	// reasoning as TestHostnameModelEnterWithValidInputSavesAndClearsError.
	exectest.WithFakeRunner(&exectest.FakeRunner{}, func() {
		m := passwordChangeModel()
		m.NewInput.SetValue("longenoughpassword")
		m.ConfirmInput.SetValue("longenoughpassword")
		m.ConfirmFocus = true

		got, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

		if !got.Saving {
			t.Fatal("Saving = false, want true once new/confirm match")
		}
		if cmd == nil {
			t.Fatal("cmd = nil, want setAdminPasswordCmd's tea.Cmd")
		}
		msg := cmd()
		if setMsg, ok := msg.(PasswordSetMsg); !ok || setMsg.Err != nil {
			t.Fatalf("cmd() = %#v, want a successful PasswordSetMsg", msg)
		}
	})
}

func TestPasswordModelForwardsOtherKeysToFocusedInput(t *testing.T) {
	m := passwordChangeModel()
	m.NewInput.Focus()

	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if got.NewInput.Value() != "x" {
		t.Fatalf("NewInput.Value() = %q, want %q - typing must forward to the focused field", got.NewInput.Value(), "x")
	}

	m2 := passwordChangeModel()
	m2.ConfirmFocus = true
	m2.ConfirmInput.Focus()
	got2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if got2.ConfirmInput.Value() != "y" {
		t.Fatalf("ConfirmInput.Value() = %q, want %q - typing must forward to ConfirmInput once ConfirmFocus", got2.ConfirmInput.Value(), "y")
	}
	if got2.NewInput.Value() != "" {
		t.Fatalf("NewInput.Value() = %q, want unchanged while ConfirmFocus", got2.NewInput.Value())
	}
}

func TestPasswordModelClearInputsBlanksAndBlursBothFieldsOnly(t *testing.T) {
	m := passwordChangeModel()
	m.NewInput.SetValue("newpass123")
	m.NewInput.Focus()
	m.ConfirmInput.SetValue("newpass123")
	m.ConfirmFocus = true
	m.Saving = true
	m.Error = "boom"

	got := m.ClearInputs()

	if got.NewInput.Value() != "" || got.NewInput.Focused() {
		t.Errorf("NewInput = %q/focused=%v, want cleared and blurred", got.NewInput.Value(), got.NewInput.Focused())
	}
	if got.ConfirmInput.Value() != "" || got.ConfirmInput.Focused() {
		t.Errorf("ConfirmInput = %q/focused=%v, want cleared and blurred", got.ConfirmInput.Value(), got.ConfirmInput.Focused())
	}
	// ClearInputs leaves Saving/Error/ConfirmFocus alone - it's only the
	// input-clearing piece Reset builds on.
	if !got.Saving {
		t.Error("Saving = false, want left untouched (true)")
	}
	if got.Error != "boom" {
		t.Errorf("Error = %q, want left untouched", got.Error)
	}
	if !got.ConfirmFocus {
		t.Error("ConfirmFocus = false, want left untouched (true)")
	}
}

func TestPasswordModelResetClearsEveryField(t *testing.T) {
	m := passwordChangeModel()
	m.NewInput.SetValue("newpass123")
	m.ConfirmInput.SetValue("newpass123")
	m.ConfirmFocus = true
	m.Saving = true
	m.Error = "boom"

	got := m.Reset()

	if got.NewInput.Value() != "" || got.NewInput.Focused() {
		t.Errorf("NewInput = %q/focused=%v, want cleared and blurred", got.NewInput.Value(), got.NewInput.Focused())
	}
	if got.ConfirmInput.Value() != "" || got.ConfirmInput.Focused() {
		t.Errorf("ConfirmInput = %q/focused=%v, want cleared and blurred", got.ConfirmInput.Value(), got.ConfirmInput.Focused())
	}
	if got.ConfirmFocus {
		t.Error("ConfirmFocus = true, want false")
	}
	if got.Saving {
		t.Error("Saving = true, want false")
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want cleared", got.Error)
	}
}

func TestPasswordModelViewCheckShowsError(t *testing.T) {
	m := PasswordModel{Error: "boom"}
	out := m.ViewCheck()
	if !strings.Contains(out, "boom") {
		t.Errorf("ViewCheck() = %q, want it to show the error", out)
	}
}

func TestPasswordModelViewChangeShowsFieldsAndHints(t *testing.T) {
	m := passwordChangeModel()
	out := m.ViewChange()
	if !strings.Contains(out, "skip for now") {
		t.Errorf("ViewChange() (not confirm-focused) = %q, want the skip hint", out)
	}

	m.ConfirmFocus = true
	out = m.ViewChange()
	if !strings.Contains(out, "back to new password") {
		t.Errorf("ViewChange() (confirm-focused) = %q, want the back-navigation hint", out)
	}
}
