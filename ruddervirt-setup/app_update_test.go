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

// aileronVersionsRowCursor finds "versions.aileron"'s position in
// updateVersionsRows() - used instead of a hardcoded index so this test
// doesn't silently start testing the wrong row if the field order ever
// changes.
func aileronVersionsRowCursor(t *testing.T) int {
	t.Helper()
	for i, r := range updateVersionsRows() {
		if !r.isSelfUpdate && r.field.key == "versions.aileron" {
			return i
		}
	}
	t.Fatal("versions.aileron not found in updateVersionsRows()")
	return -1
}

// TestAileronVersionOnceStabilizerDetected confirms the Update screen's
// "Aileron version" row stops being a hard, uneditable "managed by
// stabilizer" lock once a HelmChart named "stabilizer" is detected, and
// instead routes Enter into the guarded chart-version-change flow
// (stabilizer_upgrade.go) - see the versions.aileron special-casing in
// app_update.go/view.go.
func TestAileronVersionOnceStabilizerDetected(t *testing.T) {
	cursor := aileronVersionsRowCursor(t)

	t.Run("before stabilizer is detected, still just a normal locked/pickable field", func(t *testing.T) {
		m := model{current: screenUpdateVersions, updateVersionsCursor: cursor, cfg: defaultConfig()}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.current == screenStabilizerVersionConfirm {
			t.Fatal("must not route into the guarded version flow before stabilizer is detected")
		}
	})

	t.Run("detected but state not loaded yet: Enter is a no-op with an error, not a crash", func(t *testing.T) {
		m := model{current: screenUpdateVersions, updateVersionsCursor: cursor, cfg: defaultConfig(), cachedStabilizerDetected: true}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.current != screenUpdateVersions {
			t.Fatalf("current = %v, want to stay on screenUpdateVersions until state loads", got.current)
		}
		if got.settingsError == "" {
			t.Error("want an explanatory error while state is still loading")
		}
	})

	t.Run("detected, state loaded, but nothing fetched yet: Enter is a no-op with an error", func(t *testing.T) {
		m := model{
			current: screenUpdateVersions, updateVersionsCursor: cursor, cfg: defaultConfig(),
			cachedStabilizerDetected: true,
			stabilizerSettingsState:  &stabilizerSettingsState{declaredVersion: "1.2.3", selfUpgradeEnabled: true},
		}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.updateVersionsPicking {
			t.Fatal("must not open the picker with no fetched releases")
		}
		if got.settingsError == "" {
			t.Error("want an explanatory error")
		}
	})

	t.Run("detected, state loaded, releases fetched: Enter opens a picker of eligible releases", func(t *testing.T) {
		m := model{
			current: screenUpdateVersions, updateVersionsCursor: cursor, cfg: defaultConfig(),
			cachedStabilizerDetected: true,
			cachedAileronVersions:    []string{"v1.4.0", "v1.3.0", "v1.2.3", "v1.2.0"},
			stabilizerSettingsState:  &stabilizerSettingsState{declaredVersion: "1.2.3", selfUpgradeEnabled: true},
		}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if !got.updateVersionsPicking {
			t.Fatal("want the picker to open")
		}
		want := []string{"v1.4.0", "v1.3.0", "v1.2.3"} // v1.2.0 is below the declared 1.2.3 - excluded
		if len(got.updateVersionsPickOptions) != len(want) {
			t.Fatalf("pick options = %v, want %v", got.updateVersionsPickOptions, want)
		}
		for i, w := range want {
			if got.updateVersionsPickOptions[i] != w {
				t.Errorf("pick option[%d] = %q, want %q", i, got.updateVersionsPickOptions[i], w)
			}
		}
	})

	t.Run("picker: choosing a release advances to confirm with the plan attached, v-prefix stripped", func(t *testing.T) {
		m := model{
			current:                       screenUpdateVersions,
			updateVersionsCursor:          cursor,
			cfg:                           defaultConfig(),
			cachedStabilizerDetected:      true,
			updateVersionsPicking:         true,
			updateVersionsPickOptions:     []string{"v1.3.0", "v1.2.3"},
			updateVersionsPickCursor:      0,
			stabilizerSettingsState:       &stabilizerSettingsState{declaredVersion: "1.2.3", selfUpgradeEnabled: true, declaredValues: map[string]any{}},
			stabilizerVersionConfirmInput: textinput.New(),
		}

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.current != screenStabilizerVersionConfirm {
			t.Fatalf("current = %v, want screenStabilizerVersionConfirm", got.current)
		}
		if got.updateVersionsPicking {
			t.Error("picker should be closed")
		}
		if got.stabilizerVersionTarget != "1.3.0" {
			t.Errorf("stabilizerVersionTarget = %q, want 1.3.0 (v-prefix stripped)", got.stabilizerVersionTarget)
		}
		if len(got.stabilizerVersionPatch) == 0 {
			t.Error("want a computed patch carried into the confirm screen")
		}
	})

	t.Run("picker: a refused choice exits the picker with an error, not a crash", func(t *testing.T) {
		m := model{
			current:                   screenUpdateVersions,
			updateVersionsCursor:      cursor,
			cfg:                       defaultConfig(),
			cachedStabilizerDetected:  true,
			updateVersionsPicking:     true,
			updateVersionsPickOptions: []string{"v1.3.0"},
			updateVersionsPickCursor:  0,
			stabilizerSettingsState:   &stabilizerSettingsState{declaredVersion: "1.2.3", selfUpgradeEnabled: false, declaredValues: map[string]any{}},
		}

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.updateVersionsPicking {
			t.Error("picker should be closed on refusal")
		}
		if got.current != screenUpdateVersions {
			t.Fatalf("current = %v, want to stay on screenUpdateVersions", got.current)
		}
		if got.settingsError == "" {
			t.Error("want an error explaining the refusal (self-upgrade disabled)")
		}
	})

	t.Run("confirm screen: typing \"yes\" launches the apply pipeline", func(t *testing.T) {
		m := model{
			current:                       screenStabilizerVersionConfirm,
			cfg:                           defaultConfig(),
			stabilizerSettingsState:       &stabilizerSettingsState{helmChartNamespace: "kube-system", helmChartName: "stabilizer"},
			stabilizerVersionTarget:       "1.3.0",
			stabilizerVersionPatch:        []byte(`{"spec":{"version":"1.3.0"}}`),
			stabilizerVersionConfirmInput: textinput.New(),
		}
		m.stabilizerVersionConfirmInput.SetValue("yes")

		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.current != screenStabilizerVersionApply {
			t.Fatalf("current = %v, want screenStabilizerVersionApply", got.current)
		}
		if len(got.stabilizerVersionApplyPipeline) != 2 {
			t.Fatalf("apply pipeline has %d steps, want 2", len(got.stabilizerVersionApplyPipeline))
		}
		if cmd == nil {
			t.Error("want launchStep's tea.Cmd")
		}
	})

	t.Run("Esc from the version-apply screen (once done) returns to Update, not Settings", func(t *testing.T) {
		m := model{current: screenStabilizerVersionApply, stabilizerVersionApplyDone: true}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		got := next.(model)
		if got.current != screenUpdateVersions {
			t.Fatalf("current = %v, want screenUpdateVersions", got.current)
		}
	})
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
	if len(rows) != 6 { // ruddervirt-setup + OS + 4 version fields
		t.Fatalf("len(updateVersionsRows()) = %d, want 6", len(rows))
	}
	if !rows[0].isSelfUpdate {
		t.Fatalf("updateVersionsRows()[0].isSelfUpdate = false, want true")
	}
	if !rows[1].isOSUpdate {
		t.Fatalf("updateVersionsRows()[1].isOSUpdate = false, want true")
	}
	wantKeys := []string{"versions.k3s", "versions.kubevirt", "versions.cdi", "versions.aileron"}
	for i, key := range wantKeys {
		if got := rows[i+2].field.key; got != key {
			t.Errorf("updateVersionsRows()[%d].field.key = %q, want %q", i+2, got, key)
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
