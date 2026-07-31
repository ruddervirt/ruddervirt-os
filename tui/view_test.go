package main

import "testing"

func TestNetworkSetupLabel(t *testing.T) {
	if got := networkSetupLabel(false); got != "▸ Local physical network setup" {
		t.Errorf("networkSetupLabel(false) = %q", got)
	}
	if got := networkSetupLabel(true); got != "▾ Local physical network setup" {
		t.Errorf("networkSetupLabel(true) = %q", got)
	}
}

func TestAdvancedSettingsLabel(t *testing.T) {
	if got := advancedSettingsLabel(false); got != "▸ Advanced settings" {
		t.Errorf("advancedSettingsLabel(false) = %q", got)
	}
	if got := advancedSettingsLabel(true); got != "▾ Advanced settings" {
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

	t.Run("collapsed groups show only apply/toggle rows plus plain fields", func(t *testing.T) {
		rows := base.settingsRows()
		if !rows[0].isApply {
			t.Fatalf("rows[0].isApply = false, want true")
		}
		if !rows[1].isNetworkToggle {
			t.Fatalf("rows[1].isNetworkToggle = false, want true")
		}
		for _, r := range rows[2:] {
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
		if len(expanded) != len(collapsed)+2 { // pod_cidr, svc_cidr
			t.Errorf("expanded rows = %d, collapsed = %d, want a difference of 2", len(expanded), len(collapsed))
		}
	})
}
