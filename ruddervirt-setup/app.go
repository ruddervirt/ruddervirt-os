// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/hostname"
	"ruddervirt-setup/internal/network"
	"ruddervirt-setup/internal/osupdate"
	"ruddervirt-setup/internal/power"
	"ruddervirt-setup/internal/selfupdate"
	"ruddervirt-setup/internal/stabilizer"
	"ruddervirt-setup/internal/stabilizer/settings"
	"ruddervirt-setup/internal/tui/pipeline"
	"ruddervirt-setup/internal/tui/screens"
	versionspkg "ruddervirt-setup/internal/versions"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		m.settings.Scroll = clampScroll(m.settings.Scroll, m.settings.ScrollCursor(m.cfg, m.cachedStabilizerDetected), m.settingsVisibleRows())
		m.update.Scroll = clampScroll(m.update.Scroll, m.update.ScrollCursor(), m.settingsVisibleRows())
		return m, nil

	case k3sVersionsFetchedMsg:
		m.cachedK3sVersions = msg.versions
		return m, nil

	case aileronVersionsFetchedMsg:
		m.cachedAileronVersions = msg.versions
		return m, nil

	case selfUpdateAvailableMsg:
		m.cachedSelfUpdateAvailable = msg.available
		return m, nil

	case osUpdateAvailableMsg:
		m.cachedOSUpdateAvailable = msg.available
		return m, nil

	case stabilizerDetectedMsg:
		m.cachedStabilizerDetected = msg.present
		if msg.present {
			// Only fetch stabilizer's settings once present is confirmed, so a
			// node that never adopted it never pays for the extra kubectl
			// round trip (or an unwanted sudo prompt) just from opening the menu.
			return m, loadStabilizerSettingsStateCmd()
		}
		return m, nil

	case serviceStatusMsg:
		m.serviceStatuses = msg.statuses
		m.serviceStatusUpdatedAt = time.Now()
		return m, nil

	case hostStatsMsg:
		m.hostStats = msg.stats
		m.prevCPUSample = msg.sample
		m.hostStatsUpdatedAt = time.Now()
		return m, nil

	case serviceStatusTickMsg:
		if m.current == screenMenu {
			return m, tea.Batch(fetchServiceStatusesCmd(m.cfg), fetchHostStatsCmd(m.cfg, m.prevCPUSample), tickServiceStatusCmd())
		}
		return m, tickServiceStatusCmd()

	case serviceStatusRenderTickMsg:
		// No data to apply - just reschedules, so the next View() re-evaluates
		// "updated Xs ago" against the current clock, not the last fetch time.
		return m, tickServiceStatusRenderCmd()

	case stepOutputMsg:
		switch m.current {
		case screenUpdate:
			var cmd tea.Cmd
			m.update.Pipeline, cmd = m.update.Pipeline.Update(msg, m.cfg)
			return m, cmd
		case screenStabilizerAdopt:
			var cmd tea.Cmd
			m.stabilizerAdopt.Pipeline, cmd = m.stabilizerAdopt.Pipeline.Update(msg, m.cfg)
			return m, cmd
		case screenStabilizerSettingsApply:
			var cmd tea.Cmd
			m.stabilizerSettings.Pipeline, cmd = m.stabilizerSettings.Pipeline.Update(msg, m.cfg)
			return m, cmd
		case screenStabilizerVersionApply:
			var cmd tea.Cmd
			m.stabilizerVersion.Pipeline, cmd = m.stabilizerVersion.Pipeline.Update(msg, m.cfg)
			return m, cmd
		case screenOSUpdate:
			var cmd tea.Cmd
			m.osUpdate.Pipeline, cmd = m.osUpdate.Pipeline.Update(msg, m.cfg)
			return m, cmd
		case screenPowerApply:
			var cmd tea.Cmd
			m.power.Pipeline, cmd = m.power.Pipeline.Update(msg, m.cfg)
			return m, cmd
		default:
			// Only the install pipeline is left once every other screen
			// is ruled out - at most one pipeline runs at a time.
			var cmd tea.Cmd
			m.install.Pipeline, cmd = m.install.Pipeline.Update(msg, m.cfg)
			return m, cmd
		}

	case stepDoneMsg:
		if m.current == screenPowerApply {
			// No special-casing on success like the other pipelines here -
			// systemctl reboot/poweroff hands off to systemd and this whole
			// process goes down with the host moments later (see
			// screens.PowerModel.ViewApply); nothing to re-fetch or re-exec
			// into first.
			var cmd tea.Cmd
			m.power.Pipeline, cmd = m.power.Pipeline.Update(msg, m.cfg)
			return m, cmd
		}
		if m.current == screenOSUpdate {
			wasDone := m.osUpdate.Pipeline.Done
			var cmd tea.Cmd
			m.osUpdate.Pipeline, cmd = m.osUpdate.Pipeline.Update(msg, m.cfg)
			if m.osUpdate.Pipeline.Done && !wasDone {
				// Re-check so the row's icon clears once the staged deployment
				// is reflected, instead of showing stale "available" until restart.
				return m, checkOSUpdateAvailableCmd()
			}
			return m, cmd
		}
		if m.current == screenStabilizerVersionApply {
			wasDone := m.stabilizerVersion.Pipeline.Done
			var cmd tea.Cmd
			m.stabilizerVersion.Pipeline, cmd = m.stabilizerVersion.Pipeline.Update(msg, m.cfg)
			if m.stabilizerVersion.Pipeline.Done && !wasDone {
				// Re-fetch for real: the rollout wait step already confirmed
				// stabilizer is Available again, so this picks up
				// CHART_VERSION/spec.version as they actually landed.
				return m, loadStabilizerSettingsStateCmd()
			}
			return m, cmd
		}
		if m.current == screenStabilizerSettingsApply {
			wasDone := m.stabilizerSettings.Pipeline.Done
			var cmd tea.Cmd
			m.stabilizerSettings.Pipeline, cmd = m.stabilizerSettings.Pipeline.Update(msg, m.cfg)
			if m.stabilizerSettings.Pipeline.Done && !wasDone {
				if m.stabilizerSettingsState != nil {
					// Optimistically reflect the applied change locally, so
					// Settings shows "rollout pending -> X" (or the new value
					// once done) immediately, without another kubectl round trip.
					settings.SetByPath(m.stabilizerSettingsState.DeclaredValues, m.stabilizerSettings.PendingDef.Path, m.stabilizerSettings.PendingValue)
				}
				// Re-fetch for real too: the rollout wait step already
				// confirmed Available again, so this replaces the optimistic
				// guess with the actual applied env.
				return m, loadStabilizerSettingsStateCmd()
			}
			return m, cmd
		}
		if m.current == screenStabilizerAdopt {
			wasDone := m.stabilizerAdopt.Pipeline.Done
			wasFailed := m.stabilizerAdopt.Pipeline.Failed
			var cmd tea.Cmd
			m.stabilizerAdopt.Pipeline, cmd = m.stabilizerAdopt.Pipeline.Update(msg, m.cfg)
			if m.stabilizerAdopt.Pipeline.Failed && !wasFailed {
				stabilizer.ClearPendingSecrets()
				return m, nil
			}
			if m.stabilizerAdopt.Pipeline.Done && !wasDone {
				stabilizer.ClearPendingSecrets()
				// Refresh cachedStabilizerDetected (populated once in Init,
				// never re-fired on its own) so Settings' Aileron field lock
				// and this action row's visibility update without a restart.
				return m, detectStabilizerCmd()
			}
			return m, cmd
		}
		if m.current == screenUpdate {
			wasDone := m.update.Pipeline.Done
			var cmd tea.Cmd
			m.update.Pipeline, cmd = m.update.Pipeline.Update(msg, m.cfg)
			if m.update.Pipeline.Done && !wasDone {
				// Quit immediately (like shellMode/k9sMode): main()'s loop
				// checks update.Installed and re-execs into the new binary,
				// which only takes effect once this old process exits.
				m.update.Installed = true
				return m, tea.Quit
			}
			return m, cmd
		}
		// Only the install pipeline is left once every other screen is
		// ruled out - at most one pipeline runs at a time.
		var cmd tea.Cmd
		m.install.Pipeline, cmd = m.install.Pipeline.Update(msg, m.cfg)
		return m, cmd

	case aileronReadyCheckMsg:
		if !msg.ready {
			m.current = screenSettings
			m.settingsError = "aileron isn't installed and running yet - finish Apply first, then try again"
			return m, nil
		}
		m.current = screenStabilizerWarning
		return m, nil

	case stabilizerSettingsLoadedMsg:
		// Fetched silently in the background (once stabilizerDetectedMsg
		// confirms presence, or after a successful apply) - never navigates
		// on its own. An error only matters if Settings is on screen to show
		// it; elsewhere it's dropped and rows keep showing "loading..." until
		// the next successful fetch.
		if msg.err != nil {
			if m.current == screenSettings {
				m.settingsError = msg.err.Error()
			}
			return m, nil
		}
		m.stabilizerSettingsState = msg.state
		return m, nil

	case nebulaConfigResolvedMsg:
		m.stabilizerAdopt.NebulaResolving = false
		if msg.err != nil {
			m.stabilizerAdopt.Error = msg.err.Error()
			return m, nil
		}
		m.stabilizerAdopt.NebulaContent = msg.content
		m.stabilizerAdopt.NebulaInput.Blur()
		m.stabilizerAdopt.Error = ""
		m.current = screenStabilizerPlanning
		return m, computeStabilizerPlanCmd(kubectlBinPath)

	case stabilizerPlanMsg:
		m.stabilizerAdopt.WillAdopt = msg.willAdopt
		m.current = screenStabilizerConfirm
		m.stabilizerAdopt.ConfirmInput.SetValue("")
		m.stabilizerAdopt.ConfirmInput.Focus()
		m.stabilizerAdopt.ConfirmError = ""
		return m, nil

	case updateCheckMsg:
		m.update.Checking = false
		if msg.err != nil {
			m.current = screenResult
			m.menu.Result = fmt.Sprintf("Update check failed: %s", msg.err)
			return m, nil
		}
		m.update.LatestVersion = msg.latestVersion
		if msg.alreadyLatest {
			m.update.AlreadyLatest = true
			m.current = screenResult
			m.menu.Result = fmt.Sprintf("ruddervirt-setup is already up to date (%s).", versionspkg.Version)
			return m, nil
		}
		m.update.BinaryURL = msg.binaryURL
		m.update.ChecksumHex = msg.checksumHex
		m.current = screenUpdateConfirm
		m.update.ConfirmInput.SetValue("")
		m.update.ConfirmInput.Focus()
		m.update.ConfirmError = ""
		return m, nil

	case installPlanMsg:
		m.install.PlanLines = msg.lines
		m.current = screenInstallConfirm
		m.install.ConfirmInput.SetValue("")
		m.install.ConfirmInput.Focus()
		m.install.ConfirmError = ""
		return m, nil

	case settingsSavedMsg:
		m.settingsSaving = false
		if msg.err != nil {
			m.settingsError = msg.err.Error()
		} else {
			m.settingsError = ""
		}
		return m, nil

	case passwordCheckMsg:
		if msg.err != nil {
			m.password.Error = msg.err.Error()
			return m, nil
		}
		if msg.unchanged {
			m.current = screenPasswordChange
			m.password.NewInput.SetValue("")
			m.password.ConfirmInput.SetValue("")
			m.password.ConfirmFocus = false
			m.password.ConfirmInput.Blur()
			m.password.NewInput.Focus()
			m.password.Error = ""
			return m, nil
		}
		// Already changed outside this flow (e.g. upgraded from a version
		// that predates this check) - just record it, no need to force anything.
		m.password.Saving = true
		return m, finalizePasswordChangeCmd(m.cfg)

	case screens.PasswordSetMsg:
		if msg.Err != nil {
			m.password.Saving = false
			m.password.Error = msg.Err.Error()
			return m, nil
		}
		m.password.Saving = true
		return m, finalizePasswordChangeCmd(m.cfg)

	case passwordFinalizedMsg:
		m.password.Saving = false
		if msg.err != nil {
			m.password.Error = msg.err.Error()
			return m, nil
		}
		m.cfg = msg.cfg
		m.password = m.password.ClearInputs()
		m.current = screenSettings
		m.settings = m.settings.Reset()
		m.settingsError = ""
		return m, nil

	case screens.HostnameSetMsg:
		if msg.Err != nil {
			m.hostname.Saving = false
			m.hostname.Error = msg.Err.Error()
			return m, nil
		}
		return m, finalizeHostnameDeclaredCmd(m.cfg)

	case hostnameDeclaredMsg:
		if m.current != screenHostnameChange {
			// Silent background reconciliation from Init() (hostname was
			// already non-default) - just adopt the persisted flag if it saved.
			if msg.err == nil {
				m.cfg = msg.cfg
			}
			return m, nil
		}
		m.hostname.Saving = false
		if msg.err != nil {
			m.hostname.Error = msg.err.Error()
			return m, nil
		}
		m.cfg = msg.cfg
		m.hostname = m.hostname.Reset()
		if m.hostnameChangeForUpdate {
			// Resume the "update" flow this was forced from (see
			// hostnameChangeForUpdate) - same reset as the "update" menu case,
			// no password check.
			m.hostnameChangeForUpdate = false
			m.current = screenUpdateVersions
			m.update = m.update.Reset()
			m.settingsSaving = false
			m.settingsError = ""
			return m, nil
		}
		if !m.cfg.System.PasswordChanged {
			m.current = screenPasswordCheck
			m.password.Error = ""
			return m, checkPasswordChangedCmd()
		}
		m.current = screenSettings
		m.settings = m.settings.Reset()
		m.settingsError = ""
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {

		case tea.KeyCtrlC:
			return m, tea.Quit

		case tea.KeyCtrlS:
			// screenHostnameChange deliberately has no skip: unlike the
			// password, the hostname becomes permanently immutable once
			// install proceeds (hostnameLocked, hostname.go), so it must be
			// declared for real before Settings/Apply, not deferred. Falls
			// through to the generic no-op below.
			if m.current == screenPasswordChange {
				// Skip for now, not permanently - PasswordChanged stays false,
				// so the next "configure" entry asks again. Not bound to Esc
				// (that means cancel/step-back) since skip is its own explicit action.
				m.current = screenSettings
				m.settings = m.settings.Reset()
				m.settingsError = ""
				m.password = m.password.ClearInputs()
				m.password.ConfirmFocus = false
				m.password.Error = ""
				return m, nil
			}
			return m, nil

		case tea.KeyEsc:
			if m.current == screenInstall && !m.install.Pipeline.Done && !m.install.Pipeline.Failed {
				return m, nil
			}
			if m.current == screenUpdateChecking {
				return m, nil
			}
			if m.current == screenPasswordCheck && m.password.Error == "" {
				// Block Esc only while the check is in flight - once it's
				// failed, Esc must work so the operator isn't stuck.
				return m, nil
			}
			if m.current == screenInstallPlanning {
				return m, nil
			}
			if m.current == screenUpdate && !m.update.Pipeline.Done && !m.update.Pipeline.Failed {
				return m, nil
			}
			if m.current == screenStabilizerAdopt && !m.stabilizerAdopt.Pipeline.Done && !m.stabilizerAdopt.Pipeline.Failed {
				return m, nil
			}
			if m.current == screenStabilizerPlanning || m.current == screenStabilizerAileronCheck {
				return m, nil
			}
			if isStabilizerWizardScreen(m.current) {
				// Cancel straight to Settings, clearing every wizard input -
				// these are one-way, single-shot entries (unlike
				// screenPasswordChange's two-field back-navigation), so no
				// intermediate "step back one field" is needed.
				m.current = screenSettings
				m.stabilizerAdopt = m.stabilizerAdopt.ClearInputs()
				return m, nil
			}
			if m.current == screenOSUpdate && !m.osUpdate.Pipeline.Done && !m.osUpdate.Pipeline.Failed {
				return m, nil
			}
			if m.current == screenOSUpdate {
				m.current = screenUpdateVersions
				m.osUpdate.Pipeline = pipeline.Model{}
				return m, nil
			}
			if m.current == screenOSUpdateConfirm {
				m.current = screenUpdateVersions
				return m, nil
			}
			if m.current == screenPowerApply && !m.power.Pipeline.Done && !m.power.Pipeline.Failed {
				return m, nil
			}
			if m.current == screenPowerApply {
				// Only reachable on Failed in practice - a successful reboot/
				// shutdown takes this whole process down with the host before
				// Esc could ever be pressed (see screens.PowerModel.ViewApply).
				// Back to wherever this action was launched from, same as
				// screenPowerConfirm above.
				m.current = m.powerConfirmOrigin
				m.power.Pipeline = pipeline.Model{}
				return m, nil
			}
			if m.current == screenPowerConfirm {
				// Cancel back to wherever this confirm was reached from
				// (normally the power options list, but the OS-update-done
				// "press r to reboot" shortcut sends the operator here
				// directly - see powerConfirmOrigin's doc comment).
				m.current = m.powerConfirmOrigin
				m.power = m.power.ClearConfirm()
				return m, nil
			}
			if m.current == screenPowerOptions {
				m.current = screenMenu
				m.power = m.power.Reset()
				return m, nil
			}
			if m.current == screenStabilizerSettingsApply && !m.stabilizerSettings.Pipeline.Done {
				return m, nil
			}
			if m.current == screenStabilizerSettingsApply {
				// Done (success or failure) - back to Settings, which already
				// reflects the outcome (optimistic update on success,
				// unchanged on failure - see stepDoneMsg above).
				m.current = screenSettings
				m.stabilizerSettings.Pipeline = pipeline.Model{}
				return m, nil
			}
			if m.current == screenStabilizerSettingsConfirm {
				// Cancel back to Settings - it's an ordinary row there (see
				// screens.SettingsModel.Rows), not a separate screen.
				m.current = screenSettings
				m.stabilizerSettings = m.stabilizerSettings.ClearConfirm()
				return m, nil
			}
			if m.current == screenStabilizerVersionApply && !m.stabilizerVersion.Pipeline.Done {
				return m, nil
			}
			if m.current == screenStabilizerVersionApply {
				// Done (success or failure) - back to the Update screen,
				// which reached this flow (see the versions.aileron
				// special-casing above) - not Settings, unlike the in-situ
				// settings flow above.
				m.current = screenUpdateVersions
				m.stabilizerVersion.Pipeline = pipeline.Model{}
				return m, nil
			}
			if m.current == screenStabilizerVersionConfirm {
				// Cancel back to Update - the version came from a picker (see
				// versions.aileron special-casing above), so there's nothing
				// to retype.
				m.current = screenUpdateVersions
				m.stabilizerVersion = m.stabilizerVersion.ClearConfirm()
				return m, nil
			}
			if m.current == screenSettings && m.settings.Picking {
				m.settings.Picking = false
				m.settingsError = ""
				return m, nil
			}
			if m.current == screenSettings && m.settings.Editing {
				m.settings.Editing = false
				m.settingsError = ""
				m.settings.Input.Blur()
				return m, nil
			}
			if m.current == screenUpdateVersions && m.update.Picking {
				m.update.Picking = false
				m.settingsError = ""
				return m, nil
			}
			if m.current == screenPasswordChange && m.password.ConfirmFocus {
				// Step back to the new-password field instead of the generic
				// reset below, so a mismatch caught at confirm can be fixed
				// without abandoning the flow. Confirm value is cleared since
				// it was only validated against the new field at the time.
				m.password.ConfirmInput.SetValue("")
				m.password.ConfirmInput.Blur()
				m.password.NewInput.CursorEnd()
				m.password.NewInput.Focus()
				m.password.ConfirmFocus = false
				m.password.Error = ""
				return m, nil
			}
			if m.current == screenInstallConfirm {
				// Cancel back to wherever Apply was pressed from (Settings or
				// Update - see installConfirmOrigin), not out to the main menu.
				m.current = m.installConfirmOrigin
				m.install = m.install.Reset()
				return m, nil
			}
			if m.current == screenUpdateConfirm {
				// Cancel back into the Update screen, not out to the main
				// menu - same reasoning as screenInstallConfirm above.
				m.current = screenUpdateVersions
				m.update = m.update.ClearConfirm()
				return m, nil
			}
			m.current = screenMenu
			m.menu = m.menu.Reset()
			m.installConfirmOrigin = screenMenu
			m.settings = m.settings.Reset()
			m.settingsSaving = false
			m.settings.Picking = false
			m.settings.PickCursor = 0
			m.settings.PickScroll = 0
			m.settingsError = ""
			m.settings.Input.SetValue("")
			m.settings.Input.Blur()
			m.install = m.install.Reset()
			m.update = m.update.Reset()
			m.password = m.password.Reset()
			m.hostname = m.hostname.Reset()
			m.hostnameChangeForUpdate = false
			m.stabilizerAdopt = m.stabilizerAdopt.Reset()
			stabilizer.ClearPendingSecrets()
			// stabilizerSettingsState is NOT cleared here - it's cached live
			// cluster data for Settings display, same as
			// cachedStabilizerDetected/cachedAileronVersions, not per-flow transient.
			m.stabilizerSettings = m.stabilizerSettings.Reset()
			m.stabilizerVersion = m.stabilizerVersion.Reset()
			m.osUpdate.Pipeline = pipeline.Model{}
			m.power = m.power.Reset()
			return m, tea.Batch(fetchServiceStatusesCmd(m.cfg), fetchHostStatsCmd(m.cfg, m.prevCPUSample))

		case tea.KeyUp:
			// Falls through (no return) when Editing and not Picking, so the
			// arrow key still reaches Input via the forwarding block below.
			if m.current == screenSettings && (m.settings.Picking || !m.settings.Editing) {
				m.settings = m.settings.Up(m.cfg, m.cachedStabilizerDetected, m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenUpdateVersions {
				m.update = m.update.Up(m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenMenu {
				m.menu = m.menu.Up()
				return m, nil
			}
			if m.current == screenPowerOptions {
				m.power = m.power.Up()
				return m, nil
			}

		case tea.KeyDown:
			// Same fall-through note as tea.KeyUp above.
			if m.current == screenSettings && (m.settings.Picking || !m.settings.Editing) {
				m.settings = m.settings.Down(m.cfg, m.cachedStabilizerDetected, m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenUpdateVersions {
				m.update = m.update.Down(m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenMenu {
				m.menu = m.menu.Down()
				return m, nil
			}
			if m.current == screenPowerOptions {
				m.power = m.power.Down()
				return m, nil
			}

		case tea.KeyEnter:
			if m.current == screenMenu {
				// Typed input always wins over the ↑/↓ cursor when non-empty -
				// see showCursor in screens.MenuModel.ViewHome.
				typed := m.menu.Input
				if typed == "" {
					typed = screens.MenuOrder[m.menu.Cursor]
				}
				if label, ok := screens.ResolveInput(typed); ok {
					m.menu.Input = ""
					switch label {
					case "power options":
						m.current = screenPowerOptions
						m.power = m.power.Reset()
						return m, nil
					case "configure":
						if !m.cfg.System.HostnameDeclared && hostname.HostnameIsDefault() && !hostnameLocked() {
							// Not yet declared, still default, and install
							// hasn't locked it (hostnameLocked, hostname.go) -
							// force it before Settings, checked before the
							// password check below (see initialModel's ordering).
							m.current = screenHostnameChange
							m.hostname.Error = ""
							m.hostnameChangeForUpdate = false
							m.hostname.Input.Focus()
							return m, nil
						}
						if m.cfg.System.PasswordChanged {
							m.current = screenSettings
							// Land on the first real field row - Apply lives in
							// its own fixed footer below the table (see
							// SettingsModel.ScrollCursor), not the row list top.
							m.settings = m.settings.Reset()
							m.settingsError = ""
							return m, nil
						}
						// Not yet confirmed changed from the default - verify
						// (and force a change if still default) before Settings.
						m.current = screenPasswordCheck
						m.password.Error = ""
						return m, checkPasswordChangedCmd()
					case "shell":
						m.shellMode = true
						return m, tea.Quit
					case "k9s":
						m.k9sMode = true
						return m, tea.Quit
					case "update":
						if !m.cfg.System.HostnameDeclared && hostname.HostnameIsDefault() && !hostnameLocked() {
							// "update" can also reach the install pipeline
							// (Apply upgrades re-runs installSteps) without
							// visiting "configure" - same force-before-install
							// reasoning as "configure" above, but resumes here
							// instead of Settings once declared (see
							// hostnameChangeForUpdate).
							m.current = screenHostnameChange
							m.hostname.Error = ""
							m.hostnameChangeForUpdate = true
							m.hostname.Input.Focus()
							return m, nil
						}
						// Lands on screenUpdateVersions, not straight into the
						// ruddervirt-setup check: that's just the first row
						// there alongside the component versions moved out of
						// Settings, so every upgrade happens from one place
						// (see screens.UpdateRows).
						m.current = screenUpdateVersions
						m.update = m.update.Reset()
						m.settingsSaving = false
						m.settingsError = ""
						return m, nil
					default:
						m.menu.Result = label
						m.current = screenResult
					}
				} else {
					m.menu.Input = ""
				}
			} else if m.current == screenUpdateConfirm {
				if strings.EqualFold(strings.TrimSpace(m.update.ConfirmInput.Value()), "yes") {
					m.current = screenUpdate
					m.update.ConfirmInput.Blur()
					selfupdate.SetPending(m.update.LatestVersion, m.update.BinaryURL, m.update.ChecksumHex)
					pl, cmd := pipeline.New(selfupdate.UpdateSteps, m.cfg)
					m.update.Pipeline = pl
					return m, cmd
				}
				m.update.ConfirmError = `Type "yes" to proceed, or Esc to cancel.`
			} else if m.current == screenOSUpdateConfirm {
				// Enter proceeds anyway past the auto-update notice (see
				// IsOSUpdate above) - no "yes" gate needed, just launches the
				// same pipeline the no-notice path would have.
				m.current = screenOSUpdate
				pl, cmd := pipeline.New(osupdate.OSUpdateSteps, m.cfg)
				m.osUpdate.Pipeline = pl
				return m, cmd
			} else if m.current == screenPowerOptions {
				switch screens.PowerOrder[m.power.Cursor] {
				case "Disconnect":
					// Same as the old bare "logout" menu entry - just ends this
					// TUI session, nothing to confirm.
					return m, tea.Quit
				case "Shutdown":
					m.power.Action = "shutdown"
					m.powerConfirmOrigin = screenPowerOptions
					m.current = screenPowerConfirm
					m.power.ConfirmInput.Focus()
					return m, nil
				case "Reboot":
					m.power.Action = "reboot"
					m.powerConfirmOrigin = screenPowerOptions
					m.current = screenPowerConfirm
					m.power.ConfirmInput.Focus()
					return m, nil
				}
			} else if m.current == screenPowerConfirm {
				if strings.EqualFold(strings.TrimSpace(m.power.ConfirmInput.Value()), "yes") {
					m.current = screenPowerApply
					m.power.ConfirmInput.Blur()
					steps := power.ShutdownSteps
					if m.power.Action == "reboot" {
						steps = power.RebootSteps
					}
					pl, cmd := pipeline.New(steps, m.cfg)
					m.power.Pipeline = pl
					return m, cmd
				}
				verb := "shut down"
				if m.power.Action == "reboot" {
					verb = "reboot"
				}
				m.power.ConfirmError = fmt.Sprintf(`Type "yes" to %s, or Esc to cancel.`, verb)
			} else if m.current == screenInstallConfirm {
				if strings.EqualFold(strings.TrimSpace(m.install.ConfirmInput.Value()), "yes") {
					m.current = screenInstall
					m.install.ConfirmInput.Blur()
					pl, cmd := pipeline.New(installSteps, m.cfg)
					m.install.Pipeline = pl
					return m, cmd
				}
				m.install.ConfirmError = `Type "yes" to proceed, or Esc to cancel.`
			} else if m.current == screenSettings {
				if m.settings.Picking {
					row := m.settings.Rows(m.cfg, m.cachedStabilizerDetected)[m.settings.Cursor]
					if row.StabilizerDef != nil {
						def := *row.StabilizerDef
						chosen := m.settings.PickOptions[m.settings.PickCursor]
						value, current, err := resolveStabilizerSettingChange(m.stabilizerSettingsState, def, chosen)
						if err != nil {
							m.settingsError = err.Error()
							return m, nil
						}
						m.settings.Picking = false
						if current != nil && settings.StabilizerSettingValuesEqual(def, value, current) {
							m.settingsError = fmt.Sprintf("%s is already %s - no change", def.Key, settings.FormatStabilizerSettingValue(def, value))
							return m, nil
						}
						m.stabilizerSettings.PendingDef = def
						m.stabilizerSettings.PendingValue = value
						m.stabilizerSettings.PendingCurrent = current
						m.current = screenStabilizerSettingsConfirm
						m.stabilizerSettings.ConfirmInput.SetValue("")
						m.stabilizerSettings.ConfirmInput.Focus()
						m.stabilizerSettings.ConfirmError = ""
						m.settingsError = ""
						return m, nil
					}
					field := row.Field
					chosen := m.settings.PickOptions[m.settings.PickCursor]
					if err := field.Set(&m.cfg, chosen); err != nil {
						m.settingsError = err.Error()
						return m, nil
					}
					m.settings.Picking = false
					m.settingsSaving = true
					m.settingsError = ""
					return m, saveSettingCmd(m.cfg)
				}
				rows := m.settings.Rows(m.cfg, m.cachedStabilizerDetected)
				if m.settings.Cursor == len(rows) {
					// Apply - the fixed footer below the table, one cursor
					// position past the last real row (see
					// SettingsModel.ScrollCursor).
					//
					// Defense-in-depth: "configure" already forces
					// screenHostnameChange before Settings is reachable, but
					// guard the install trigger too in case Settings was
					// reached another way - install must never proceed
					// without a declared hostname (hostnameLocked, hostname.go).
					if !m.cfg.System.HostnameDeclared && hostname.HostnameIsDefault() && !hostnameLocked() {
						m.current = screenHostnameChange
						m.hostname.Error = ""
						m.hostnameChangeForUpdate = false
						m.hostname.Input.Focus()
						return m, nil
					}
					if err := network.ResolveNetworkForInstall(&m.cfg.Network); err != nil {
						m.settingsError = err.Error()
						return m, nil
					}
					m.current = screenInstallPlanning
					m.installConfirmOrigin = screenSettings
					m.install.PlanLines = nil
					return m, computeInstallPlanCmd(m.cfg)
				}
				row := rows[m.settings.Cursor]
				if row.IsStabilizerAction {
					// Check aileron is actually installed and running first -
					// adopting against one that's missing/unhealthy would
					// re-stamp ownership of resources that aren't really
					// there. Only then move to the coordination warning (needs
					// selfhosted@ruddervirt.com to provide secrets first).
					m.current = screenStabilizerAileronCheck
					m.settingsError = ""
					m.stabilizerAdopt.Error = ""
					return m, checkAileronReadyCmd(kubectlBinPath)
				}
				if row.StabilizerDef != nil {
					// Live-cluster-backed row, not config.Config-backed -
					// handled separately from field.Get/set below, but reuses
					// the same settings.Picking/Editing/Input/PickOptions
					// fields every config.Config-backed row uses, so it's an
					// ordinary in-situ row. Picking's confirm is handled
					// earlier (the `if m.settings.Picking` branch above always
					// returns); only Editing's confirm needs handling here,
					// since it falls through this same row-dispatch on its
					// second Enter press.
					def := *row.StabilizerDef
					state := m.stabilizerSettingsState
					if m.settings.Editing {
						if state == nil {
							m.settingsError = "stabilizer settings are still loading - try again in a moment"
							return m, nil
						}
						value, current, err := resolveStabilizerSettingChange(state, def, m.settings.Input.Value())
						if err != nil {
							m.settingsError = err.Error()
							return m, nil
						}
						m.settings.Editing = false
						m.settings.Input.Blur()
						if current != nil && settings.StabilizerSettingValuesEqual(def, value, current) {
							m.settingsError = fmt.Sprintf("%s is already %s - no change", def.Key, settings.FormatStabilizerSettingValue(def, value))
							return m, nil
						}
						m.stabilizerSettings.PendingDef = def
						m.stabilizerSettings.PendingValue = value
						m.stabilizerSettings.PendingCurrent = current
						m.current = screenStabilizerSettingsConfirm
						m.stabilizerSettings.ConfirmInput.SetValue("")
						m.stabilizerSettings.ConfirmInput.Focus()
						m.stabilizerSettings.ConfirmError = ""
						m.settingsError = ""
						return m, nil
					}
					if state == nil {
						m.settingsError = "stabilizer settings are still loading - try again in a moment"
						return m, nil
					}
					if state.JobActive {
						m.settingsError = fmt.Sprintf("a stabilizer release operation is already in progress (helm-install-%s job is active) - wait for it to finish and try again", state.HelmChartName)
						return m, nil
					}
					if _, _, editable := stabilizerSettingRowDisplay(def, state); !editable {
						m.settingsError = fmt.Sprintf("%s can't be changed right now", def.Key)
						return m, nil
					}
					m.settingsError = ""
					if def.Type == settings.StabilizerSettingBool {
						m.settings.PickOptions = []string{"true", "false"}
						m.settings.PickCursor = 0
						if appliedRaw, ok := state.AppliedEnv[def.Env]; ok {
							for i, o := range m.settings.PickOptions {
								if strings.EqualFold(o, appliedRaw) {
									m.settings.PickCursor = i
									break
								}
							}
						}
						m.settings.Picking = true
						return m, nil
					}
					m.settings.Input.SetValue(state.AppliedEnv[def.Env])
					m.settings.Input.CursorEnd()
					m.settings.Input.Focus()
					m.settings.Editing = true
					return m, nil
				}
				if row.IsNetworkToggle {
					m.settings.ShowNetwork = !m.settings.ShowNetwork
					return m, nil
				}
				if row.IsToggle {
					// Its position never moves when toggling, since expanding
					// only inserts rows after it - cursor stays put.
					m.settings.ShowAdvanced = !m.settings.ShowAdvanced
					return m, nil
				}
				field := row.Field
				versions := config.VersionCache{K3s: m.cachedK3sVersions, Aileron: m.cachedAileronVersions, StabilizerDetected: m.cachedStabilizerDetected}
				if field.Locked != nil {
					if locked, reason := field.Locked(&m.cfg, versions); locked {
						m.settingsError = reason
						return m, nil
					}
				}
				if field.Options != nil {
					options := field.Options(&m.cfg, versions)
					if len(options) == 0 {
						return m, nil // nothing to pick yet - e.g. still fetching, or none detected
					}
					m.settings.PickOptions = options
					m.settings.PickCursor = 0
					if current := field.Get(&m.cfg); current != "" {
						for i, o := range options {
							if o == current {
								m.settings.PickCursor = i
								break
							}
						}
					}
					m.settings.PickScroll = clampScroll(0, m.settings.PickCursor, m.settingsVisibleRows())
					m.settings.Picking = true
					m.settingsError = ""
					return m, nil
				}
				if m.settings.Editing {
					if err := field.Set(&m.cfg, m.settings.Input.Value()); err != nil {
						m.settingsError = err.Error()
						return m, nil
					}
					m.settings.Editing = false
					m.settingsSaving = true
					m.settingsError = ""
					m.settings.Input.Blur()
					return m, saveSettingCmd(m.cfg)
				}
				m.settings.Input.SetValue(field.Get(&m.cfg))
				m.settings.Input.CursorEnd()
				m.settings.Input.Focus()
				m.settings.Editing = true
				m.settingsError = ""
			} else if m.current == screenUpdateVersions {
				rows := screens.UpdateRows()
				if m.update.Picking {
					field := rows[m.update.Cursor].Field
					chosen := m.update.PickOptions[m.update.PickCursor]
					if field.Key == "versions.aileron" && m.cachedStabilizerDetected {
						if m.stabilizerSettingsState == nil {
							m.settingsError = "still loading stabilizer state - try again in a moment"
							return m, nil
						}
						target := strings.TrimPrefix(chosen, "v")
						patch, cleared, err := stabilizer.PlanStabilizerVersionUpgrade(m.stabilizerSettingsState, target)
						if err != nil {
							m.update.Picking = false
							m.settingsError = err.Error()
							return m, nil
						}
						m.update.Picking = false
						m.stabilizerVersion.Target = target
						m.stabilizerVersion.Patch = patch
						m.stabilizerVersion.ClearedPins = cleared
						m.current = screenStabilizerVersionConfirm
						m.stabilizerVersion.ConfirmInput.SetValue("")
						m.stabilizerVersion.ConfirmInput.Focus()
						m.stabilizerVersion.ConfirmError = ""
						m.settingsError = ""
						return m, nil
					}
					if err := field.Set(&m.cfg, chosen); err != nil {
						m.settingsError = err.Error()
						return m, nil
					}
					m.update.Picking = false
					m.settingsSaving = true
					m.settingsError = ""
					return m, saveSettingCmd(m.cfg)
				}
				if m.update.Cursor == len(rows) {
					// Apply upgrades - the fixed footer below the table, one
					// cursor position past the last real row (see
					// screens.UpdateModel.ScrollCursor). Same install pipeline
					// Settings' Apply uses.
					//
					// Defense-in-depth: same guard as Settings' Apply above -
					// "update" already forces screenHostnameChange first, but
					// this is the actual install trigger, guarded here too.
					if !m.cfg.System.HostnameDeclared && hostname.HostnameIsDefault() && !hostnameLocked() {
						m.current = screenHostnameChange
						m.hostname.Error = ""
						m.hostnameChangeForUpdate = true
						m.hostname.Input.Focus()
						return m, nil
					}
					if err := network.ResolveNetworkForInstall(&m.cfg.Network); err != nil {
						m.settingsError = err.Error()
						return m, nil
					}
					m.current = screenInstallPlanning
					m.installConfirmOrigin = screenUpdateVersions
					m.install.PlanLines = nil
					return m, computeInstallPlanCmd(m.cfg)
				}
				row := rows[m.update.Cursor]
				if row.IsSelfUpdate {
					m.current = screenUpdateChecking
					m.update.Checking = true
					m.update.CheckErr = ""
					return m, checkForUpdateCmd()
				}
				if row.IsOSUpdate {
					// No separate check/confirm step: applies `rpm-ostree
					// upgrade --bypass-driver` directly (osupdate.OSUpdateSteps,
					// os_update.go). Unlike a k3s/kubevirt/etc. version bump
					// this can't move to something unexpected - rpm-ostree
					// only moves to the latest deployment on the configured
					// stream, and doesn't take effect until reboot - so
					// there's nothing meaningful to confirm. Except when
					// automatic OS package updates are already on
					// (cfg.System.AutoUpdate): Zincati's already doing this in
					// the background, so a heads-up notice (screenOSUpdateConfirm)
					// comes first instead - see screens.OSUpdateModel.ViewAutoUpdateNotice.
					if m.cfg.System.AutoUpdate {
						m.current = screenOSUpdateConfirm
						return m, nil
					}
					m.current = screenOSUpdate
					pl, cmd := pipeline.New(osupdate.OSUpdateSteps, m.cfg)
					m.osUpdate.Pipeline = pl
					return m, cmd
				}
				field := row.Field
				versions := config.VersionCache{K3s: m.cachedK3sVersions, Aileron: m.cachedAileronVersions, StabilizerDetected: m.cachedStabilizerDetected}
				if field.Key == "versions.aileron" && versions.StabilizerDetected {
					// Once stabilizer manages Aileron, "Aileron version" means
					// the stabilizer chart's own spec.version (Aileron ships as
					// its subchart, pinned in lockstep with aileron's release
					// tags - see stabilizerVersionPickerOptions). Same
					// fetch-and-pick shape as other component-version rows,
					// just validated through the guarded flow
					// (stabilizer_upgrade.go) instead of a plain field.Set, no
					// longer a hard uneditable lock.
					if m.stabilizerSettingsState == nil {
						m.settingsError = "still loading stabilizer state - try again in a moment"
						return m, nil
					}
					options := stabilizer.StabilizerVersionPickerOptions(m.cachedAileronVersions, m.stabilizerSettingsState.DeclaredVersion)
					if len(options) == 0 {
						m.settingsError = "no eligible releases fetched yet - try again in a moment"
						return m, nil // nothing to pick yet - e.g. still fetching
					}
					m.update.PickOptions = options
					m.update.PickCursor = 0
					m.update.PickScroll = clampScroll(0, m.update.PickCursor, m.settingsVisibleRows())
					m.update.Picking = true
					m.settingsError = ""
					return m, nil
				}
				if field.Locked != nil {
					if locked, reason := field.Locked(&m.cfg, versions); locked {
						m.settingsError = reason
						return m, nil
					}
				}
				// Every updateScreen field is picker-only (all four
				// component-version fields set options), so this always has
				// something to open.
				options := field.Options(&m.cfg, versions)
				if len(options) == 0 {
					return m, nil // nothing to pick yet - e.g. still fetching, or none detected
				}
				m.update.PickOptions = options
				m.update.PickCursor = 0
				if current := field.Get(&m.cfg); current != "" {
					for i, o := range options {
						if o == current {
							m.update.PickCursor = i
							break
						}
					}
				}
				m.update.PickScroll = clampScroll(0, m.update.PickCursor, m.settingsVisibleRows())
				m.update.Picking = true
				m.settingsError = ""
			} else if m.current == screenPasswordChange {
				var cmd tea.Cmd
				m.password, cmd = m.password.Update(msg)
				return m, cmd
			} else if m.current == screenHostnameChange {
				var cmd tea.Cmd
				m.hostname, cmd = m.hostname.Update(msg)
				return m, cmd
			} else if m.current == screenStabilizerWarning {
				m.current = screenStabilizerZone
				m.stabilizerAdopt.ZoneInput.SetValue(m.cfg.Stabilizer.Zone)
				m.stabilizerAdopt.ZoneInput.CursorEnd()
				m.stabilizerAdopt.ZoneInput.Focus()
				m.stabilizerAdopt.Error = ""
				return m, nil
			} else if m.current == screenStabilizerZone {
				zone, err := stabilizer.NonEmptyField("zone name", m.stabilizerAdopt.ZoneInput.Value())
				if err != nil {
					m.stabilizerAdopt.Error = err.Error()
					return m, nil
				}
				m.cfg.Stabilizer.Zone = zone
				m.stabilizerAdopt.ZoneInput.Blur()
				// NATS URL is fixed (defaultStabilizerNatsURL) and the NATS
				// username is always the zone name, so neither gets its own
				// input screen.
				m.current = screenStabilizerNatsPassword
				m.stabilizerAdopt.NatsPasswordInput.SetValue("")
				m.stabilizerAdopt.NatsPasswordInput.Focus()
				m.stabilizerAdopt.Error = ""
				return m, nil
			} else if m.current == screenStabilizerNatsPassword {
				if _, err := stabilizer.NonEmptyField("NATS password", m.stabilizerAdopt.NatsPasswordInput.Value()); err != nil {
					m.stabilizerAdopt.Error = err.Error()
					return m, nil
				}
				m.stabilizerAdopt.NatsPasswordInput.Blur()
				m.current = screenStabilizerNebula
				m.stabilizerAdopt.NebulaInput.SetValue("")
				m.stabilizerAdopt.NebulaInput.Focus()
				m.stabilizerAdopt.Error = ""
				return m, nil
			} else if m.current == screenStabilizerNebula {
				pathOrURL, err := stabilizer.NonEmptyField("nebula config path/URL", m.stabilizerAdopt.NebulaInput.Value())
				if err != nil {
					m.stabilizerAdopt.Error = err.Error()
					return m, nil
				}
				m.stabilizerAdopt.NebulaResolving = true
				m.stabilizerAdopt.Error = ""
				return m, resolveNebulaConfigCmd(pathOrURL)
			} else if m.current == screenStabilizerConfirm {
				if strings.EqualFold(strings.TrimSpace(m.stabilizerAdopt.ConfirmInput.Value()), "yes") {
					// NATS URL/username are fixed - see screenStabilizerZone above.
					m.cfg.Stabilizer.NatsURL = stabilizer.DefaultStabilizerNatsURL
					natsUser := m.cfg.Stabilizer.Zone
					natsPassword := m.stabilizerAdopt.NatsPasswordInput.Value()
					stabilizer.SetPendingSecrets(natsUser, natsPassword, m.stabilizerAdopt.NebulaContent)
					m.cfg.Stabilizer.Version = versionspkg.DefaultStabilizerVersion
					m.current = screenStabilizerAdopt
					m.stabilizerAdopt.ConfirmInput.Blur()
					m.stabilizerAdopt.NatsPasswordInput.SetValue("")
					m.stabilizerAdopt.NatsPasswordInput.Blur()
					m.stabilizerAdopt.NebulaInput.SetValue("")
					m.stabilizerAdopt.NebulaInput.Blur()
					m.stabilizerAdopt.NebulaContent = ""
					pl, cmd := pipeline.New(stabilizer.AdoptSteps, m.cfg)
					m.stabilizerAdopt.Pipeline = pl
					return m, tea.Batch(saveSettingCmd(m.cfg), cmd)
				}
				m.stabilizerAdopt.ConfirmError = `Type "yes" to proceed, or Esc to cancel.`
			} else if m.current == screenStabilizerSettingsConfirm {
				if strings.EqualFold(strings.TrimSpace(m.stabilizerSettings.ConfirmInput.Value()), "yes") {
					if m.stabilizerSettingsState == nil {
						return m, nil
					}
					m.stabilizerSettings.ConfirmInput.Blur()
					steps := stabilizerSettingsApplySteps(
						m.stabilizerSettingsState.HelmChartNamespace, m.stabilizerSettingsState.HelmChartName,
						m.stabilizerSettings.PendingDef, m.stabilizerSettings.PendingValue)
					pl, cmd := pipeline.New(steps, m.cfg)
					m.stabilizerSettings.Pipeline = pl
					m.current = screenStabilizerSettingsApply
					return m, cmd
				}
				m.stabilizerSettings.ConfirmError = `Type "yes" to proceed, or Esc to cancel.`
			} else if m.current == screenStabilizerVersionConfirm {
				if strings.EqualFold(strings.TrimSpace(m.stabilizerVersion.ConfirmInput.Value()), "yes") {
					if m.stabilizerSettingsState == nil {
						return m, nil
					}
					m.stabilizerVersion.ConfirmInput.Blur()
					steps := stabilizerVersionApplySteps(
						m.stabilizerSettingsState.HelmChartNamespace, m.stabilizerSettingsState.HelmChartName,
						m.stabilizerVersion.Patch, m.stabilizerVersion.Target)
					pl, cmd := pipeline.New(steps, m.cfg)
					m.stabilizerVersion.Pipeline = pl
					m.current = screenStabilizerVersionApply
					return m, cmd
				}
				m.stabilizerVersion.ConfirmError = `Type "yes" to proceed, or Esc to cancel.`
			}
			return m, nil

		case tea.KeyBackspace:
			if m.current == screenMenu && len(m.menu.Input) > 0 {
				m.menu = m.menu.Backspace()
				return m, nil
			}

		case tea.KeyRunes:
			// "press r to reboot" shortcut off a successful OS Packages
			// update (see screens.OSUpdateModel.View's done copy) - reuses
			// the power-options reboot confirm+apply flow instead of a
			// separate one, so there's exactly one reboot implementation
			// (see powerConfirmOrigin's doc comment for the Esc-back
			// wrinkle that comes with reusing it from here).
			if m.current == screenOSUpdate && m.osUpdate.Pipeline.Done && strings.EqualFold(msg.String(), "r") {
				m.power.Action = "reboot"
				m.powerConfirmOrigin = screenOSUpdate
				m.current = screenPowerConfirm
				m.power.ConfirmInput.Focus()
				return m, nil
			}
		}
	}

	if m.current == screenSettings && m.settings.Editing {
		var cmd tea.Cmd
		m.settings, cmd = m.settings.Update(msg)
		return m, cmd
	}

	if m.current == screenInstallConfirm {
		var cmd tea.Cmd
		m.install, cmd = m.install.Update(msg)
		return m, cmd
	}

	if m.current == screenUpdateConfirm {
		var cmd tea.Cmd
		m.update, cmd = m.update.Update(msg)
		return m, cmd
	}

	if m.current == screenPasswordChange {
		var cmd tea.Cmd
		m.password, cmd = m.password.Update(msg)
		return m, cmd
	}

	if m.current == screenHostnameChange {
		var cmd tea.Cmd
		m.hostname, cmd = m.hostname.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerZone {
		var cmd tea.Cmd
		m.stabilizerAdopt.ZoneInput, cmd = m.stabilizerAdopt.ZoneInput.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerNatsPassword {
		var cmd tea.Cmd
		m.stabilizerAdopt.NatsPasswordInput, cmd = m.stabilizerAdopt.NatsPasswordInput.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerNebula {
		var cmd tea.Cmd
		m.stabilizerAdopt.NebulaInput, cmd = m.stabilizerAdopt.NebulaInput.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerConfirm {
		var cmd tea.Cmd
		m.stabilizerAdopt.ConfirmInput, cmd = m.stabilizerAdopt.ConfirmInput.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerSettingsConfirm {
		var cmd tea.Cmd
		m.stabilizerSettings.ConfirmInput, cmd = m.stabilizerSettings.ConfirmInput.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerVersionConfirm {
		var cmd tea.Cmd
		m.stabilizerVersion.ConfirmInput, cmd = m.stabilizerVersion.ConfirmInput.Update(msg)
		return m, cmd
	}

	if m.current == screenPowerConfirm {
		var cmd tea.Cmd
		m.power.ConfirmInput, cmd = m.power.ConfirmInput.Update(msg)
		return m, cmd
	}

	if m.current == screenMenu {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			if keyMsg.Type == tea.KeyRunes {
				m.menu = m.menu.TypeRune(keyMsg.String())
			}
		}
	}

	return m, nil
}
