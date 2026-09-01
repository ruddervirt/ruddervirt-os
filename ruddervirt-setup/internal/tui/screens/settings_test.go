// SPDX-License-Identifier: GPL-3.0-only

package screens

import (
	"strings"
	"testing"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/network"
	"ruddervirt-setup/internal/stabilizer/settings"
)

func TestNetworkSetupLabel(t *testing.T) {
	if got := NetworkSetupLabel(false); got != "▶ Local physical network setup" {
		t.Errorf("NetworkSetupLabel(false) = %q", got)
	}
	if got := NetworkSetupLabel(true); got != "▼ Local physical network setup" {
		t.Errorf("NetworkSetupLabel(true) = %q", got)
	}
}

func TestAdvancedSettingsLabel(t *testing.T) {
	if got := AdvancedSettingsLabel(false); got != "▶ Advanced settings" {
		t.Errorf("AdvancedSettingsLabel(false) = %q", got)
	}
	if got := AdvancedSettingsLabel(true); got != "▼ Advanced settings" {
		t.Errorf("AdvancedSettingsLabel(true) = %q", got)
	}
}

func TestSettingsModelResetClearsNavigationButNotPicking(t *testing.T) {
	m := SettingsModel{
		Cursor: 5, Scroll: 2, Editing: true, ShowAdvanced: true, ShowNetwork: true,
		Picking: true, PickCursor: 3, PickScroll: 1, PickOptions: []string{"a", "b"},
	}
	got := m.Reset()

	if got.Cursor != 0 {
		t.Errorf("Cursor = %d, want 0", got.Cursor)
	}
	if got.Scroll != 0 {
		t.Errorf("Scroll = %d, want 0", got.Scroll)
	}
	if got.Editing {
		t.Error("Editing = true, want false")
	}
	if got.ShowAdvanced {
		t.Error("ShowAdvanced = true, want false")
	}
	if got.ShowNetwork {
		t.Error("ShowNetwork = true, want false")
	}
	// Reset deliberately leaves the picker fields alone - see its doc
	// comment for why.
	if !got.Picking {
		t.Error("Picking = false, want left untouched (true)")
	}
	if got.PickCursor != 3 {
		t.Errorf("PickCursor = %d, want left untouched at 3", got.PickCursor)
	}
	if got.PickScroll != 1 {
		t.Errorf("PickScroll = %d, want left untouched at 1", got.PickScroll)
	}
	if len(got.PickOptions) != 2 {
		t.Errorf("PickOptions = %v, want left untouched", got.PickOptions)
	}
}

// TestSettingsModelRows covers SettingsModel.Rows.
func TestSettingsModelRows(t *testing.T) {
	cfg := config.Config{Network: network.NetworkConfig{Addressing: "dhcp"}}
	base := SettingsModel{}

	t.Run("collapsed groups show only toggle rows plus plain fields - Apply isn't one of these rows", func(t *testing.T) {
		rows := base.Rows(cfg, false)
		if !rows[0].IsNetworkToggle {
			t.Fatalf("rows[0].IsNetworkToggle = false, want true")
		}
		for _, r := range rows[1:] {
			if r.Nested {
				t.Errorf("expected no nested rows while collapsed, got nested row for key %q", r.Field.Key)
			}
		}
	})

	t.Run("expanding network reveals its fields but not staticOnly ones under dhcp", func(t *testing.T) {
		m := base
		m.ShowNetwork = true
		rows := m.Rows(cfg, false)
		var nestedKeys []string
		for _, r := range rows {
			if r.Nested {
				nestedKeys = append(nestedKeys, r.Field.Key)
			}
		}
		want := []string{"network.interface_name", "network.addressing"}
		if len(nestedKeys) != len(want) {
			t.Fatalf("nested keys = %v, want %v", nestedKeys, want)
		}
		for i, k := range want {
			if nestedKeys[i] != k {
				t.Errorf("nested key[%d] = %q, want %q", i, nestedKeys[i], k)
			}
		}
	})

	t.Run("static addressing also reveals staticOnly fields when network expanded", func(t *testing.T) {
		m := base
		m.ShowNetwork = true
		staticCfg := cfg
		staticCfg.Network.Addressing = "static"
		rows := m.Rows(staticCfg, false)
		count := 0
		for _, r := range rows {
			if r.Nested {
				count++
			}
		}
		if count != 6 { // 2 networkSetup fields + 4 staticOnly fields
			t.Errorf("nested row count = %d, want 6", count)
		}
	})

	t.Run("advanced settings only shown when expanded", func(t *testing.T) {
		collapsed := base.Rows(cfg, false)
		m := base
		m.ShowAdvanced = true
		expanded := m.Rows(cfg, false)
		// pod_cidr, svc_cidr, storage.engine, and (not yet detected) the
		// stabilizer action row - all four now live under Advanced.
		if len(expanded) != len(collapsed)+4 {
			t.Errorf("expanded rows = %d, collapsed = %d, want a difference of 4", len(expanded), len(collapsed))
		}
	})

	t.Run("stabilizer action row lives under Advanced, shown until detected", func(t *testing.T) {
		collapsed := base
		collapsed.ShowAdvanced = false
		if hasStabilizerActionRow(collapsed.Rows(cfg, false)) {
			t.Error("stabilizer action row must not appear while Advanced is collapsed")
		}

		notDetected := base
		notDetected.ShowAdvanced = true
		if !hasStabilizerActionRow(notDetected.Rows(cfg, false)) {
			t.Error("stabilizer action row missing while not yet detected (Advanced expanded)")
		}
		if countStabilizerDefRows(notDetected.Rows(cfg, false)) != 0 {
			t.Error("no stabilizer setting rows should appear before adoption")
		}

		detected := base
		detected.ShowAdvanced = true
		if hasStabilizerActionRow(detected.Rows(cfg, true)) {
			t.Error("stabilizer action row still present after stabilizer was detected")
		}
		// Every stabilizer setting becomes an in-situ row once detected -
		// no separate action row leading anywhere.
		if got, want := countStabilizerDefRows(detected.Rows(cfg, true)), len(settings.StabilizerSettingDefs); got != want {
			t.Errorf("stabilizer setting rows = %d, want %d (one per settings.StabilizerSettingDefs entry)", got, want)
		}
	})

	t.Run("aileron_ui_enabled is replaced in place once detected, never duplicated", func(t *testing.T) {
		notDetected := base
		notDetected.ShowAdvanced = true
		if hasStabilizerDefRow(notDetected.Rows(cfg, false), "aileron_ui_enabled") {
			t.Error("aileron_ui_enabled shouldn't be stabilizer-backed before adoption")
		}
		if !hasConfigFieldRow(notDetected.Rows(cfg, false), "system.aileron_ui_enabled") {
			t.Error("the Config-backed system.aileron_ui_enabled row should still be present before adoption")
		}

		detected := base
		detected.ShowAdvanced = true
		rows := detected.Rows(cfg, true)
		if hasConfigFieldRow(rows, "system.aileron_ui_enabled") {
			t.Error("the old Config-backed row must be gone once stabilizer is detected - not shown twice")
		}
		var aileronUIRows int
		var aileronUINested bool
		for _, r := range rows {
			if r.StabilizerDef != nil && r.StabilizerDef.Key == "aileron_ui_enabled" {
				aileronUIRows++
				aileronUINested = r.Nested
			}
		}
		if aileronUIRows != 1 {
			t.Errorf("aileron_ui_enabled appears %d times, want exactly 1", aileronUIRows)
		}
		if aileronUINested {
			t.Error("aileron_ui_enabled should stay in its original plain (non-nested) slot, not move under Advanced")
		}
	})
}

func hasStabilizerActionRow(rows []SettingsRow) bool {
	for _, r := range rows {
		if r.IsStabilizerAction {
			return true
		}
	}
	return false
}

func countStabilizerDefRows(rows []SettingsRow) int {
	n := 0
	for _, r := range rows {
		if r.StabilizerDef != nil {
			n++
		}
	}
	return n
}

func hasStabilizerDefRow(rows []SettingsRow, key string) bool {
	for _, r := range rows {
		if r.StabilizerDef != nil && r.StabilizerDef.Key == key {
			return true
		}
	}
	return false
}

func hasConfigFieldRow(rows []SettingsRow, key string) bool {
	for _, r := range rows {
		if r.StabilizerDef == nil && !r.IsNetworkToggle && !r.IsToggle && !r.IsStabilizerAction && r.Field.Key == key {
			return true
		}
	}
	return false
}

// noopStabilizerValue is the StabilizerValue callback used by tests that
// don't exercise a StabilizerDef row's rendered value itself.
func noopStabilizerValue(d settings.StabilizerSettingDef, state *settings.StabilizerSettingsState) string {
	return ""
}

// TestSettingsModelStabilizerSettingDescriptionShown covers
// SettingsModel.View - confirms both places a
// stabilizer/aileron setting is picked/edited (the bool picker overlay and
// the int/quantity free-text edit prompt) show its summary from
// stabilizer-settings.yaml, so an operator isn't guessing what a setting
// does from its key name alone.
func TestSettingsModelStabilizerSettingDescriptionShown(t *testing.T) {
	boolDef, ok := settings.StabilizerSettingByKey("aileron_ui_enabled")
	if !ok {
		t.Fatal("aileron_ui_enabled not found in settings.StabilizerSettingDefs")
	}
	intDef, ok := settings.StabilizerSettingByKey("build_max_cpu")
	if !ok {
		t.Fatal("build_max_cpu not found in settings.StabilizerSettingDefs")
	}
	cfg := config.Config{Network: network.NetworkConfig{Addressing: "dhcp"}}
	state := &settings.StabilizerSettingsState{}

	t.Run("picker overlay shows the summary", func(t *testing.T) {
		m := SettingsModel{Picking: true, PickOptions: []string{"true", "false"}}
		rows := m.Rows(cfg, true)
		for i, r := range rows {
			if r.StabilizerDef != nil && r.StabilizerDef.Key == "aileron_ui_enabled" {
				m.Cursor = i
				break
			}
		}
		out := m.View(SettingsViewParams{
			Cfg: cfg, Versions: config.VersionCache{StabilizerDetected: true},
			StabilizerSettingsState: state, StabilizerValue: noopStabilizerValue,
			TermWidth: 100, VisibleRows: 30,
		})
		if !strings.Contains(out, boolDef.Summary) {
			t.Errorf("picker overlay doesn't show %q's summary %q:\n%s", boolDef.Key, boolDef.Summary, out)
		}
	})

	t.Run("edit prompt shows the summary", func(t *testing.T) {
		m := SettingsModel{ShowAdvanced: true, Editing: true}
		rows := m.Rows(cfg, true)
		for i, r := range rows {
			if r.StabilizerDef != nil && r.StabilizerDef.Key == "build_max_cpu" {
				m.Cursor = i
				break
			}
		}
		out := m.View(SettingsViewParams{
			Cfg: cfg, Versions: config.VersionCache{StabilizerDetected: true},
			StabilizerSettingsState: state, StabilizerValue: noopStabilizerValue,
			TermWidth: 100, VisibleRows: 30,
		})
		if !strings.Contains(out, intDef.Summary) {
			t.Errorf("edit prompt doesn't show %q's summary %q:\n%s", intDef.Key, intDef.Summary, out)
		}
	})
}
