// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec/exectest"
	"ruddervirt-setup/internal/power"
	"ruddervirt-setup/internal/stabilizer/settings"
	"ruddervirt-setup/internal/tui/pipeline"
	"ruddervirt-setup/internal/tui/screens"
)

// TestMenuUpdateOpensUpdateVersionsScreen is a regression test for a bug
// where selecting "update" from the main menu fell through to the generic
// default label branch, just showing the literal text "update" as a
// result instead of opening the Update screen.
func TestMenuUpdateOpensUpdateVersionsScreen(t *testing.T) {
	m := model{current: screenMenu, menu: screens.MenuModel{Input: "update"}}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if got.current != screenUpdateVersions {
		t.Fatalf("current = %v, want screenUpdateVersions", got.current)
	}
	if got.menu.Result == "update" {
		t.Fatalf("menu.Result = %q - selecting update must not just echo the label back as a result", got.menu.Result)
	}
}

// TestMenuArrowNavigation confirms ↑/↓ move menu.Cursor (clamped, no wrap)
// and Enter with no typed input submits whatever's highlighted.
func TestMenuArrowNavigation(t *testing.T) {
	m := model{current: screenMenu}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := next.(model).menu.Cursor; got != 0 {
		t.Fatalf("menu.Cursor after Up at 0 = %d, want 0 (clamped)", got)
	}

	m.menu.Cursor = 0
	for i := 0; i < len(screens.MenuOrder)+2; i++ {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(model)
	}
	if m.menu.Cursor != len(screens.MenuOrder)-1 {
		t.Fatalf("menu.Cursor after Down x%d = %d, want %d (clamped)", len(screens.MenuOrder)+2, m.menu.Cursor, len(screens.MenuOrder)-1)
	}

	// menu.Cursor is on "power options" (last item) - Enter with no typed
	// input should submit it directly, same as typing "5"/"power options"
	// would, landing on screenPowerOptions.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := next.(model).current; got != screenPowerOptions {
		t.Fatalf("current after Enter on \"power options\" = %v, want screenPowerOptions", got)
	}
}

// TestMenuTypedInputWinsOverCursor confirms Enter prefers typed input over
// the ↑/↓ cursor when both are present.
func TestMenuTypedInputWinsOverCursor(t *testing.T) {
	m := model{current: screenMenu, menu: screens.MenuModel{Input: "3", Cursor: 4}} // cursor on "power options", typed "3" (shell)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if !got.shellMode {
		t.Fatalf("shellMode = false, want true - typed \"3\" (shell) should win over the cursor's \"power options\"")
	}
}

// TestUpdateVersionsSelfUpdateRowStartsUpdateCheck confirms the Update
// screen's ruddervirt-setup row (row 0) still delegates into the existing
// self-update check flow.
func TestUpdateVersionsSelfUpdateRowStartsUpdateCheck(t *testing.T) {
	m := model{current: screenUpdateVersions, update: screens.UpdateModel{Cursor: 0}}

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
	m := model{current: screenUpdateVersions, cfg: config.DefaultConfig()}
	m.update.Cursor = len(screens.UpdateRows())

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
// screens.UpdateRows() - used instead of a hardcoded index so this test
// doesn't silently start testing the wrong row if the field order ever
// changes.
func aileronVersionsRowCursor(t *testing.T) int {
	t.Helper()
	for i, r := range screens.UpdateRows() {
		if !r.IsSelfUpdate && r.Field.Key == "versions.aileron" {
			return i
		}
	}
	t.Fatal("versions.aileron not found in screens.UpdateRows()")
	return -1
}

// TestAileronVersionOnceStabilizerDetected confirms the Update screen's
// "Aileron version" row stops being a hard, uneditable "managed by
// stabilizer" lock once a HelmChart named "stabilizer" is detected, and
// instead routes Enter into the guarded chart-version-change flow
// (stabilizer_upgrade.go) - see the versions.aileron special-casing in
// app.go/view.go.
func TestAileronVersionOnceStabilizerDetected(t *testing.T) {
	cursor := aileronVersionsRowCursor(t)

	t.Run("before stabilizer is detected, still just a normal locked/pickable field", func(t *testing.T) {
		m := model{current: screenUpdateVersions, update: screens.UpdateModel{Cursor: cursor}, cfg: config.DefaultConfig()}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.current == screenStabilizerVersionConfirm {
			t.Fatal("must not route into the guarded version flow before stabilizer is detected")
		}
	})

	t.Run("detected but state not loaded yet: Enter is a no-op with an error, not a crash", func(t *testing.T) {
		m := model{current: screenUpdateVersions, update: screens.UpdateModel{Cursor: cursor}, cfg: config.DefaultConfig(), cachedStabilizerDetected: true}
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
			current: screenUpdateVersions, update: screens.UpdateModel{Cursor: cursor}, cfg: config.DefaultConfig(),
			cachedStabilizerDetected: true,
			stabilizerSettingsState:  &settings.StabilizerSettingsState{DeclaredVersion: "1.2.3", SelfUpgradeEnabled: true},
		}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.update.Picking {
			t.Fatal("must not open the picker with no fetched releases")
		}
		if got.settingsError == "" {
			t.Error("want an explanatory error")
		}
	})

	t.Run("detected, state loaded, releases fetched: Enter opens a picker of eligible releases", func(t *testing.T) {
		m := model{
			current: screenUpdateVersions, update: screens.UpdateModel{Cursor: cursor}, cfg: config.DefaultConfig(),
			cachedStabilizerDetected: true,
			cachedAileronVersions:    []string{"v1.4.0", "v1.3.0", "v1.2.3", "v1.2.0"},
			stabilizerSettingsState:  &settings.StabilizerSettingsState{DeclaredVersion: "1.2.3", SelfUpgradeEnabled: true},
		}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if !got.update.Picking {
			t.Fatal("want the picker to open")
		}
		want := []string{"v1.4.0", "v1.3.0", "v1.2.3"} // v1.2.0 is below the declared 1.2.3 - excluded
		if len(got.update.PickOptions) != len(want) {
			t.Fatalf("pick options = %v, want %v", got.update.PickOptions, want)
		}
		for i, w := range want {
			if got.update.PickOptions[i] != w {
				t.Errorf("pick option[%d] = %q, want %q", i, got.update.PickOptions[i], w)
			}
		}
	})

	t.Run("picker: choosing a release advances to confirm with the plan attached, v-prefix stripped", func(t *testing.T) {
		m := model{
			current: screenUpdateVersions,
			update: screens.UpdateModel{
				Cursor:      cursor,
				Picking:     true,
				PickOptions: []string{"v1.3.0", "v1.2.3"},
				PickCursor:  0,
			},
			cfg:                      config.DefaultConfig(),
			cachedStabilizerDetected: true,
			stabilizerSettingsState:  &settings.StabilizerSettingsState{DeclaredVersion: "1.2.3", SelfUpgradeEnabled: true, DeclaredValues: map[string]any{}},
			stabilizerVersion:        screens.StabilizerVersionModel{ConfirmInput: textinput.New()},
		}

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.current != screenStabilizerVersionConfirm {
			t.Fatalf("current = %v, want screenStabilizerVersionConfirm", got.current)
		}
		if got.update.Picking {
			t.Error("picker should be closed")
		}
		if got.stabilizerVersion.Target != "1.3.0" {
			t.Errorf("stabilizerVersion.Target = %q, want 1.3.0 (v-prefix stripped)", got.stabilizerVersion.Target)
		}
		if len(got.stabilizerVersion.Patch) == 0 {
			t.Error("want a computed patch carried into the confirm screen")
		}
	})

	t.Run("picker: a refused choice exits the picker with an error, not a crash", func(t *testing.T) {
		m := model{
			current: screenUpdateVersions,
			update: screens.UpdateModel{
				Cursor:      cursor,
				Picking:     true,
				PickOptions: []string{"v1.3.0"},
				PickCursor:  0,
			},
			cfg:                      config.DefaultConfig(),
			cachedStabilizerDetected: true,
			stabilizerSettingsState:  &settings.StabilizerSettingsState{DeclaredVersion: "1.2.3", SelfUpgradeEnabled: false, DeclaredValues: map[string]any{}},
		}

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.update.Picking {
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
		// pipeline.New starts step.Run in its own goroutine. Without a fake
		// runner it would shell out for real; without draining cmd to
		// stepDoneMsg, that goroutine could still be reading
		// exec.DefaultRunner after this subtest returns, racing a later
		// test's DefaultRunner swap (e.g. TestFetchVMCounts).
		exectest.WithFakeRunner(&exectest.FakeRunner{}, func() {
			m := model{
				current:                 screenStabilizerVersionConfirm,
				cfg:                     config.DefaultConfig(),
				stabilizerSettingsState: &settings.StabilizerSettingsState{HelmChartNamespace: "kube-system", HelmChartName: "stabilizer"},
				stabilizerVersion: screens.StabilizerVersionModel{
					Target:       "1.3.0",
					Patch:        []byte(`{"spec":{"version":"1.3.0"}}`),
					ConfirmInput: textinput.New(),
				},
			}
			m.stabilizerVersion.ConfirmInput.SetValue("yes")

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			got := next.(model)
			if got.current != screenStabilizerVersionApply {
				t.Fatalf("current = %v, want screenStabilizerVersionApply", got.current)
			}
			if len(got.stabilizerVersion.Pipeline.Steps) != 2 {
				t.Fatalf("apply pipeline has %d steps, want 2", len(got.stabilizerVersion.Pipeline.Steps))
			}
			if cmd == nil {
				t.Fatal("want pipeline.New's tea.Cmd")
			}
			for {
				if _, done := cmd().(stepDoneMsg); done {
					break
				}
			}
		})
	})

	t.Run("Esc from the version-apply screen (once done) returns to Update, not Settings", func(t *testing.T) {
		m := model{current: screenStabilizerVersionApply, stabilizerVersion: screens.StabilizerVersionModel{Pipeline: pipeline.Model{Done: true}}}
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
	m := model{cfg: config.DefaultConfig()}
	for _, r := range m.settings.Rows(m.cfg, m.cachedStabilizerDetected) {
		if r.Field.UpdateScreen {
			t.Errorf("settings.Rows() includes updateScreen field %q, want it moved to screens.UpdateRows", r.Field.Key)
		}
	}

	rows := screens.UpdateRows()
	if len(rows) != 8 { // ruddervirt-setup + OS + 6 version fields
		t.Fatalf("len(screens.UpdateRows()) = %d, want 8", len(rows))
	}
	if !rows[0].IsSelfUpdate {
		t.Fatalf("screens.UpdateRows()[0].IsSelfUpdate = false, want true")
	}
	if !rows[1].IsOSUpdate {
		t.Fatalf("screens.UpdateRows()[1].IsOSUpdate = false, want true")
	}
	wantKeys := []string{"versions.k3s", "versions.kubeovn", "versions.multus", "versions.kubevirt", "versions.cdi", "versions.aileron"}
	for i, key := range wantKeys {
		if got := rows[i+2].Field.Key; got != key {
			t.Errorf("screens.UpdateRows()[%d].Field.Key = %q, want %q", i+2, got, key)
		}
	}
}

// passwordChangeModel builds a minimal model on screenPasswordChange,
// mirroring the fields initialModel sets up for it - a regression fixture
// for the "can't go back to fix a bad password without restarting" bug.
//
// Only the two cross-group-routing tests below (Esc-from-confirm and
// Ctrl+S-skip, which transition m.current and touch other groups' fields)
// stay here; screen-local Enter-validation tests moved to
// internal/tui/screens/password_test.go.
func passwordChangeModel() model {
	newInput := textinput.New()
	confirmInput := textinput.New()
	return model{
		current: screenPasswordChange,
		password: screens.PasswordModel{
			NewInput:     newInput,
			ConfirmInput: confirmInput,
		},
	}
}

func TestPasswordChangeEscFromConfirmGoesBackToNewField(t *testing.T) {
	m := passwordChangeModel()
	m.password.NewInput.SetValue("longenoughpassword")
	m.password.ConfirmInput.SetValue("typo")
	m.password.ConfirmFocus = true
	m.password.ConfirmInput.Focus()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(model)

	if got.current != screenPasswordChange {
		t.Fatalf("current = %v, want screenPasswordChange - Esc from confirm must not exit the flow", got.current)
	}
	if got.password.ConfirmFocus {
		t.Fatalf("password.ConfirmFocus = true, want false after stepping back")
	}
	if got.password.NewInput.Value() != "longenoughpassword" {
		t.Fatalf("password.NewInput.Value() = %q, want preserved", got.password.NewInput.Value())
	}
	if got.password.ConfirmInput.Value() != "" {
		t.Fatalf("password.ConfirmInput.Value() = %q, want cleared", got.password.ConfirmInput.Value())
	}
}

// TestPasswordChangeCtrlSSkipsToSettingsWithoutMarkingChanged confirms
// Ctrl+S lets the operator into Settings without ever setting the default
// password, and - deliberately - without recording PasswordChanged, so
// the next "configure" entry asks again rather than silently treating the
// well-known default password as acceptable going forward.
func TestPasswordChangeCtrlSSkipsToSettingsWithoutMarkingChanged(t *testing.T) {
	m := passwordChangeModel()
	m.cfg = config.DefaultConfig()
	m.password.NewInput.SetValue("wip")
	m.password.NewInput.Focus()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := next.(model)

	if got.current != screenSettings {
		t.Fatalf("current = %v, want screenSettings", got.current)
	}
	if got.cfg.System.PasswordChanged {
		t.Fatalf("cfg.System.PasswordChanged = true, want false - skip must not silence future prompts")
	}
	if got.password.NewInput.Value() != "" {
		t.Fatalf("password.NewInput.Value() = %q, want cleared", got.password.NewInput.Value())
	}
}

// TestPowerOptionsDisconnectQuits confirms "power options" -> "Disconnect"
// still just ends the TUI session, same as the old bare "logout" menu entry.
func TestPowerOptionsDisconnectQuits(t *testing.T) {
	m := model{current: screenPowerOptions, power: screens.PowerModel{Cursor: 2}} // 2 = "Disconnect" (screens.PowerOrder)

	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Fatal("cmd = nil, want tea.Quit (Disconnect)")
	}
}

// TestPowerOptionsRebootConfirmLaunchesPipeline confirms "power options" ->
// "Reboot" requires typing "yes" before launching power.RebootSteps, and
// that Esc before confirming cancels back to the options list without
// running anything.
func TestPowerOptionsRebootConfirmLaunchesPipeline(t *testing.T) {
	t.Run("Esc from the options list cancels back to the menu", func(t *testing.T) {
		m := model{current: screenPowerOptions, power: screens.PowerModel{Cursor: 1}} // 1 = "Reboot"
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		got := next.(model)
		if got.current != screenMenu {
			t.Fatalf("current = %v, want screenMenu", got.current)
		}
	})

	t.Run("Reboot row opens the confirm screen, Esc cancels back to the options list", func(t *testing.T) {
		m := model{current: screenPowerOptions, power: screens.PowerModel{Cursor: 1, ConfirmInput: textinput.New()}}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.current != screenPowerConfirm {
			t.Fatalf("current = %v, want screenPowerConfirm", got.current)
		}
		if got.power.Action != "reboot" {
			t.Fatalf("power.Action = %q, want \"reboot\"", got.power.Action)
		}

		next, _ = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
		got = next.(model)
		if got.current != screenPowerOptions {
			t.Fatalf("current = %v, want screenPowerOptions", got.current)
		}
	})

	t.Run("typing \"yes\" launches power.RebootSteps", func(t *testing.T) {
		// Same fake-runner-and-drain reasoning as the stabilizer-version
		// confirm test above: pipeline.New starts Run in its own goroutine,
		// which would otherwise really shell out to systemctl.
		exectest.WithFakeRunner(&exectest.FakeRunner{}, func() {
			m := model{
				current: screenPowerConfirm,
				cfg:     config.DefaultConfig(),
				power:   screens.PowerModel{Action: "reboot", ConfirmInput: textinput.New()},
			}
			m.power.ConfirmInput.SetValue("yes")

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			got := next.(model)
			if got.current != screenPowerApply {
				t.Fatalf("current = %v, want screenPowerApply", got.current)
			}
			if len(got.power.Pipeline.Steps) != 1 || got.power.Pipeline.Steps[0].Label != power.RebootStepLabel {
				t.Fatalf("Pipeline.Steps = %+v, want a single %q step", got.power.Pipeline.Steps, power.RebootStepLabel)
			}
			if cmd == nil {
				t.Fatal("want pipeline.New's tea.Cmd")
			}
			for {
				if _, done := cmd().(stepDoneMsg); done {
					break
				}
			}
		})
	})
}

// TestOSPackagesAutoUpdateNotice confirms selecting the OS Packages row
// warns first (screenOSUpdateConfirm) when automatic OS package updates are
// already on, but launches OSUpdateSteps directly - same as before this
// notice existed - when they're off.
func TestOSPackagesAutoUpdateNotice(t *testing.T) {
	t.Run("AutoUpdate on: Enter shows the notice instead of updating directly", func(t *testing.T) {
		m := model{
			current: screenUpdateVersions,
			cfg:     config.Config{System: config.SystemConfig{AutoUpdate: true}},
			update:  screens.UpdateModel{Cursor: 1}, // 1 = OS Packages row (screens.UpdateRows)
		}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(model)
		if got.current != screenOSUpdateConfirm {
			t.Fatalf("current = %v, want screenOSUpdateConfirm", got.current)
		}
		if got.osUpdate.Pipeline.Steps != nil {
			t.Fatalf("Pipeline.Steps = %+v, want nil - must not launch until the notice is acknowledged", got.osUpdate.Pipeline.Steps)
		}

		// Enter on the notice proceeds anyway.
		exectest.WithFakeRunner(&exectest.FakeRunner{}, func() {
			next, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
			got = next.(model)
			if got.current != screenOSUpdate {
				t.Fatalf("current = %v, want screenOSUpdate", got.current)
			}
			if cmd == nil {
				t.Fatal("want pipeline.New's tea.Cmd")
			}
			for {
				if _, done := cmd().(stepDoneMsg); done {
					break
				}
			}
		})
	})

	t.Run("AutoUpdate off: Enter updates directly, no notice", func(t *testing.T) {
		exectest.WithFakeRunner(&exectest.FakeRunner{}, func() {
			m := model{
				current: screenUpdateVersions,
				cfg:     config.Config{System: config.SystemConfig{AutoUpdate: false}},
				update:  screens.UpdateModel{Cursor: 1},
			}
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			got := next.(model)
			if got.current != screenOSUpdate {
				t.Fatalf("current = %v, want screenOSUpdate", got.current)
			}
			for {
				if _, done := cmd().(stepDoneMsg); done {
					break
				}
			}
		})
	})
}

// TestOSUpdateRebootShortcut confirms pressing "r" once OS Packages has
// finished updating routes into the same reboot confirm+apply flow as
// "power options" -> "Reboot" - see model.go's powerConfirmOrigin doc
// comment - and that Esc cancels back to screenOSUpdate (not screenPowerOptions)
// since that's where this shortcut was actually reached from.
func TestOSUpdateRebootShortcut(t *testing.T) {
	m := model{current: screenOSUpdate, osUpdate: screens.OSUpdateModel{Pipeline: pipeline.Model{Done: true}}, power: screens.PowerModel{ConfirmInput: textinput.New()}}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	got := next.(model)
	if got.current != screenPowerConfirm {
		t.Fatalf("current = %v, want screenPowerConfirm", got.current)
	}
	if got.power.Action != "reboot" {
		t.Fatalf("power.Action = %q, want \"reboot\"", got.power.Action)
	}

	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = next.(model)
	if got.current != screenOSUpdate {
		t.Fatalf("current = %v, want screenOSUpdate (where the shortcut was pressed), not screenPowerOptions", got.current)
	}
}
