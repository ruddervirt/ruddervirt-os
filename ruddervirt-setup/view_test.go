// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"strings"
	"testing"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/network"
	"ruddervirt-setup/internal/stabilizer/settings"
	"ruddervirt-setup/internal/tui/pipeline"
	versionspkg "ruddervirt-setup/internal/versions"
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
	if strings.Contains(findRowLine("OS Packages"), "↑") {
		t.Error("OS Packages row should NOT show the upgrade icon (cachedOSUpdateAvailable=false)")
	}
	if !strings.Contains(findRowLine("k3s"), "↑") {
		t.Error("k3s row should show the upgrade icon (v1.31.0 > v1.30.0 available)")
	}
}

// TestHomeMenuUpdateAvailableIcon confirms the home screen's "update" row
// picks up the ↑ icon from the same caches screenUpdateVersions itself
// renders from (see screens.AnyUpdateAvailable), and doesn't show one when
// nothing's out of date.
func TestHomeMenuUpdateAvailableIcon(t *testing.T) {
	findUpdateLine := func(m model) string {
		lines := strings.Split(m.View(), "\n")
		for _, l := range lines {
			if strings.Contains(l, "update") {
				return l
			}
		}
		t.Fatalf("update row not found in rendered output:\n%s", m.View())
		return ""
	}

	available := model{
		cfg:                       config.Config{Versions: config.VersionsConfig{K3s: "v1.30.0"}},
		termWidth:                 120,
		termHeight:                40,
		cachedK3sVersions:         []string{"v1.31.0", "v1.30.0"},
		cachedSelfUpdateAvailable: false,
		cachedOSUpdateAvailable:   false,
	}
	if !strings.Contains(findUpdateLine(available), "↑") {
		t.Error("update row should show the upgrade icon when k3s has a newer version available")
	}

	// Newest supported release per hand-curated component (not
	// Default*Version - default-versions.yaml can lag behind
	// supported-versions.yaml, which would make that version look
	// upgradable and defeat the point of this "nothing available" case).
	// K3s/Aileron are left unset: their Options come from a fetch cache
	// (empty here), so they report no upgrade regardless of value.
	newest := func(all []string) string {
		sorted := versionspkg.SupportedVersionsAtLeast(all, "")
		if len(sorted) == 0 {
			return ""
		}
		return sorted[0]
	}
	upToDate := model{
		cfg: config.Config{Versions: config.VersionsConfig{
			KubeOVN:  newest(versionspkg.SupportedVersions.KubeOVN),
			Multus:   newest(versionspkg.SupportedVersions.Multus),
			KubeVirt: newest(versionspkg.SupportedVersions.KubeVirt),
			CDI:      newest(versionspkg.SupportedVersions.CDI),
		}},
		termWidth:  120,
		termHeight: 40,
	}
	if strings.Contains(findUpdateLine(upToDate), "↑") {
		t.Error("update row should NOT show the upgrade icon when nothing has an upgrade available")
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

// TestOSUpdateAutoUpdateNoticeRenders is a smoke test for
// screenOSUpdateConfirm - the notice shown before OSUpdateSteps when
// cfg.System.AutoUpdate is already on (see app.go's IsOSUpdate handling).
func TestOSUpdateAutoUpdateNoticeRenders(t *testing.T) {
	m := model{termWidth: 80, termHeight: 24, current: screenOSUpdateConfirm}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("View() panicked: %v", r)
		}
	}()
	_ = m.View()
}

// TestPowerScreensRender is a smoke test for the "power options" submenu's
// three screens (options list, confirm, and the running/done/failed apply
// pipeline), same style as TestOSUpdateScreenRenders.
func TestPowerScreensRender(t *testing.T) {
	m := model{termWidth: 80, termHeight: 24}
	render := func(s screen) {
		m.current = s
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("View() panicked for screen %d: %v", s, r)
			}
		}()
		_ = m.View()
	}

	render(screenPowerOptions)
	m.power.Cursor = 1
	render(screenPowerOptions)

	render(screenPowerConfirm)
	m.power.ConfirmError = `Type "yes" to proceed, or Esc to cancel.`
	render(screenPowerConfirm)
	m.power.ConfirmError = ""

	render(screenPowerApply)
	m.power.Pipeline.Done = true
	render(screenPowerApply)
	m.power.Pipeline.Done = false
	m.power.Pipeline.Failed = true
	render(screenPowerApply)
	m.power.Pipeline = pipeline.Model{}
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
