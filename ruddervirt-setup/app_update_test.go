// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// TestMenuUpdateOpensUpdateVersionsScreen is a regression test for a bug
// where selecting "update" from the main menu fell through to the generic
// default label branch, just showing the literal text "update" as a
// result instead of opening the Update screen.
func TestMenuUpdateOpensUpdateVersionsScreen(t *testing.T) {
	m := model{current: screenMenu, input: "update"}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if got.current != screenUpdateVersions {
		t.Fatalf("current = %v, want screenUpdateVersions", got.current)
	}
	if got.result == "update" {
		t.Fatalf("result = %q - selecting update must not just echo the label back as a result", got.result)
	}
}

// TestMenuArrowNavigation confirms ↑/↓ move menuCursor (clamped, no wrap)
// and Enter with no typed input submits whatever's highlighted.
func TestMenuArrowNavigation(t *testing.T) {
	m := model{current: screenMenu}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := next.(model).menuCursor; got != 0 {
		t.Fatalf("menuCursor after Up at 0 = %d, want 0 (clamped)", got)
	}

	m.menuCursor = 0
	for i := 0; i < len(menuOrder)+2; i++ {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(model)
	}
	if m.menuCursor != len(menuOrder)-1 {
		t.Fatalf("menuCursor after Down x%d = %d, want %d (clamped)", len(menuOrder)+2, m.menuCursor, len(menuOrder)-1)
	}

	// menuCursor is on "logout" (last item) - Enter with no typed input
	// should submit it directly, same as typing "5"/"logout" would.
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Fatalf("cmd = nil, want tea.Quit (logout)")
	}
}

// TestMenuTypedInputWinsOverCursor confirms Enter prefers typed input over
// the ↑/↓ cursor when both are present.
func TestMenuTypedInputWinsOverCursor(t *testing.T) {
	m := model{current: screenMenu, input: "3", menuCursor: 4} // cursor on "logout", typed "3" (shell)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if !got.shellMode {
		t.Fatalf("shellMode = false, want true - typed \"3\" (shell) should win over the cursor's \"logout\"")
	}
}

// TestUpdateVersionsSelfUpdateRowStartsUpdateCheck confirms the Update
// screen's ruddervirt-setup row (row 0) still delegates into the existing
// self-update check flow.
func TestUpdateVersionsSelfUpdateRowStartsUpdateCheck(t *testing.T) {
	m := model{current: screenUpdateVersions, updateVersionsCursor: 0}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if got.current != screenUpdateChecking {
		t.Fatalf("current = %v, want screenUpdateChecking", got.current)
	}
	if cmd == nil {
		t.Fatalf("cmd = nil, want checkForUpdateCmd's tea.Cmd")
	}
}

// TestUpdateVersionsApplyRowStartsInstallPlanning confirms the Update
// screen's Apply-upgrades footer (one cursor position past the last real
// row) reuses the same install pipeline Settings' Apply does, and remembers
// its origin so canceling the confirmation returns to this screen.
func TestUpdateVersionsApplyRowStartsInstallPlanning(t *testing.T) {
	m := model{current: screenUpdateVersions, cfg: defaultConfig()}
	m.updateVersionsCursor = len(updateVersionsRows())

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if got.current != screenInstallPlanning {
		t.Fatalf("current = %v, want screenInstallPlanning", got.current)
	}
	if got.installConfirmOrigin != screenUpdateVersions {
		t.Fatalf("installConfirmOrigin = %v, want screenUpdateVersions", got.installConfirmOrigin)
	}
	if cmd == nil {
		t.Fatalf("cmd = nil, want computeInstallPlanCmd's tea.Cmd")
	}
}

// TestVersionFieldsNotInSettingsRows confirms the four component-version
// fields moved out of Settings and onto the Update screen instead.
func TestVersionFieldsNotInSettingsRows(t *testing.T) {
	m := model{cfg: defaultConfig()}
	for _, r := range m.settingsRows() {
		if r.field.updateScreen {
			t.Errorf("settingsRows() includes updateScreen field %q, want it moved to updateVersionsRows", r.field.key)
		}
	}

	rows := updateVersionsRows()
	if len(rows) != 5 { // ruddervirt-setup + 4 version fields
		t.Fatalf("len(updateVersionsRows()) = %d, want 5", len(rows))
	}
	if !rows[0].isSelfUpdate {
		t.Fatalf("updateVersionsRows()[0].isSelfUpdate = false, want true")
	}
	wantKeys := []string{"versions.k3s", "versions.kubevirt", "versions.cdi", "versions.aileron"}
	for i, key := range wantKeys {
		if got := rows[i+1].field.key; got != key {
			t.Errorf("updateVersionsRows()[%d].field.key = %q, want %q", i+1, got, key)
		}
	}
}

// passwordChangeModel builds a minimal model on screenPasswordChange,
// mirroring the fields initialModel sets up for it - a regression fixture
// for the "can't go back to fix a bad password without restarting" bug.
func passwordChangeModel() model {
	newInput := textinput.New()
	confirmInput := textinput.New()
	return model{
		current:              screenPasswordChange,
		passwordNewInput:     newInput,
		passwordConfirmInput: confirmInput,
	}
}

func TestPasswordChangeShortPasswordStaysOnField(t *testing.T) {
	m := passwordChangeModel()
	m.passwordNewInput.SetValue("short")
	m.passwordNewInput.Focus()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if got.passwordConfirmFocus {
		t.Fatalf("passwordConfirmFocus = true, want false - a too-short password must not advance to confirm")
	}
	if got.passwordError == "" {
		t.Fatalf("passwordError = %q, want a length error", got.passwordError)
	}
	if got.passwordNewInput.Value() != "short" {
		t.Fatalf("passwordNewInput.Value() = %q, want unchanged %q", got.passwordNewInput.Value(), "short")
	}
}

func TestPasswordChangeValidPasswordAdvancesToConfirm(t *testing.T) {
	m := passwordChangeModel()
	m.passwordNewInput.SetValue("longenoughpassword")
	m.passwordNewInput.Focus()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if !got.passwordConfirmFocus {
		t.Fatalf("passwordConfirmFocus = false, want true - a valid password should advance to confirm")
	}
	if got.passwordError != "" {
		t.Fatalf("passwordError = %q, want empty", got.passwordError)
	}
}

func TestPasswordChangeEscFromConfirmGoesBackToNewField(t *testing.T) {
	m := passwordChangeModel()
	m.passwordNewInput.SetValue("longenoughpassword")
	m.passwordConfirmInput.SetValue("typo")
	m.passwordConfirmFocus = true
	m.passwordConfirmInput.Focus()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(model)

	if got.current != screenPasswordChange {
		t.Fatalf("current = %v, want screenPasswordChange - Esc from confirm must not exit the flow", got.current)
	}
	if got.passwordConfirmFocus {
		t.Fatalf("passwordConfirmFocus = true, want false after stepping back")
	}
	if got.passwordNewInput.Value() != "longenoughpassword" {
		t.Fatalf("passwordNewInput.Value() = %q, want preserved", got.passwordNewInput.Value())
	}
	if got.passwordConfirmInput.Value() != "" {
		t.Fatalf("passwordConfirmInput.Value() = %q, want cleared", got.passwordConfirmInput.Value())
	}
}

// TestPasswordChangeCtrlSSkipsToSettingsWithoutMarkingChanged confirms
// Ctrl+S lets the operator into Settings without ever setting the default
// password, and - deliberately - without recording PasswordChanged, so
// the next "configure" entry asks again rather than silently treating the
// well-known default password as acceptable going forward.
func TestPasswordChangeCtrlSSkipsToSettingsWithoutMarkingChanged(t *testing.T) {
	m := passwordChangeModel()
	m.cfg = defaultConfig()
	m.passwordNewInput.SetValue("wip")
	m.passwordNewInput.Focus()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := next.(model)

	if got.current != screenSettings {
		t.Fatalf("current = %v, want screenSettings", got.current)
	}
	if got.cfg.System.PasswordChanged {
		t.Fatalf("cfg.System.PasswordChanged = true, want false - skip must not silence future prompts")
	}
	if got.passwordNewInput.Value() != "" {
		t.Fatalf("passwordNewInput.Value() = %q, want cleared", got.passwordNewInput.Value())
	}
}
