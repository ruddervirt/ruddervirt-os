// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/tui/pipeline"
	versionspkg "ruddervirt-setup/internal/versions"
)

func TestUpdateModelViewChecking(t *testing.T) {
	m := UpdateModel{}
	if out := m.ViewChecking(); !strings.Contains(out, "Checking for updates") {
		t.Errorf("ViewChecking() = %q, want it to mention checking for updates", out)
	}
}

func TestUpdateModelViewConfirm(t *testing.T) {
	m := UpdateModel{
		LatestVersion: "v9.9.9",
		ConfirmInput:  textinput.New(),
		ConfirmError:  "boom",
	}
	out := m.ViewConfirm()
	if !strings.Contains(out, versionspkg.Version) {
		t.Errorf("ViewConfirm() = %q, want the current version shown", out)
	}
	if !strings.Contains(out, "v9.9.9") {
		t.Errorf("ViewConfirm() = %q, want the latest version shown", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("ViewConfirm() = %q, want the confirm error shown", out)
	}
}

// TestUpdateModelPipelineWiringAdvancesToDone/StopsOnFailure mirror
// TestInstallModelPipelineWiringAdvancesToDone/StopsOnFailure - see
// install_test.go's installStep/drainPipeline helpers (reused here) for why
// these use synthetic steps rather than the real selfupdate.UpdateSteps.
func TestUpdateModelPipelineWiringAdvancesToDone(t *testing.T) {
	steps := []installsteps.Step{installStep("first", nil), installStep("second", nil)}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	m := UpdateModel{ConfirmInput: textinput.New(), Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Done {
		t.Fatal("Pipeline.Done = false, want true after both steps succeed")
	}
	out := m.ViewRunning(10)
	if !strings.Contains(out, "Update complete.") {
		t.Errorf("ViewRunning() = %q, want the success message", out)
	}
}

func TestUpdateModelPipelineWiringStopsOnFailure(t *testing.T) {
	steps := []installsteps.Step{installStep("first", errors.New("boom"))}
	cfg := config.Config{}

	pl, cmd := pipeline.New(steps, cfg)
	m := UpdateModel{ConfirmInput: textinput.New(), Pipeline: pl}

	m.Pipeline = drainPipeline(t, m.Pipeline, cmd, cfg)

	if !m.Pipeline.Failed {
		t.Fatal("Pipeline.Failed = false, want true after a step errors")
	}
	out := m.ViewRunning(10)
	if !strings.Contains(out, "Update failed.") {
		t.Errorf("ViewRunning() = %q, want the failure message", out)
	}
	if !strings.Contains(out, "Press Esc to return to menu.") {
		t.Errorf("ViewRunning() = %q, want the return-to-menu hint on failure", out)
	}
}

func TestUpdateModelUpdateForwardsToConfirmInput(t *testing.T) {
	m := UpdateModel{ConfirmInput: textinput.New()}
	m.ConfirmInput.Focus()

	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("yes")})
	if got.ConfirmInput.Value() != "yes" {
		t.Fatalf("ConfirmInput.Value() = %q, want %q - typing must forward to ConfirmInput", got.ConfirmInput.Value(), "yes")
	}
}

func TestUpdateModelClearConfirmLeavesTableStateAlone(t *testing.T) {
	m := UpdateModel{
		Cursor: 2, Scroll: 1, LatestVersion: "v9.9.9",
		ConfirmInput: textinput.New(),
		ConfirmError: `Type "yes" to proceed, or Esc to cancel.`,
	}
	m.ConfirmInput.SetValue("yes")
	m.ConfirmInput.Focus()

	got := m.ClearConfirm()

	if got.ConfirmInput.Value() != "" || got.ConfirmInput.Focused() {
		t.Errorf("ConfirmInput = %q/focused=%v, want cleared and blurred", got.ConfirmInput.Value(), got.ConfirmInput.Focused())
	}
	if got.ConfirmError != "" {
		t.Errorf("ConfirmError = %q, want cleared", got.ConfirmError)
	}
	if got.Cursor != 2 || got.Scroll != 1 {
		t.Errorf("Cursor/Scroll = %d/%d, want left untouched at 2/1", got.Cursor, got.Scroll)
	}
	if got.LatestVersion != "v9.9.9" {
		t.Errorf("LatestVersion = %q, want left untouched", got.LatestVersion)
	}
}

func TestUpdateModelResetClearsEveryField(t *testing.T) {
	m := UpdateModel{
		Cursor: 2, Scroll: 1, Picking: true, PickCursor: 3, PickScroll: 1,
		PickOptions:   []string{"a", "b"},
		Checking:      true,
		CheckErr:      "boom",
		LatestVersion: "v9.9.9",
		BinaryURL:     "https://example.com/bin",
		ChecksumHex:   "deadbeef",
		AlreadyLatest: true,
		ConfirmInput:  textinput.New(),
		ConfirmError:  `Type "yes" to proceed, or Esc to cancel.`,
		Pipeline:      pipeline.Model{Done: true},
		Installed:     false,
	}
	m.ConfirmInput.SetValue("yes")
	m.ConfirmInput.Focus()

	got := m.Reset()

	if got.Cursor != 0 || got.Scroll != 0 {
		t.Errorf("Cursor/Scroll = %d/%d, want 0/0", got.Cursor, got.Scroll)
	}
	if got.Picking || got.PickCursor != 0 || got.PickScroll != 0 {
		t.Errorf("Picking/PickCursor/PickScroll = %v/%d/%d, want false/0/0", got.Picking, got.PickCursor, got.PickScroll)
	}
	if got.Checking {
		t.Error("Checking = true, want false")
	}
	if got.CheckErr != "" || got.LatestVersion != "" || got.BinaryURL != "" || got.ChecksumHex != "" {
		t.Errorf("CheckErr/LatestVersion/BinaryURL/ChecksumHex = %q/%q/%q/%q, want all cleared", got.CheckErr, got.LatestVersion, got.BinaryURL, got.ChecksumHex)
	}
	if got.AlreadyLatest {
		t.Error("AlreadyLatest = true, want false")
	}
	if got.ConfirmInput.Value() != "" || got.ConfirmInput.Focused() {
		t.Errorf("ConfirmInput = %q/focused=%v, want cleared and blurred", got.ConfirmInput.Value(), got.ConfirmInput.Focused())
	}
	if got.ConfirmError != "" {
		t.Errorf("ConfirmError = %q, want cleared", got.ConfirmError)
	}
	if got.Pipeline.Done {
		t.Error("Pipeline.Done = true, want the zero-value pipeline")
	}
}

func TestUpdateRows(t *testing.T) {
	rows := UpdateRows()
	if len(rows) != 8 { // ruddervirt-setup + OS + 6 version fields
		t.Fatalf("len(UpdateRows()) = %d, want 8", len(rows))
	}
	if !rows[0].IsSelfUpdate {
		t.Errorf("UpdateRows()[0].IsSelfUpdate = false, want true")
	}
	if !rows[1].IsOSUpdate {
		t.Errorf("UpdateRows()[1].IsOSUpdate = false, want true")
	}
	for _, r := range rows[2:] {
		if !r.Field.UpdateScreen {
			t.Errorf("UpdateRows() row %q isn't tagged UpdateScreen", r.Field.Key)
		}
	}
}

// TestUpdateModelUpDown confirms Up/Down move Cursor (table) or PickCursor
// (picker) clamped to their respective bounds, and re-clamp scroll.
func TestUpdateModelUpDown(t *testing.T) {
	t.Run("table cursor clamps at 0 and at Apply (len(UpdateRows()))", func(t *testing.T) {
		m := UpdateModel{}
		m = m.Up(5)
		if m.Cursor != 0 {
			t.Fatalf("Cursor after Up at 0 = %d, want 0 (clamped)", m.Cursor)
		}
		last := len(UpdateRows())
		for i := 0; i < last+3; i++ {
			m = m.Down(5)
		}
		if m.Cursor != last {
			t.Fatalf("Cursor after repeated Down = %d, want %d (clamped at Apply)", m.Cursor, last)
		}
	})

	t.Run("picker cursor clamps within PickOptions instead of the table", func(t *testing.T) {
		m := UpdateModel{Picking: true, PickOptions: []string{"a", "b", "c"}, Cursor: 4}
		m = m.Down(5)
		m = m.Down(5)
		m = m.Down(5)
		m = m.Down(5)
		if m.PickCursor != 2 {
			t.Fatalf("PickCursor after repeated Down = %d, want 2 (clamped at len(PickOptions)-1)", m.PickCursor)
		}
		if m.Cursor != 4 {
			t.Fatalf("Cursor = %d, want unchanged (4) while Picking", m.Cursor)
		}
		m = m.Up(5)
		if m.PickCursor != 1 {
			t.Fatalf("PickCursor after Up = %d, want 1", m.PickCursor)
		}
	})
}

// TestUpdateModelViewVersionsUpgradeIcon confirms ViewVersions marks the
// self-update/OS rows with the "↑ available upgrade" icon exactly when
// their corresponding SelfUpdateAvailable/OSUpdateAvailable param is true
// (the equivalent full-dispatch smoke test is view_test.go's
// TestUpdateScreenUpgradeIcon).
func TestUpdateModelViewVersionsUpgradeIcon(t *testing.T) {
	m := UpdateModel{}
	out := m.ViewVersions(UpdateViewParams{
		Cfg:                 config.DefaultConfig(),
		SelfUpdateAvailable: true,
		OSUpdateAvailable:   false,
		TermWidth:           120,
		VisibleRows:         20,
	})
	lines := strings.Split(out, "\n")
	findRowLine := func(label string) string {
		for _, l := range lines {
			if strings.Contains(l, label) {
				return l
			}
		}
		t.Fatalf("row %q not found in rendered output:\n%s", label, out)
		return ""
	}
	if !strings.Contains(findRowLine("ruddervirt-setup"), "↑") {
		t.Error("ruddervirt-setup row should show the upgrade icon (SelfUpdateAvailable=true)")
	}
	if strings.Contains(findRowLine("OS Packages"), "↑") {
		t.Error("OS Packages row should NOT show the upgrade icon (OSUpdateAvailable=false)")
	}
}
