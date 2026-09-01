// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"strings"
	"testing"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/network"
	"ruddervirt-setup/internal/stabilizer/settings"
	"ruddervirt-setup/internal/tui/pipeline"
)

// TestNetworkSetupLabel/TestAdvancedSettingsLabel/TestSettingsRows/
// TestStabilizerSettingDescriptionShown moved to
// internal/tui/screens/settings_test.go alongside screens.SettingsModel
// itself.

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

// TestUpdateScreenUpgradeIcon confirms the Update screen marks rows that
// have a newer version available (self-update, OS, and an ordinary
// config.SettingField row like k3s) and doesn't mark rows that don't.
func TestUpdateScreenUpgradeIcon(t *testing.T) {
	m := model{
		cfg:                       config.Config{Versions: config.VersionsConfig{K3s: "v1.30.0", KubeVirt: "v1.2.0", CDI: "v1.58.0", Aileron: "v1.0.0"}},
		termWidth:                 120,
		termHeight:                40,
		current:                   screenUpdateVersions,
		cachedK3sVersions:         []string{"v1.31.0", "v1.30.0"},
		cachedSelfUpdateAvailable: true,
		cachedOSUpdateAvailable:   false,
	}
	out := m.View()
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
		t.Error("ruddervirt-setup row should show the upgrade icon (cachedSelfUpdateAvailable=true)")
	}
	if strings.Contains(findRowLine("Operating system"), "↑") {
		t.Error("Operating system row should NOT show the upgrade icon (cachedOSUpdateAvailable=false)")
	}
	if !strings.Contains(findRowLine("k3s version"), "↑") {
		t.Error("k3s version row should show the upgrade icon (v1.31.0 > v1.30.0 available)")
	}
}

// TestOSUpdateScreenRenders is a smoke test for screenOSUpdate's running/
// done/failed branches, same style as TestStabilizerScreensRender below.
func TestOSUpdateScreenRenders(t *testing.T) {
	m := model{termWidth: 80, termHeight: 24, current: screenOSUpdate}
	render := func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("View() panicked: %v", r)
			}
		}()
		_ = m.View()
	}
	render()
	m.osUpdate.Pipeline.Done = true
	render()
	m.osUpdate.Pipeline.Done = false
	m.osUpdate.Pipeline.Failed = true
	render()
}

// TestStabilizerScreensRender is a lightweight smoke test (matches this
// file's existing coverage style) asserting View() doesn't panic for any of
// the "Adopt to ruddervirt.com" wizard's screens at a minimal terminal size.
func TestStabilizerScreensRender(t *testing.T) {
	m := model{cfg: config.Config{Network: network.NetworkConfig{Addressing: "dhcp"}}, termWidth: 80, termHeight: 24}
	screens := []screen{
		screenStabilizerAileronCheck, screenStabilizerWarning, screenStabilizerZone,
		screenStabilizerNatsPassword, screenStabilizerNebula, screenStabilizerPlanning,
		screenStabilizerConfirm, screenStabilizerAdopt,
		screenStabilizerSettingsConfirm, screenStabilizerSettingsApply,
		screenStabilizerVersionConfirm, screenStabilizerVersionApply,
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
	def, _ := settings.StabilizerSettingByKey("build_max_cpu")
	m.stabilizerSettings.PendingDef = def
	m.stabilizerSettings.Pipeline = pipeline.Model{Steps: stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)}
	render(screenStabilizerSettingsApply)

	m.stabilizerSettings.Pipeline.Done = true
	render(screenStabilizerSettingsApply)
	m.stabilizerSettings.Pipeline.Done = false

	m.stabilizerSettings.Pipeline.Failed = true
	render(screenStabilizerSettingsApply)
	m.stabilizerSettings.Pipeline.Failed = false

	// screenStabilizerVersionApply's running/done/failed branches, same as
	// above - and screenStabilizerVersionConfirm with a nil
	// stabilizerSettingsState (must not panic; only Enter-dispatch requires
	// the state to already be loaded, View() must tolerate not having it).
	render(screenStabilizerVersionConfirm)

	m.stabilizerVersion.Target = "1.3.0"
	m.stabilizerVersion.ClearedPins = []string{"aileron.image.tag"}
	m.stabilizerVersion.Pipeline = pipeline.Model{Steps: stabilizerVersionApplySteps("kube-system", "stabilizer", []byte(`{}`), "1.3.0")}
	render(screenStabilizerVersionApply)

	m.stabilizerVersion.Pipeline.Done = true
	render(screenStabilizerVersionApply)
	m.stabilizerVersion.Pipeline.Done = false

	m.stabilizerVersion.Pipeline.Failed = true
	render(screenStabilizerVersionApply)
}
