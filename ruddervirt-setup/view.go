// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"github.com/charmbracelet/bubbles/textinput"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/stabilizer/settings"
	"ruddervirt-setup/internal/tui/screens"
)

// stabilizerFieldScreenInfo returns the title/label/input/help text for
// whichever of the "Adopt to ruddervirt.com" wizard's plain-text input
// screens m.current currently is (screenStabilizerZone/NatsPassword/
// Nebula) - factored out since those screens share an identical
// single-field layout. Every value here is provided by ruddervirt, not
// operator-chosen, so the help text is just "enter the value provided by
// ruddervirt for X", not a description of what X does.
func stabilizerFieldScreenInfo(m model) (title, label string, input textinput.Model, help string) {
	switch m.current {
	case screenStabilizerZone:
		return "Adopt to ruddervirt.com: Zone Name", "Zone name", m.stabilizerAdopt.ZoneInput,
			"Enter the value provided by ruddervirt for zone name.\n\n"
	case screenStabilizerNatsPassword:
		return "Adopt to ruddervirt.com: NATS Password", "NATS password", m.stabilizerAdopt.NatsPasswordInput,
			"Enter the value provided by ruddervirt for the NATS password.\n\n"
	case screenStabilizerNebula:
		return "Adopt to ruddervirt.com: Nebula Mesh Config", "Path or URL", m.stabilizerAdopt.NebulaInput,
			"Enter the value (a local file path or http(s):// URL) provided by\nruddervirt for the nebula mesh config.\n\n"
	default:
		return "", "", textinput.Model{}, ""
	}
}

// settingsChromeLines is the number of lines the Settings screen spends on
// its header, footer/help text, scroll indicators, table borders/header
// row, and the fixed Apply action bar below the table - i.e. everything
// around the field rows themselves. Subtracted from the terminal height to
// decide how many fields can be shown at once.
const settingsChromeLines = 20

// networkSetupLabel/advancedSettingsLabel/settingsRow/settingsRows/
// settingsScrollCursor moved to internal/tui/screens.NetworkSetupLabel/
// AdvancedSettingsLabel/SettingsRow/SettingsModel.Rows/
// SettingsModel.ScrollCursor.

// updateVersionsRow/updateVersionsRows/updateVersionsScrollCursor moved to
// internal/tui/screens.UpdateRow/UpdateRows/UpdateModel.ScrollCursor.

// settingsVisibleRows returns how many setting fields fit in the current
// terminal height. Falls back to a conservative default before the first
// tea.WindowSizeMsg arrives.
func (m model) settingsVisibleRows() int {
	h := m.termHeight
	if h <= 0 {
		h = 24
	}
	visible := h - settingsChromeLines
	if visible < 1 {
		visible = 1
	}
	return visible
}

// clampScroll adjusts scroll so cursor stays within the visible window.
func clampScroll(scroll, cursor, visible int) int {
	if cursor < scroll {
		scroll = cursor
	}
	if cursor >= scroll+visible {
		scroll = cursor - visible + 1
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll
}

// installChromeLines mirrors settingsChromeLines for the install log screen.
const installChromeLines = 5

// installVisibleLogLines returns how many log lines fit in the current
// terminal height, so the view can tail the log like `tail -f` instead of
// printing lines the terminal can't show.
func (m model) installVisibleLogLines() int {
	h := m.termHeight
	if h <= 0 {
		h = 24
	}
	visible := h - installChromeLines
	if visible < 1 {
		visible = 1
	}
	return visible
}

// menuOptions/menuOrder/resolveInput moved to
// internal/tui/screens.MenuOptions/MenuOrder/ResolveInput.

// saveSettingCmd persists cfg after a settings-field edit - nothing more.
// Settings only checks and records intent; install is the one place that
// actually applies it (see the "Applying settings" install step below).
// Runs as a tea.Cmd since config.SaveConfig shells out via sudo and would
// otherwise block the UI loop.

func (m model) View() string {
	switch m.current {

	case screenInstallPlanning:
		return m.install.ViewPlanning()

	case screenInstallConfirm:
		return m.install.ViewConfirm(installSteps)

	case screenInstall:
		return m.install.ViewInstall(m.installVisibleLogLines())

	case screenResult:
		return m.menu.ViewResult()

	case screenUpdateChecking:
		return m.update.ViewChecking()

	case screenPasswordCheck:
		return m.password.ViewCheck()

	case screenPasswordChange:
		return m.password.ViewChange()

	case screenHostnameChange:
		return m.hostname.View()

	case screenStabilizerAileronCheck:
		return m.stabilizerAdopt.ViewAileronCheck()

	case screenStabilizerWarning:
		return m.stabilizerAdopt.ViewWarning()

	case screenStabilizerZone, screenStabilizerNatsPassword, screenStabilizerNebula:
		title, label, input, help := stabilizerFieldScreenInfo(m)
		return m.stabilizerAdopt.ViewField(title, label, help, input)

	case screenStabilizerPlanning:
		return m.stabilizerAdopt.ViewPlanning()

	case screenStabilizerConfirm:
		return m.stabilizerAdopt.ViewConfirm(m.cfg.Stabilizer.Zone)

	case screenStabilizerAdopt:
		return m.stabilizerAdopt.ViewAdopt(m.installVisibleLogLines())

	case screenStabilizerSettingsConfirm:
		return m.stabilizerSettings.ViewConfirm()

	case screenStabilizerSettingsApply:
		return m.stabilizerSettings.ViewApply(m.installVisibleLogLines())

	case screenStabilizerVersionConfirm:
		current := "(unset)"
		if m.stabilizerSettingsState != nil && m.stabilizerSettingsState.DeclaredVersion != "" {
			current = m.stabilizerSettingsState.DeclaredVersion
		}
		return m.stabilizerVersion.ViewConfirm(current)

	case screenStabilizerVersionApply:
		return m.stabilizerVersion.ViewApply(m.installVisibleLogLines())

	case screenUpdateConfirm:
		return m.update.ViewConfirm()

	case screenUpdate:
		return m.update.ViewRunning(m.installVisibleLogLines())

	case screenOSUpdateConfirm:
		return m.osUpdate.ViewAutoUpdateNotice()

	case screenOSUpdate:
		return m.osUpdate.View(m.installVisibleLogLines())

	case screenPowerOptions:
		return m.power.ViewOptions()

	case screenPowerConfirm:
		return m.power.ViewConfirm()

	case screenPowerApply:
		return m.power.ViewApply(m.installVisibleLogLines())

	case screenUpdateVersions:
		return m.update.ViewVersions(screens.UpdateViewParams{
			Cfg:                     m.cfg,
			Versions:                config.VersionCache{K3s: m.cachedK3sVersions, Aileron: m.cachedAileronVersions, StabilizerDetected: m.cachedStabilizerDetected},
			StabilizerSettingsState: m.stabilizerSettingsState,
			SelfUpdateAvailable:     m.cachedSelfUpdateAvailable,
			OSUpdateAvailable:       m.cachedOSUpdateAvailable,
			Error:                   m.settingsError,
			Saving:                  m.settingsSaving,
			TermWidth:               m.termWidth,
			VisibleRows:             m.settingsVisibleRows(),
		})

	case screenSettings:
		return m.settings.View(screens.SettingsViewParams{
			Cfg:                     m.cfg,
			Versions:                config.VersionCache{K3s: m.cachedK3sVersions, Aileron: m.cachedAileronVersions, StabilizerDetected: m.cachedStabilizerDetected},
			StabilizerSettingsState: m.stabilizerSettingsState,
			StabilizerValue: func(d settings.StabilizerSettingDef, state *settings.StabilizerSettingsState) string {
				value, _ := stabilizerSettingListValue(d, state)
				return value
			},
			Error:       m.settingsError,
			Saving:      m.settingsSaving,
			TermWidth:   m.termWidth,
			VisibleRows: m.settingsVisibleRows(),
		})

	default:
		statusUpdatedAt := m.serviceStatusUpdatedAt
		if m.hostStatsUpdatedAt.After(statusUpdatedAt) {
			statusUpdatedAt = m.hostStatsUpdatedAt
		}
		homeURL := ""
		if config.ConfigSaved() {
			homeURL = aileronUIURL(m.cfg)
		}
		return m.menu.ViewHome(screens.HomeParams{
			TermWidth:          m.termWidth,
			StabilizerDetected: m.cachedStabilizerDetected,
			AileronUIURL:       homeURL,
			ServiceStatuses:    m.serviceStatuses,
			HostStats:          m.hostStats,
			StatusUpdatedAt:    statusUpdatedAt,
			UpdateAvailable: screens.AnyUpdateAvailable(screens.UpdateViewParams{
				Cfg:                     m.cfg,
				Versions:                config.VersionCache{K3s: m.cachedK3sVersions, Aileron: m.cachedAileronVersions, StabilizerDetected: m.cachedStabilizerDetected},
				StabilizerSettingsState: m.stabilizerSettingsState,
				SelfUpdateAvailable:     m.cachedSelfUpdateAvailable,
				OSUpdateAvailable:       m.cachedOSUpdateAvailable,
			}),
		})
	}
}
