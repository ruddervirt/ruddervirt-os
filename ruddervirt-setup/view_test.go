// SPDX-License-Identifier: GPL-3.0-only

package main

import "testing"

func TestNetworkSetupLabel(t *testing.T) {
	if got := networkSetupLabel(false); got != "▶ Local physical network setup" {
		t.Errorf("networkSetupLabel(false) = %q", got)
	}
	if got := networkSetupLabel(true); got != "▼ Local physical network setup" {
		t.Errorf("networkSetupLabel(true) = %q", got)
	}
}

func TestAdvancedSettingsLabel(t *testing.T) {
	if got := advancedSettingsLabel(false); got != "▶ Advanced settings" {
		t.Errorf("advancedSettingsLabel(false) = %q", got)
	}
	if got := advancedSettingsLabel(true); got != "▼ Advanced settings" {
		t.Errorf("advancedSettingsLabel(true) = %q", got)
	}
}

func TestClampScroll(t *testing.T) {
	cases := []struct {
		name                    string
		scroll, cursor, visible int
		want                    int
	}{
		{"cursor above window scrolls up to it", 5, 2, 3, 2},
		{"cursor below window scrolls down", 0, 10, 3, 8},
		{"cursor already in window stays put", 2, 3, 5, 2},
		{"never goes negative", 0, 0, 5, 0},
	}
	for _, c := range cases {
		if got := clampScroll(c.scroll, c.cursor, c.visible); got != c.want {
			t.Errorf("%s: clampScroll(%d, %d, %d) = %d, want %d", c.name, c.scroll, c.cursor, c.visible, got, c.want)
		}
	}
}

func TestFitCell(t *testing.T) {
	cases := []struct {
		name  string
		s     string
		width int
		want  string
	}{
		{"pads short string", "ab", 5, "ab   "},
		{"exact width unchanged", "abcde", 5, "abcde"},
		{"truncates long string with ellipsis", "abcdefgh", 5, "abcd…"},
	}
	for _, c := range cases {
		if got := fitCell(c.s, c.width); got != c.want {
			t.Errorf("%s: fitCell(%q, %d) = %q, want %q", c.name, c.s, c.width, got, c.want)
		}
	}
}

func TestSettingsRows(t *testing.T) {
	base := model{cfg: Config{Network: NetworkConfig{Addressing: "dhcp"}}}

	t.Run("collapsed groups show only toggle rows plus plain fields - Apply isn't one of these rows", func(t *testing.T) {
		rows := base.settingsRows()
		if !rows[0].isNetworkToggle {
			t.Fatalf("rows[0].isNetworkToggle = false, want true")
		}
		for _, r := range rows[1:] {
			if r.nested {
				t.Errorf("expected no nested rows while collapsed, got nested row for key %q", r.field.key)
			}
		}
	})

	t.Run("expanding network reveals its fields but not staticOnly ones under dhcp", func(t *testing.T) {
		m := base
		m.settingsShowNetwork = true
		rows := m.settingsRows()
		var nestedKeys []string
		for _, r := range rows {
			if r.nested {
				nestedKeys = append(nestedKeys, r.field.key)
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
		m.cfg.Network.Addressing = "static"
		m.settingsShowNetwork = true
		rows := m.settingsRows()
		count := 0
		for _, r := range rows {
			if r.nested {
				count++
			}
		}
		if count != 6 { // 2 networkSetup fields + 4 staticOnly fields
			t.Errorf("nested row count = %d, want 6", count)
		}
	})

	t.Run("advanced settings only shown when expanded", func(t *testing.T) {
		collapsed := base.settingsRows()
		m := base
		m.settingsShowAdvanced = true
		expanded := m.settingsRows()
		// pod_cidr, svc_cidr, storage.engine, and (not yet detected) the
		// stabilizer action row - all four now live under Advanced.
		if len(expanded) != len(collapsed)+4 {
			t.Errorf("expanded rows = %d, collapsed = %d, want a difference of 4", len(expanded), len(collapsed))
		}
	})

	t.Run("stabilizer action row lives under Advanced, shown until detected", func(t *testing.T) {
		collapsed := base
		collapsed.settingsShowAdvanced = false
		if hasStabilizerActionRow(collapsed.settingsRows()) {
			t.Error("stabilizer action row must not appear while Advanced is collapsed")
		}

		notDetected := base
		notDetected.settingsShowAdvanced = true
		notDetected.cachedStabilizerDetected = false
		if !hasStabilizerActionRow(notDetected.settingsRows()) {
			t.Error("stabilizer action row missing while not yet detected (Advanced expanded)")
		}
		if countStabilizerDefRows(notDetected.settingsRows()) != 0 {
			t.Error("no stabilizer setting rows should appear before adoption")
		}

		detected := base
		detected.settingsShowAdvanced = true
		detected.cachedStabilizerDetected = true
		if hasStabilizerActionRow(detected.settingsRows()) {
			t.Error("stabilizer action row still present after stabilizer was detected")
		}
		// Every stabilizer setting becomes an in-situ row once detected -
		// no separate action row leading anywhere.
		if got, want := countStabilizerDefRows(detected.settingsRows()), len(stabilizerSettingDefs); got != want {
			t.Errorf("stabilizer setting rows = %d, want %d (one per stabilizerSettingDefs entry)", got, want)
		}
	})

	t.Run("aileron_ui_enabled is replaced in place once detected, never duplicated", func(t *testing.T) {
		notDetected := base
		notDetected.settingsShowAdvanced = true
		notDetected.cachedStabilizerDetected = false
		if hasStabilizerDefRow(notDetected.settingsRows(), "aileron_ui_enabled") {
			t.Error("aileron_ui_enabled shouldn't be stabilizer-backed before adoption")
		}
		if !hasConfigFieldRow(notDetected.settingsRows(), "system.aileron_ui_enabled") {
			t.Error("the Config-backed system.aileron_ui_enabled row should still be present before adoption")
		}

		detected := base
		detected.settingsShowAdvanced = true
		detected.cachedStabilizerDetected = true
		rows := detected.settingsRows()
		if hasConfigFieldRow(rows, "system.aileron_ui_enabled") {
			t.Error("the old Config-backed row must be gone once stabilizer is detected - not shown twice")
		}
		var aileronUIRows int
		var aileronUINested bool
		for _, r := range rows {
			if r.stabilizerDef != nil && r.stabilizerDef.Key == "aileron_ui_enabled" {
				aileronUIRows++
				aileronUINested = r.nested
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

func hasStabilizerActionRow(rows []settingsRow) bool {
	for _, r := range rows {
		if r.isStabilizerAction {
			return true
		}
	}
	return false
}

func countStabilizerDefRows(rows []settingsRow) int {
	n := 0
	for _, r := range rows {
		if r.stabilizerDef != nil {
			n++
		}
	}
	return n
}

func hasStabilizerDefRow(rows []settingsRow, key string) bool {
	for _, r := range rows {
		if r.stabilizerDef != nil && r.stabilizerDef.Key == key {
			return true
		}
	}
	return false
}

func hasConfigFieldRow(rows []settingsRow, key string) bool {
	for _, r := range rows {
		if r.stabilizerDef == nil && !r.isNetworkToggle && !r.isToggle && !r.isStabilizerAction && r.field.key == key {
			return true
		}
	}
	return false
}

// TestStabilizerScreensRender is a lightweight smoke test (matches this
// file's existing coverage style) asserting View() doesn't panic for any of
// the "Adopt to ruddervirt.com" wizard's screens at a minimal terminal size.
func TestStabilizerScreensRender(t *testing.T) {
	m := model{cfg: Config{Network: NetworkConfig{Addressing: "dhcp"}}, termWidth: 80, termHeight: 24}
	screens := []screen{
		screenStabilizerAileronCheck, screenStabilizerWarning, screenStabilizerZone,
		screenStabilizerNatsPassword, screenStabilizerNebula, screenStabilizerPlanning,
		screenStabilizerConfirm, screenStabilizerAdopt,
		screenStabilizerSettingsConfirm, screenStabilizerSettingsApply,
		screenStabilizerVersionInput, screenStabilizerVersionConfirm, screenStabilizerVersionApply,
	}
	render := func(s screen) {
		m.current = s
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("View() panicked for screen %d: %v", s, r)
			}
		}()
		_ = m.View()
	}
	for _, s := range screens {
		render(s)
	}

	// screenStabilizerSettingsApply's running/done/failed branches all take
	// separate paths in View().
	def, _ := stabilizerSettingByKey("build_max_cpu")
	m.stabilizerSettingsPendingDef = def
	m.stabilizerSettingsApplyPipeline = stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)
	render(screenStabilizerSettingsApply)

	m.stabilizerSettingsApplyDone = true
	render(screenStabilizerSettingsApply)
	m.stabilizerSettingsApplyDone = false

	m.stabilizerSettingsApplyFailed = true
	render(screenStabilizerSettingsApply)
	m.stabilizerSettingsApplyFailed = false

	// screenStabilizerVersionApply's running/done/failed branches, same as
	// above - and screenStabilizerVersionConfirm with a nil
	// stabilizerSettingsState (must not panic; only Enter-dispatch requires
	// the state to already be loaded, View() must tolerate not having it).
	render(screenStabilizerVersionConfirm)

	m.stabilizerVersionTarget = "1.3.0"
	m.stabilizerVersionClearedPins = []string{"aileron.image.tag"}
	m.stabilizerVersionApplyPipeline = stabilizerVersionApplySteps("kube-system", "stabilizer", []byte(`{}`), "1.3.0")
	render(screenStabilizerVersionApply)

	m.stabilizerVersionApplyDone = true
	render(screenStabilizerVersionApply)
	m.stabilizerVersionApplyDone = false

	m.stabilizerVersionApplyFailed = true
	render(screenStabilizerVersionApply)
}
