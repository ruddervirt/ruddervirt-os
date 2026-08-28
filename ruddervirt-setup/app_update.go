// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		m.settingsScroll = clampScroll(m.settingsScroll, m.settingsScrollCursor(), m.settingsVisibleRows())
		m.updateVersionsScroll = clampScroll(m.updateVersionsScroll, m.updateVersionsScrollCursor(), m.settingsVisibleRows())
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
			// Only fetch stabilizer's own settings state once we actually
			// know stabilizer is present - never unconditionally on every
			// launch, so a node that never adopted stabilizer never pays
			// for the extra kubectl round trip (or an unwanted sudo
			// prompt) just from opening the menu.
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
		// Carries no data to apply - just reschedules, so the next View()
		// call (which bubbletea triggers for every processed message,
		// this one included) re-evaluates "updated Xs ago" against the
		// current clock instead of whatever it was at the last actual
		// fetch.
		return m, tickServiceStatusRenderCmd()

	case stepOutputMsg:
		switch m.current {
		case screenUpdate:
			m.updateLogs = append(m.updateLogs, string(msg))
			return m, readFromCh(m.updateCh)
		case screenStabilizerAdopt:
			m.stabilizerLogs = append(m.stabilizerLogs, string(msg))
			return m, readFromCh(m.stabilizerCh)
		case screenStabilizerSettingsApply:
			m.stabilizerSettingsApplyLogs = append(m.stabilizerSettingsApplyLogs, string(msg))
			return m, readFromCh(m.stabilizerSettingsApplyCh)
		case screenStabilizerVersionApply:
			m.stabilizerVersionApplyLogs = append(m.stabilizerVersionApplyLogs, string(msg))
			return m, readFromCh(m.stabilizerVersionApplyCh)
		case screenOSUpdate:
			m.osUpdateLogs = append(m.osUpdateLogs, string(msg))
			return m, readFromCh(m.osUpdateCh)
		default:
			m.installLogs = append(m.installLogs, string(msg))
			return m, readFromCh(m.installCh)
		}

	case stepDoneMsg:
		if m.current == screenOSUpdate {
			if msg.err != nil {
				m.osUpdateLogs = append(m.osUpdateLogs, fmt.Sprintf("✗ %s: %s", msg.label, msg.err.Error()))
				m.osUpdateFailed = true
				return m, nil
			}
			m.osUpdateLogs = append(m.osUpdateLogs, fmt.Sprintf("✓ %s", msg.label))
			m.osUpdateStepIdx++
			if m.osUpdateStepIdx >= len(osUpdateSteps) {
				m.osUpdateDone = true
				// Re-check in the background so the row's icon clears once
				// the newly staged deployment is actually reflected, rather
				// than showing stale "update available" until the process
				// restarts.
				return m, checkOSUpdateAvailableCmd()
			}
			ch, cmd := launchStep(osUpdateSteps[m.osUpdateStepIdx], m.cfg)
			m.osUpdateCh = ch
			return m, cmd
		}
		if m.current == screenStabilizerVersionApply {
			if msg.err != nil {
				m.stabilizerVersionApplyLogs = append(m.stabilizerVersionApplyLogs, fmt.Sprintf("✗ %s: %s", msg.label, msg.err.Error()))
				m.stabilizerVersionApplyFailed = true
				return m, nil
			}
			m.stabilizerVersionApplyLogs = append(m.stabilizerVersionApplyLogs, fmt.Sprintf("✓ %s", msg.label))
			m.stabilizerVersionApplyStepIdx++
			if m.stabilizerVersionApplyStepIdx >= len(m.stabilizerVersionApplyPipeline) {
				m.stabilizerVersionApplyDone = true
				// Re-fetch for real, off the UI thread - the rollout wait
				// step already confirmed stabilizer is Available again, so
				// this picks up CHART_VERSION/spec.version as they actually
				// landed rather than leaving a guess as the last word.
				return m, loadStabilizerSettingsStateCmd()
			}
			ch, cmd := launchStep(m.stabilizerVersionApplyPipeline[m.stabilizerVersionApplyStepIdx], m.cfg)
			m.stabilizerVersionApplyCh = ch
			return m, cmd
		}
		if m.current == screenStabilizerSettingsApply {
			if msg.err != nil {
				m.stabilizerSettingsApplyLogs = append(m.stabilizerSettingsApplyLogs, fmt.Sprintf("✗ %s: %s", msg.label, msg.err.Error()))
				m.stabilizerSettingsApplyFailed = true
				return m, nil
			}
			m.stabilizerSettingsApplyLogs = append(m.stabilizerSettingsApplyLogs, fmt.Sprintf("✓ %s", msg.label))
			m.stabilizerSettingsApplyStepIdx++
			if m.stabilizerSettingsApplyStepIdx >= len(m.stabilizerSettingsApplyPipeline) {
				m.stabilizerSettingsApplyDone = true
				if m.stabilizerSettingsState != nil {
					// Optimistically reflect the just-applied change
					// locally, so Settings shows "rollout pending -> X"
					// (or, once the rollout's done, the new value outright)
					// immediately instead of the stale previous state,
					// without needing another kubectl round trip.
					setByPath(m.stabilizerSettingsState.declaredValues, m.stabilizerSettingsPendingDef.Path, m.stabilizerSettingsPendingValue)
				}
				// Re-fetch for real too, off the UI thread - the rollout
				// wait step already confirmed stabilizer is Available
				// again, so this picks up its actual current applied env
				// rather than leaving the optimistic guess as the last
				// word.
				return m, loadStabilizerSettingsStateCmd()
			}
			ch, cmd := launchStep(m.stabilizerSettingsApplyPipeline[m.stabilizerSettingsApplyStepIdx], m.cfg)
			m.stabilizerSettingsApplyCh = ch
			return m, cmd
		}
		if m.current == screenStabilizerAdopt {
			if msg.err != nil {
				m.stabilizerLogs = append(m.stabilizerLogs, fmt.Sprintf("✗ %s: %s", msg.label, msg.err.Error()))
				m.stabilizerFailed = true
				clearPendingStabilizerSecrets()
				return m, nil
			}
			m.stabilizerLogs = append(m.stabilizerLogs, fmt.Sprintf("✓ %s", msg.label))
			m.stabilizerStepIdx++
			if m.stabilizerStepIdx >= len(stabilizerSteps) {
				m.stabilizerDone = true
				clearPendingStabilizerSecrets()
				// Refresh the otherwise-stale cachedStabilizerDetected
				// (populated once in Init, never re-fired on its own -
				// see its doc comment) immediately, so the Settings
				// screen's Aileron field locks and this action row's own
				// visibility update without needing a restart.
				return m, detectStabilizerCmd()
			}
			ch, cmd := launchStep(stabilizerSteps[m.stabilizerStepIdx], m.cfg)
			m.stabilizerCh = ch
			return m, cmd
		}
		if m.current == screenUpdate {
			if msg.err != nil {
				m.updateLogs = append(m.updateLogs, fmt.Sprintf("✗ %s: %s", msg.label, msg.err.Error()))
				m.updateFailed = true
				return m, nil
			}
			m.updateLogs = append(m.updateLogs, fmt.Sprintf("✓ %s", msg.label))
			m.updateStepIdx++
			if m.updateStepIdx >= len(updateSteps) {
				// Quit immediately (like shellMode/k9sMode) rather than
				// looping back into this screen - main()'s loop checks
				// updateInstalled and re-execs into the freshly-installed
				// binary, which only takes effect once this old process
				// exits.
				m.updateDone = true
				m.updateInstalled = true
				return m, tea.Quit
			}
			ch, cmd := launchStep(updateSteps[m.updateStepIdx], m.cfg)
			m.updateCh = ch
			return m, cmd
		}
		if msg.err != nil {
			m.installLogs = append(m.installLogs, fmt.Sprintf("✗ %s: %s", msg.label, msg.err.Error()))
			m.installFailed = true
			return m, nil
		}
		m.installLogs = append(m.installLogs, fmt.Sprintf("✓ %s", msg.label))
		m.installStepIdx++
		if m.installStepIdx >= len(installSteps) {
			m.installDone = true
			return m, nil
		}
		ch, cmd := launchStep(installSteps[m.installStepIdx], m.cfg)
		m.installCh = ch
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
		// Fetched silently in the background (fired once
		// stabilizerDetectedMsg confirms stabilizer is present, or after a
		// successful apply - see below) - never navigates on its own. An
		// error only matters if the operator is actually looking at
		// Settings right now to see it; on any other screen it's dropped
		// and the rows just keep showing "loading..." until the next
		// successful fetch.
		if msg.err != nil {
			if m.current == screenSettings {
				m.settingsError = msg.err.Error()
			}
			return m, nil
		}
		m.stabilizerSettingsState = msg.state
		return m, nil

	case nebulaConfigResolvedMsg:
		m.stabilizerNebulaResolving = false
		if msg.err != nil {
			m.stabilizerError = msg.err.Error()
			return m, nil
		}
		m.stabilizerNebulaContent = msg.content
		m.stabilizerNebulaInput.Blur()
		m.stabilizerError = ""
		m.current = screenStabilizerPlanning
		return m, computeStabilizerPlanCmd(kubectlBinPath)

	case stabilizerPlanMsg:
		m.stabilizerWillAdopt = msg.willAdopt
		m.current = screenStabilizerConfirm
		m.stabilizerConfirmInput.SetValue("")
		m.stabilizerConfirmInput.Focus()
		m.stabilizerConfirmError = ""
		return m, nil

	case updateCheckMsg:
		m.updateChecking = false
		if msg.err != nil {
			m.current = screenResult
			m.result = fmt.Sprintf("Update check failed: %s", msg.err)
			return m, nil
		}
		m.updateLatestVersion = msg.latestVersion
		if msg.alreadyLatest {
			m.updateAlreadyLatest = true
			m.current = screenResult
			m.result = fmt.Sprintf("ruddervirt-setup is already up to date (%s).", version)
			return m, nil
		}
		m.updateBinaryURL = msg.binaryURL
		m.updateChecksumHex = msg.checksumHex
		m.current = screenUpdateConfirm
		m.updateConfirmInput.SetValue("")
		m.updateConfirmInput.Focus()
		m.updateConfirmError = ""
		return m, nil

	case installPlanMsg:
		m.installPlanLines = msg.lines
		m.current = screenInstallConfirm
		m.installConfirmInput.SetValue("")
		m.installConfirmInput.Focus()
		m.installConfirmError = ""
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
			m.passwordError = msg.err.Error()
			return m, nil
		}
		if msg.unchanged {
			m.current = screenPasswordChange
			m.passwordNewInput.SetValue("")
			m.passwordConfirmInput.SetValue("")
			m.passwordConfirmFocus = false
			m.passwordConfirmInput.Blur()
			m.passwordNewInput.Focus()
			m.passwordError = ""
			return m, nil
		}
		// Already changed outside this flow (e.g. upgraded from a version
		// that predates this check) - just record it and move on, no need
		// to force anything.
		m.passwordSaving = true
		return m, finalizePasswordChangeCmd(m.cfg)

	case passwordSetMsg:
		if msg.err != nil {
			m.passwordSaving = false
			m.passwordError = msg.err.Error()
			return m, nil
		}
		m.passwordSaving = true
		return m, finalizePasswordChangeCmd(m.cfg)

	case passwordFinalizedMsg:
		m.passwordSaving = false
		if msg.err != nil {
			m.passwordError = msg.err.Error()
			return m, nil
		}
		m.cfg = msg.cfg
		m.passwordNewInput.SetValue("")
		m.passwordConfirmInput.SetValue("")
		m.passwordNewInput.Blur()
		m.passwordConfirmInput.Blur()
		m.current = screenSettings
		m.settingsCursor = 0
		m.settingsScroll = 0
		m.settingsEditing = false
		m.settingsShowAdvanced = false
		m.settingsShowNetwork = false
		m.settingsError = ""
		return m, nil

	case hostnameSetMsg:
		if msg.err != nil {
			m.hostnameSaving = false
			m.hostnameError = msg.err.Error()
			return m, nil
		}
		return m, finalizeHostnameDeclaredCmd(m.cfg)

	case hostnameDeclaredMsg:
		if m.current != screenHostnameChange {
			// Silent background reconciliation fired from Init() (the
			// hostname was already non-default) - nothing on screen to
			// update, just adopt the persisted flag if it saved.
			if msg.err == nil {
				m.cfg = msg.cfg
			}
			return m, nil
		}
		m.hostnameSaving = false
		if msg.err != nil {
			m.hostnameError = msg.err.Error()
			return m, nil
		}
		m.cfg = msg.cfg
		m.hostnameInput.SetValue("")
		m.hostnameInput.Blur()
		m.hostnameError = ""
		if m.hostnameChangeForUpdate {
			// Resume the "update" flow this was forced from (see
			// hostnameChangeForUpdate's doc comment) - same reset as the
			// "update" menu case itself, no password check, matching that
			// path's existing behavior.
			m.hostnameChangeForUpdate = false
			m.current = screenUpdateVersions
			m.updateVersionsCursor = 0
			m.updateVersionsScroll = 0
			m.updateVersionsPicking = false
			m.settingsSaving = false
			m.settingsError = ""
			return m, nil
		}
		if !m.cfg.System.PasswordChanged {
			m.current = screenPasswordCheck
			m.passwordError = ""
			return m, checkPasswordChangedCmd()
		}
		m.current = screenSettings
		m.settingsCursor = 0
		m.settingsScroll = 0
		m.settingsEditing = false
		m.settingsShowAdvanced = false
		m.settingsShowNetwork = false
		m.settingsError = ""
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {

		case tea.KeyCtrlC:
			return m, tea.Quit

		case tea.KeyCtrlS:
			// screenHostnameChange deliberately has no skip: unlike the
			// password (which can be changed again anytime from within
			// Settings' first-boot flow), the hostname becomes permanently
			// immutable the moment install proceeds (see hostnameLocked,
			// hostname.go) - so the operator must declare one for real
			// before ever reaching Settings/Apply, not defer it and risk
			// installing under the default. Falls through to the generic
			// no-op below.
			if m.current == screenPasswordChange {
				// Skip for now, not permanently - cfg.System.PasswordChanged
				// stays false, so the next "configure" entry re-runs
				// checkPasswordChangedCmd and asks again. Deliberately not
				// bound to Esc: that already means "cancel this field/step
				// back", and skip needs to be its own explicit action, not
				// something a password field's existing cancel behavior
				// accidentally triggers.
				m.current = screenSettings
				m.settingsCursor = 0
				m.settingsScroll = 0
				m.settingsEditing = false
				m.settingsShowAdvanced = false
				m.settingsShowNetwork = false
				m.settingsError = ""
				m.passwordNewInput.SetValue("")
				m.passwordNewInput.Blur()
				m.passwordConfirmInput.SetValue("")
				m.passwordConfirmInput.Blur()
				m.passwordConfirmFocus = false
				m.passwordError = ""
				return m, nil
			}
			return m, nil

		case tea.KeyEsc:
			if m.current == screenInstall && !m.installDone && !m.installFailed {
				return m, nil
			}
			if m.current == screenUpdateChecking {
				return m, nil
			}
			if m.current == screenPasswordCheck && m.passwordError == "" {
				// Block Esc only while the check is actually in flight -
				// once it's failed, Esc must still work so the operator
				// isn't stuck (falls through to the generic reset below).
				return m, nil
			}
			if m.current == screenInstallPlanning {
				return m, nil
			}
			if m.current == screenUpdate && !m.updateDone && !m.updateFailed {
				return m, nil
			}
			if m.current == screenStabilizerAdopt && !m.stabilizerDone && !m.stabilizerFailed {
				return m, nil
			}
			if m.current == screenStabilizerPlanning || m.current == screenStabilizerAileronCheck {
				return m, nil
			}
			if isStabilizerWizardScreen(m.current) {
				// Cancel straight back to Settings, clearing every wizard
				// input - these are one-way, single-shot entries (unlike
				// screenPasswordChange's two-field back-navigation), so
				// there's no intermediate "step back one field" to support.
				m.current = screenSettings
				m.stabilizerZoneInput.SetValue("")
				m.stabilizerZoneInput.Blur()
				m.stabilizerNatsPasswordInput.SetValue("")
				m.stabilizerNatsPasswordInput.Blur()
				m.stabilizerNebulaInput.SetValue("")
				m.stabilizerNebulaInput.Blur()
				m.stabilizerNebulaResolving = false
				m.stabilizerNebulaContent = ""
				m.stabilizerConfirmInput.SetValue("")
				m.stabilizerConfirmInput.Blur()
				m.stabilizerConfirmError = ""
				m.stabilizerError = ""
				return m, nil
			}
			if m.current == screenOSUpdate && !m.osUpdateDone && !m.osUpdateFailed {
				return m, nil
			}
			if m.current == screenOSUpdate {
				m.current = screenUpdateVersions
				m.osUpdateDone = false
				m.osUpdateFailed = false
				m.osUpdateLogs = nil
				m.osUpdateStepIdx = 0
				m.osUpdateCh = nil
				return m, nil
			}
			if m.current == screenStabilizerSettingsApply && !m.stabilizerSettingsApplyDone {
				return m, nil
			}
			if m.current == screenStabilizerSettingsApply {
				// Done (success or failure) - straight back to Settings,
				// which already reflects the outcome (optimistic update on
				// success, unchanged on failure - see the stepDoneMsg
				// handler above).
				m.current = screenSettings
				m.stabilizerSettingsApplyDone = false
				m.stabilizerSettingsApplyFailed = false
				m.stabilizerSettingsApplyLogs = nil
				m.stabilizerSettingsApplyPipeline = nil
				m.stabilizerSettingsApplyStepIdx = 0
				m.stabilizerSettingsApplyCh = nil
				return m, nil
			}
			if m.current == screenStabilizerSettingsConfirm {
				// Cancel back to Settings - it's an ordinary row there
				// (see settingsRows), not a separate screen to return to.
				m.current = screenSettings
				m.stabilizerSettingsConfirmInput.SetValue("")
				m.stabilizerSettingsConfirmInput.Blur()
				m.stabilizerSettingsConfirmError = ""
				return m, nil
			}
			if m.current == screenStabilizerVersionApply && !m.stabilizerVersionApplyDone {
				return m, nil
			}
			if m.current == screenStabilizerVersionApply {
				// Done (success or failure) - back to the Update screen,
				// which reached this flow in the first place (see the
				// versions.aileron special-casing above) - not Settings,
				// unlike the in-situ settings flow above.
				m.current = screenUpdateVersions
				m.stabilizerVersionApplyDone = false
				m.stabilizerVersionApplyFailed = false
				m.stabilizerVersionApplyLogs = nil
				m.stabilizerVersionApplyPipeline = nil
				m.stabilizerVersionApplyStepIdx = 0
				m.stabilizerVersionApplyCh = nil
				return m, nil
			}
			if m.current == screenStabilizerVersionConfirm {
				// Cancel back to Update, not a free-text input screen to
				// step back to - the version came from a picker (see the
				// versions.aileron special-casing above), so there's
				// nothing to retype.
				m.current = screenUpdateVersions
				m.stabilizerVersionConfirmInput.SetValue("")
				m.stabilizerVersionConfirmInput.Blur()
				m.stabilizerVersionConfirmError = ""
				return m, nil
			}
			if m.current == screenSettings && m.settingsPicking {
				m.settingsPicking = false
				m.settingsError = ""
				return m, nil
			}
			if m.current == screenSettings && m.settingsEditing {
				m.settingsEditing = false
				m.settingsError = ""
				m.settingsInput.Blur()
				return m, nil
			}
			if m.current == screenUpdateVersions && m.updateVersionsPicking {
				m.updateVersionsPicking = false
				m.settingsError = ""
				return m, nil
			}
			if m.current == screenPasswordChange && m.passwordConfirmFocus {
				// Step back to the new-password field instead of falling
				// through to the generic reset below - otherwise a typo
				// caught only at the confirm step (e.g. it not matching)
				// had no way back to fix the field that's actually wrong,
				// short of abandoning the whole flow. The confirm value is
				// cleared since it was only ever validated against
				// whatever the new field held at the time.
				m.passwordConfirmInput.SetValue("")
				m.passwordConfirmInput.Blur()
				m.passwordNewInput.CursorEnd()
				m.passwordNewInput.Focus()
				m.passwordConfirmFocus = false
				m.passwordError = ""
				return m, nil
			}
			if m.current == screenInstallConfirm {
				// Cancel back to wherever Apply was pressed from (Settings
				// or the Update screen - see installConfirmOrigin), not
				// all the way out to the main menu.
				m.current = m.installConfirmOrigin
				m.installConfirmInput.SetValue("")
				m.installConfirmInput.Blur()
				m.installConfirmError = ""
				m.installPlanLines = nil
				return m, nil
			}
			if m.current == screenUpdateConfirm {
				// Cancel back into the Update screen, not all the way out
				// to the main menu - same reasoning as screenInstallConfirm
				// below, since this is reached from there now too.
				m.current = screenUpdateVersions
				m.updateConfirmInput.SetValue("")
				m.updateConfirmInput.Blur()
				m.updateConfirmError = ""
				return m, nil
			}
			m.current = screenMenu
			m.input = ""
			m.result = ""
			m.resultSource = ""
			m.installStepIdx = 0
			m.installLogs = nil
			m.installDone = false
			m.installFailed = false
			m.installCh = nil
			m.installPlanLines = nil
			m.installConfirmOrigin = screenMenu
			m.settingsCursor = 0
			m.settingsScroll = 0
			m.settingsEditing = false
			m.settingsSaving = false
			m.settingsShowAdvanced = false
			m.settingsShowNetwork = false
			m.settingsPicking = false
			m.settingsPickCursor = 0
			m.settingsPickScroll = 0
			m.settingsError = ""
			m.settingsInput.SetValue("")
			m.settingsInput.Blur()
			m.updateVersionsCursor = 0
			m.updateVersionsScroll = 0
			m.updateVersionsPicking = false
			m.updateVersionsPickCursor = 0
			m.updateVersionsPickScroll = 0
			m.installConfirmInput.SetValue("")
			m.installConfirmInput.Blur()
			m.installConfirmError = ""
			m.updateChecking = false
			m.updateCheckErr = ""
			m.updateLatestVersion = ""
			m.updateBinaryURL = ""
			m.updateChecksumHex = ""
			m.updateAlreadyLatest = false
			m.updateConfirmInput.SetValue("")
			m.updateConfirmInput.Blur()
			m.updateConfirmError = ""
			m.updateStepIdx = 0
			m.updateLogs = nil
			m.updateDone = false
			m.updateFailed = false
			m.updateCh = nil
			m.passwordSaving = false
			m.passwordError = ""
			m.passwordConfirmFocus = false
			m.passwordNewInput.SetValue("")
			m.passwordNewInput.Blur()
			m.passwordConfirmInput.SetValue("")
			m.passwordConfirmInput.Blur()
			m.hostnameSaving = false
			m.hostnameError = ""
			m.hostnameInput.SetValue("")
			m.hostnameInput.Blur()
			m.hostnameChangeForUpdate = false
			m.stabilizerZoneInput.SetValue("")
			m.stabilizerZoneInput.Blur()
			m.stabilizerNatsPasswordInput.SetValue("")
			m.stabilizerNatsPasswordInput.Blur()
			m.stabilizerNebulaInput.SetValue("")
			m.stabilizerNebulaInput.Blur()
			m.stabilizerNebulaResolving = false
			m.stabilizerNebulaContent = ""
			m.stabilizerError = ""
			m.stabilizerWillAdopt = false
			m.stabilizerConfirmInput.SetValue("")
			m.stabilizerConfirmInput.Blur()
			m.stabilizerConfirmError = ""
			m.stabilizerStepIdx = 0
			m.stabilizerLogs = nil
			m.stabilizerDone = false
			m.stabilizerFailed = false
			m.stabilizerCh = nil
			clearPendingStabilizerSecrets()
			// stabilizerSettingsState is deliberately NOT cleared here - it's
			// cached live cluster data for display in Settings, same as
			// cachedStabilizerDetected/cachedAileronVersions above, not a
			// per-flow transient.
			m.stabilizerSettingsPendingDef = stabilizerSettingDef{}
			m.stabilizerSettingsPendingValue = nil
			m.stabilizerSettingsPendingCurrent = nil
			m.stabilizerSettingsConfirmInput.SetValue("")
			m.stabilizerSettingsConfirmInput.Blur()
			m.stabilizerSettingsConfirmError = ""
			m.stabilizerSettingsApplyPipeline = nil
			m.stabilizerSettingsApplyStepIdx = 0
			m.stabilizerSettingsApplyLogs = nil
			m.stabilizerSettingsApplyDone = false
			m.stabilizerSettingsApplyFailed = false
			m.stabilizerSettingsApplyCh = nil
			m.stabilizerVersionTarget = ""
			m.stabilizerVersionPatch = nil
			m.stabilizerVersionClearedPins = nil
			m.stabilizerVersionConfirmInput.SetValue("")
			m.stabilizerVersionConfirmInput.Blur()
			m.stabilizerVersionConfirmError = ""
			m.stabilizerVersionApplyPipeline = nil
			m.stabilizerVersionApplyStepIdx = 0
			m.stabilizerVersionApplyLogs = nil
			m.stabilizerVersionApplyDone = false
			m.stabilizerVersionApplyFailed = false
			m.stabilizerVersionApplyCh = nil
			m.osUpdateStepIdx = 0
			m.osUpdateLogs = nil
			m.osUpdateDone = false
			m.osUpdateFailed = false
			m.osUpdateCh = nil
			return m, tea.Batch(fetchServiceStatusesCmd(m.cfg), fetchHostStatsCmd(m.cfg, m.prevCPUSample))

		case tea.KeyUp:
			if m.current == screenSettings && m.settingsPicking {
				if m.settingsPickCursor > 0 {
					m.settingsPickCursor--
				}
				m.settingsPickScroll = clampScroll(m.settingsPickScroll, m.settingsPickCursor, m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenSettings && !m.settingsEditing {
				if m.settingsCursor > 0 {
					m.settingsCursor--
				}
				m.settingsScroll = clampScroll(m.settingsScroll, m.settingsScrollCursor(), m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenUpdateVersions && m.updateVersionsPicking {
				if m.updateVersionsPickCursor > 0 {
					m.updateVersionsPickCursor--
				}
				m.updateVersionsPickScroll = clampScroll(m.updateVersionsPickScroll, m.updateVersionsPickCursor, m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenUpdateVersions {
				if m.updateVersionsCursor > 0 {
					m.updateVersionsCursor--
				}
				m.updateVersionsScroll = clampScroll(m.updateVersionsScroll, m.updateVersionsScrollCursor(), m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenMenu {
				if m.menuCursor > 0 {
					m.menuCursor--
				}
				return m, nil
			}

		case tea.KeyDown:
			if m.current == screenSettings && m.settingsPicking {
				if m.settingsPickCursor < len(m.settingsPickOptions)-1 {
					m.settingsPickCursor++
				}
				m.settingsPickScroll = clampScroll(m.settingsPickScroll, m.settingsPickCursor, m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenSettings && !m.settingsEditing {
				// len(m.settingsRows()), not -1: one cursor position past
				// the last real row lands on Apply, the fixed action bar
				// pinned below the table (see settingsScrollCursor).
				if m.settingsCursor < len(m.settingsRows()) {
					m.settingsCursor++
				}
				m.settingsScroll = clampScroll(m.settingsScroll, m.settingsScrollCursor(), m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenUpdateVersions && m.updateVersionsPicking {
				if m.updateVersionsPickCursor < len(m.updateVersionsPickOptions)-1 {
					m.updateVersionsPickCursor++
				}
				m.updateVersionsPickScroll = clampScroll(m.updateVersionsPickScroll, m.updateVersionsPickCursor, m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenUpdateVersions {
				// len(updateVersionsRows()), not -1: one cursor position
				// past the last real row lands on Apply upgrades (see
				// updateVersionsScrollCursor).
				if m.updateVersionsCursor < len(updateVersionsRows()) {
					m.updateVersionsCursor++
				}
				m.updateVersionsScroll = clampScroll(m.updateVersionsScroll, m.updateVersionsScrollCursor(), m.settingsVisibleRows())
				return m, nil
			}
			if m.current == screenMenu {
				if m.menuCursor < len(menuOrder)-1 {
					m.menuCursor++
				}
				return m, nil
			}

		case tea.KeyEnter:
			if m.current == screenMenu {
				// Typing a number/word into input always wins over the
				// ↑/↓ cursor when there's something typed - see
				// showCursor in view.go's screenMenu case.
				typed := m.input
				if typed == "" {
					typed = menuOrder[m.menuCursor]
				}
				if label, ok := resolveInput(typed); ok {
					m.input = ""
					switch label {
					case "logout":
						return m, tea.Quit
					case "configure":
						if !m.cfg.System.HostnameDeclared && hostnameIsDefault() && !hostnameLocked() {
							// Not yet declared, still the well-known default,
							// and install hasn't proceeded far enough to lock
							// it (see hostnameLocked, hostname.go) - same
							// "force it before Settings" reasoning as the
							// password check below, checked first (see
							// initialModel's comment on ordering).
							m.current = screenHostnameChange
							m.hostnameError = ""
							m.hostnameChangeForUpdate = false
							m.hostnameInput.Focus()
							return m, nil
						}
						if m.cfg.System.PasswordChanged {
							m.current = screenSettings
							// Land on the first real field row - Apply now
							// lives in its own fixed footer below the
							// table (see settingsScrollCursor), not at the
							// top of the row list.
							m.settingsCursor = 0
							m.settingsScroll = 0
							m.settingsEditing = false
							m.settingsShowAdvanced = false
							m.settingsShowNetwork = false
							m.settingsError = ""
							return m, nil
						}
						// Not yet confirmed changed from the well-known
						// default - verify (and force a change if it's
						// still the default) before letting the operator
						// into Settings.
						m.current = screenPasswordCheck
						m.passwordError = ""
						return m, checkPasswordChangedCmd()
					case "shell":
						m.shellMode = true
						return m, tea.Quit
					case "k9s":
						m.k9sMode = true
						return m, tea.Quit
					case "update":
						if !m.cfg.System.HostnameDeclared && hostnameIsDefault() && !hostnameLocked() {
							// "update" can also reach the install pipeline
							// (Apply upgrades re-runs installSteps, same as
							// Settings' Apply) without ever visiting
							// "configure" - same "force it before install
							// can run" reasoning as the "configure" case
							// above, just resuming here instead of Settings
							// once declared (see hostnameChangeForUpdate).
							m.current = screenHostnameChange
							m.hostnameError = ""
							m.hostnameChangeForUpdate = true
							m.hostnameInput.Focus()
							return m, nil
						}
						// Lands on screenUpdateVersions, not straight into
						// the ruddervirt-setup check - that's now just the
						// first row there, alongside the component
						// versions moved out of Settings, so every upgrade
						// happens from one place (see updateVersionsRows).
						m.current = screenUpdateVersions
						m.updateVersionsCursor = 0
						m.updateVersionsScroll = 0
						m.updateVersionsPicking = false
						m.settingsSaving = false
						m.settingsError = ""
						return m, nil
					default:
						m.result = label
						m.current = screenResult
					}
				} else {
					m.input = ""
				}
			} else if m.current == screenUpdateConfirm {
				if strings.EqualFold(strings.TrimSpace(m.updateConfirmInput.Value()), "yes") {
					m.current = screenUpdate
					m.updateStepIdx = 0
					m.updateLogs = nil
					m.updateDone = false
					m.updateFailed = false
					m.updateConfirmInput.Blur()
					pendingUpdateVersion = m.updateLatestVersion
					pendingUpdateBinaryURL = m.updateBinaryURL
					pendingUpdateChecksum = m.updateChecksumHex
					ch, cmd := launchStep(updateSteps[0], m.cfg)
					m.updateCh = ch
					return m, cmd
				}
				m.updateConfirmError = `Type "yes" to proceed, or Esc to cancel.`
			} else if m.current == screenInstallConfirm {
				if strings.EqualFold(strings.TrimSpace(m.installConfirmInput.Value()), "yes") {
					m.current = screenInstall
					m.installStepIdx = 0
					m.installLogs = nil
					m.installDone = false
					m.installFailed = false
					m.installConfirmInput.Blur()
					ch, cmd := launchStep(installSteps[0], m.cfg)
					m.installCh = ch
					return m, cmd
				}
				m.installConfirmError = `Type "yes" to proceed, or Esc to cancel.`
			} else if m.current == screenSettings {
				if m.settingsPicking {
					row := m.settingsRows()[m.settingsCursor]
					if row.stabilizerDef != nil {
						def := *row.stabilizerDef
						chosen := m.settingsPickOptions[m.settingsPickCursor]
						value, current, err := resolveStabilizerSettingChange(m.stabilizerSettingsState, def, chosen)
						if err != nil {
							m.settingsError = err.Error()
							return m, nil
						}
						m.settingsPicking = false
						if current != nil && stabilizerSettingValuesEqual(def, value, current) {
							m.settingsError = fmt.Sprintf("%s is already %s - no change", def.Key, formatStabilizerSettingValue(def, value))
							return m, nil
						}
						m.stabilizerSettingsPendingDef = def
						m.stabilizerSettingsPendingValue = value
						m.stabilizerSettingsPendingCurrent = current
						m.current = screenStabilizerSettingsConfirm
						m.stabilizerSettingsConfirmInput.SetValue("")
						m.stabilizerSettingsConfirmInput.Focus()
						m.stabilizerSettingsConfirmError = ""
						m.settingsError = ""
						return m, nil
					}
					field := row.field
					chosen := m.settingsPickOptions[m.settingsPickCursor]
					if err := field.set(&m.cfg, chosen); err != nil {
						m.settingsError = err.Error()
						return m, nil
					}
					m.settingsPicking = false
					m.settingsSaving = true
					m.settingsError = ""
					return m, saveSettingCmd(m.cfg)
				}
				rows := m.settingsRows()
				if m.settingsCursor == len(rows) {
					// Apply - the fixed footer below the table, one cursor
					// position past the last real row (see
					// settingsScrollCursor).
					//
					// Defense-in-depth: the "configure" menu entry already
					// forces screenHostnameChange before Settings is ever
					// reachable, but guard the actual install trigger too,
					// in case Settings was somehow reached another way -
					// installing must never proceed without a declared
					// hostname (see hostnameLocked, hostname.go).
					if !m.cfg.System.HostnameDeclared && hostnameIsDefault() && !hostnameLocked() {
						m.current = screenHostnameChange
						m.hostnameError = ""
						m.hostnameChangeForUpdate = false
						m.hostnameInput.Focus()
						return m, nil
					}
					if err := resolveNetworkForInstall(&m.cfg); err != nil {
						m.settingsError = err.Error()
						return m, nil
					}
					m.current = screenInstallPlanning
					m.installConfirmOrigin = screenSettings
					m.installPlanLines = nil
					return m, computeInstallPlanCmd(m.cfg)
				}
				row := rows[m.settingsCursor]
				if row.isStabilizerAction {
					// Checks aileron is actually installed and running
					// before anything else - adopting against an aileron
					// that was never installed, or isn't healthy, would
					// re-stamp ownership of resources that aren't really
					// there. Only once that passes does it move on to the
					// coordination warning (this can't proceed without
					// selfhosted@ruddervirt.com providing secrets first).
					m.current = screenStabilizerAileronCheck
					m.settingsError = ""
					m.stabilizerError = ""
					return m, checkAileronReadyCmd(kubectlBinPath)
				}
				if row.stabilizerDef != nil {
					// Live-cluster-backed row, not Config-backed - handled
					// entirely separately from the field.get/set path below,
					// but still reuses the exact same
					// settingsPicking/settingsEditing/settingsInput/
					// settingsPickOptions fields every Config-backed row
					// already uses, so it's an ordinary in-situ row, not a
					// separate flow. m.settingsPicking's own confirm is
					// handled earlier (the `if m.settingsPicking` branch at
					// the top of this else-if chain always returns, so it's
					// never reached from here) - only settingsEditing's
					// confirm needs handling here, since unlike picking it
					// falls through this same row-dispatch on its second
					// Enter press too.
					def := *row.stabilizerDef
					state := m.stabilizerSettingsState
					if m.settingsEditing {
						if state == nil {
							m.settingsError = "stabilizer settings are still loading - try again in a moment"
							return m, nil
						}
						value, current, err := resolveStabilizerSettingChange(state, def, m.settingsInput.Value())
						if err != nil {
							m.settingsError = err.Error()
							return m, nil
						}
						m.settingsEditing = false
						m.settingsInput.Blur()
						if current != nil && stabilizerSettingValuesEqual(def, value, current) {
							m.settingsError = fmt.Sprintf("%s is already %s - no change", def.Key, formatStabilizerSettingValue(def, value))
							return m, nil
						}
						m.stabilizerSettingsPendingDef = def
						m.stabilizerSettingsPendingValue = value
						m.stabilizerSettingsPendingCurrent = current
						m.current = screenStabilizerSettingsConfirm
						m.stabilizerSettingsConfirmInput.SetValue("")
						m.stabilizerSettingsConfirmInput.Focus()
						m.stabilizerSettingsConfirmError = ""
						m.settingsError = ""
						return m, nil
					}
					if state == nil {
						m.settingsError = "stabilizer settings are still loading - try again in a moment"
						return m, nil
					}
					if state.jobActive {
						m.settingsError = fmt.Sprintf("a stabilizer release operation is already in progress (helm-install-%s job is active) - wait for it to finish and try again", state.helmChartName)
						return m, nil
					}
					if _, _, editable := stabilizerSettingRowDisplay(def, state); !editable {
						m.settingsError = fmt.Sprintf("%s can't be changed right now", def.Key)
						return m, nil
					}
					m.settingsError = ""
					if def.Type == stabilizerSettingBool {
						m.settingsPickOptions = []string{"true", "false"}
						m.settingsPickCursor = 0
						if appliedRaw, ok := state.appliedEnv[def.Env]; ok {
							for i, o := range m.settingsPickOptions {
								if strings.EqualFold(o, appliedRaw) {
									m.settingsPickCursor = i
									break
								}
							}
						}
						m.settingsPicking = true
						return m, nil
					}
					m.settingsInput.SetValue(state.appliedEnv[def.Env])
					m.settingsInput.CursorEnd()
					m.settingsInput.Focus()
					m.settingsEditing = true
					return m, nil
				}
				if row.isNetworkToggle {
					m.settingsShowNetwork = !m.settingsShowNetwork
					return m, nil
				}
				if row.isToggle {
					// Its position (right after the basic fields) never
					// moves when toggling, since expanding only inserts
					// rows after it - so the cursor just stays put.
					m.settingsShowAdvanced = !m.settingsShowAdvanced
					return m, nil
				}
				field := row.field
				versions := versionCache{K3s: m.cachedK3sVersions, Aileron: m.cachedAileronVersions, StabilizerDetected: m.cachedStabilizerDetected}
				if field.locked != nil {
					if locked, reason := field.locked(&m.cfg, versions); locked {
						m.settingsError = reason
						return m, nil
					}
				}
				if field.options != nil {
					options := field.options(&m.cfg, versions)
					if len(options) == 0 {
						return m, nil // nothing to pick yet - e.g. still fetching, or none detected
					}
					m.settingsPickOptions = options
					m.settingsPickCursor = 0
					if current := field.get(&m.cfg); current != "" {
						for i, o := range options {
							if o == current {
								m.settingsPickCursor = i
								break
							}
						}
					}
					m.settingsPickScroll = clampScroll(0, m.settingsPickCursor, m.settingsVisibleRows())
					m.settingsPicking = true
					m.settingsError = ""
					return m, nil
				}
				if m.settingsEditing {
					if err := field.set(&m.cfg, m.settingsInput.Value()); err != nil {
						m.settingsError = err.Error()
						return m, nil
					}
					m.settingsEditing = false
					m.settingsSaving = true
					m.settingsError = ""
					m.settingsInput.Blur()
					return m, saveSettingCmd(m.cfg)
				}
				m.settingsInput.SetValue(field.get(&m.cfg))
				m.settingsInput.CursorEnd()
				m.settingsInput.Focus()
				m.settingsEditing = true
				m.settingsError = ""
			} else if m.current == screenUpdateVersions {
				rows := updateVersionsRows()
				if m.updateVersionsPicking {
					field := rows[m.updateVersionsCursor].field
					chosen := m.updateVersionsPickOptions[m.updateVersionsPickCursor]
					if field.key == "versions.aileron" && m.cachedStabilizerDetected {
						if m.stabilizerSettingsState == nil {
							m.settingsError = "still loading stabilizer state - try again in a moment"
							return m, nil
						}
						target := strings.TrimPrefix(chosen, "v")
						patch, cleared, err := planStabilizerVersionUpgrade(m.stabilizerSettingsState, target)
						if err != nil {
							m.updateVersionsPicking = false
							m.settingsError = err.Error()
							return m, nil
						}
						m.updateVersionsPicking = false
						m.stabilizerVersionTarget = target
						m.stabilizerVersionPatch = patch
						m.stabilizerVersionClearedPins = cleared
						m.current = screenStabilizerVersionConfirm
						m.stabilizerVersionConfirmInput.SetValue("")
						m.stabilizerVersionConfirmInput.Focus()
						m.stabilizerVersionConfirmError = ""
						m.settingsError = ""
						return m, nil
					}
					if err := field.set(&m.cfg, chosen); err != nil {
						m.settingsError = err.Error()
						return m, nil
					}
					m.updateVersionsPicking = false
					m.settingsSaving = true
					m.settingsError = ""
					return m, saveSettingCmd(m.cfg)
				}
				if m.updateVersionsCursor == len(rows) {
					// Apply upgrades - the fixed footer below the table,
					// one cursor position past the last real row (see
					// updateVersionsScrollCursor). Same install pipeline
					// Settings' Apply uses.
					//
					// Defense-in-depth: same guard as Settings' Apply above
					// - the "update" menu entry already forces
					// screenHostnameChange first, but this is the actual
					// install trigger, so it's guarded here too.
					if !m.cfg.System.HostnameDeclared && hostnameIsDefault() && !hostnameLocked() {
						m.current = screenHostnameChange
						m.hostnameError = ""
						m.hostnameChangeForUpdate = true
						m.hostnameInput.Focus()
						return m, nil
					}
					if err := resolveNetworkForInstall(&m.cfg); err != nil {
						m.settingsError = err.Error()
						return m, nil
					}
					m.current = screenInstallPlanning
					m.installConfirmOrigin = screenUpdateVersions
					m.installPlanLines = nil
					return m, computeInstallPlanCmd(m.cfg)
				}
				row := rows[m.updateVersionsCursor]
				if row.isSelfUpdate {
					m.current = screenUpdateChecking
					m.updateChecking = true
					m.updateCheckErr = ""
					return m, checkForUpdateCmd()
				}
				if row.isOSUpdate {
					// No separate check/confirm step - the user asked for
					// this to just apply `rpm-ostree upgrade
					// --bypass-driver` directly (osUpdateSteps, os_update.go).
					// Unlike a k3s/kubevirt/etc. version bump this can't
					// silently move to something unexpected: rpm-ostree only
					// ever moves to the single latest deployment on the
					// configured stream, and doesn't even take effect until
					// a reboot, so there's nothing meaningful to confirm.
					m.current = screenOSUpdate
					m.osUpdateStepIdx = 0
					m.osUpdateLogs = nil
					m.osUpdateDone = false
					m.osUpdateFailed = false
					ch, cmd := launchStep(osUpdateSteps[0], m.cfg)
					m.osUpdateCh = ch
					return m, cmd
				}
				field := row.field
				versions := versionCache{K3s: m.cachedK3sVersions, Aileron: m.cachedAileronVersions, StabilizerDetected: m.cachedStabilizerDetected}
				if field.key == "versions.aileron" && versions.StabilizerDetected {
					// Once stabilizer manages Aileron, "Aileron version" means
					// the stabilizer chart's own spec.version (Aileron ships
					// as its subchart, pinned to whatever version the chart
					// release bundles, in lockstep with aileron's own release
					// tags - see stabilizerVersionPickerOptions). Same
					// fetch-a-list-and-pick shape as every other
					// component-version row, just validated through the
					// guarded flow (stabilizer_upgrade.go) instead of a plain
					// field.set, and no longer a hard, uneditable lock.
					if m.stabilizerSettingsState == nil {
						m.settingsError = "still loading stabilizer state - try again in a moment"
						return m, nil
					}
					options := stabilizerVersionPickerOptions(m.cachedAileronVersions, m.stabilizerSettingsState.declaredVersion)
					if len(options) == 0 {
						m.settingsError = "no eligible releases fetched yet - try again in a moment"
						return m, nil // nothing to pick yet - e.g. still fetching
					}
					m.updateVersionsPickOptions = options
					m.updateVersionsPickCursor = 0
					m.updateVersionsPickScroll = clampScroll(0, m.updateVersionsPickCursor, m.settingsVisibleRows())
					m.updateVersionsPicking = true
					m.settingsError = ""
					return m, nil
				}
				if field.locked != nil {
					if locked, reason := field.locked(&m.cfg, versions); locked {
						m.settingsError = reason
						return m, nil
					}
				}
				// Every updateScreen field is picker-only (all four
				// component-version fields set options), so this always
				// has something to open.
				options := field.options(&m.cfg, versions)
				if len(options) == 0 {
					return m, nil // nothing to pick yet - e.g. still fetching, or none detected
				}
				m.updateVersionsPickOptions = options
				m.updateVersionsPickCursor = 0
				if current := field.get(&m.cfg); current != "" {
					for i, o := range options {
						if o == current {
							m.updateVersionsPickCursor = i
							break
						}
					}
				}
				m.updateVersionsPickScroll = clampScroll(0, m.updateVersionsPickCursor, m.settingsVisibleRows())
				m.updateVersionsPicking = true
				m.settingsError = ""
			} else if m.current == screenPasswordChange {
				if !m.passwordConfirmFocus {
					// Validated here, before the confirm field ever gets
					// focus - so a bad password (empty or too short) is
					// caught while the operator can still just fix it and
					// press Enter again, instead of only surfacing once
					// they've moved on to confirm (see the KeyEsc case
					// below for the way back from there).
					if m.passwordNewInput.Value() == "" {
						m.passwordError = "password must not be empty"
						return m, nil
					}
					if len(m.passwordNewInput.Value()) < minAdminPasswordLength {
						m.passwordError = fmt.Sprintf("password must be at least %d characters", minAdminPasswordLength)
						return m, nil
					}
					m.passwordNewInput.Blur()
					m.passwordConfirmInput.Focus()
					m.passwordConfirmFocus = true
					m.passwordError = ""
					return m, nil
				}
				newVal := m.passwordNewInput.Value()
				if newVal != m.passwordConfirmInput.Value() {
					m.passwordError = "passwords do not match"
					m.passwordConfirmInput.SetValue("")
					return m, nil
				}
				m.passwordSaving = true
				m.passwordError = ""
				return m, setAdminPasswordCmd(newVal)
			} else if m.current == screenHostnameChange {
				newHostname, err := parseHostname(m.hostnameInput.Value())
				if err != nil {
					m.hostnameError = err.Error()
					return m, nil
				}
				m.hostnameSaving = true
				m.hostnameError = ""
				return m, setHostnameCmd(newHostname)
			} else if m.current == screenStabilizerWarning {
				m.current = screenStabilizerZone
				m.stabilizerZoneInput.SetValue(m.cfg.Stabilizer.Zone)
				m.stabilizerZoneInput.CursorEnd()
				m.stabilizerZoneInput.Focus()
				m.stabilizerError = ""
				return m, nil
			} else if m.current == screenStabilizerZone {
				zone, err := stabilizerNonEmptyField("zone name", m.stabilizerZoneInput.Value())
				if err != nil {
					m.stabilizerError = err.Error()
					return m, nil
				}
				m.cfg.Stabilizer.Zone = zone
				m.stabilizerZoneInput.Blur()
				// NATS URL is fixed (defaultStabilizerNatsURL) and the NATS
				// username is always the zone name - ruddervirt provides
				// both implicitly by providing the zone and a password, so
				// neither gets its own input screen.
				m.current = screenStabilizerNatsPassword
				m.stabilizerNatsPasswordInput.SetValue("")
				m.stabilizerNatsPasswordInput.Focus()
				m.stabilizerError = ""
				return m, nil
			} else if m.current == screenStabilizerNatsPassword {
				if _, err := stabilizerNonEmptyField("NATS password", m.stabilizerNatsPasswordInput.Value()); err != nil {
					m.stabilizerError = err.Error()
					return m, nil
				}
				m.stabilizerNatsPasswordInput.Blur()
				m.current = screenStabilizerNebula
				m.stabilizerNebulaInput.SetValue("")
				m.stabilizerNebulaInput.Focus()
				m.stabilizerError = ""
				return m, nil
			} else if m.current == screenStabilizerNebula {
				pathOrURL, err := stabilizerNonEmptyField("nebula config path/URL", m.stabilizerNebulaInput.Value())
				if err != nil {
					m.stabilizerError = err.Error()
					return m, nil
				}
				m.stabilizerNebulaResolving = true
				m.stabilizerError = ""
				return m, resolveNebulaConfigCmd(pathOrURL)
			} else if m.current == screenStabilizerConfirm {
				if strings.EqualFold(strings.TrimSpace(m.stabilizerConfirmInput.Value()), "yes") {
					// NATS URL is always the fixed shared bus address, and
					// the NATS username is always the zone name - see
					// screenStabilizerZone above.
					m.cfg.Stabilizer.NatsURL = defaultStabilizerNatsURL
					pendingStabilizerNatsUser = m.cfg.Stabilizer.Zone
					pendingStabilizerNatsPassword = m.stabilizerNatsPasswordInput.Value()
					pendingStabilizerNebulaConfig = m.stabilizerNebulaContent
					m.cfg.Stabilizer.Version = defaultStabilizerVersion
					m.current = screenStabilizerAdopt
					m.stabilizerStepIdx = 0
					m.stabilizerLogs = nil
					m.stabilizerDone = false
					m.stabilizerFailed = false
					m.stabilizerConfirmInput.Blur()
					m.stabilizerNatsPasswordInput.SetValue("")
					m.stabilizerNatsPasswordInput.Blur()
					m.stabilizerNebulaInput.SetValue("")
					m.stabilizerNebulaInput.Blur()
					m.stabilizerNebulaContent = ""
					ch, cmd := launchStep(stabilizerSteps[0], m.cfg)
					m.stabilizerCh = ch
					return m, tea.Batch(saveSettingCmd(m.cfg), cmd)
				}
				m.stabilizerConfirmError = `Type "yes" to proceed, or Esc to cancel.`
			} else if m.current == screenStabilizerSettingsConfirm {
				if strings.EqualFold(strings.TrimSpace(m.stabilizerSettingsConfirmInput.Value()), "yes") {
					if m.stabilizerSettingsState == nil {
						return m, nil
					}
					m.stabilizerSettingsConfirmInput.Blur()
					m.stabilizerSettingsApplyDone = false
					m.stabilizerSettingsApplyFailed = false
					m.stabilizerSettingsApplyLogs = nil
					m.stabilizerSettingsApplyStepIdx = 0
					steps := stabilizerSettingsApplySteps(
						m.stabilizerSettingsState.helmChartNamespace, m.stabilizerSettingsState.helmChartName,
						m.stabilizerSettingsPendingDef, m.stabilizerSettingsPendingValue)
					m.stabilizerSettingsApplyPipeline = steps
					m.current = screenStabilizerSettingsApply
					ch, cmd := launchStep(steps[0], m.cfg)
					m.stabilizerSettingsApplyCh = ch
					return m, cmd
				}
				m.stabilizerSettingsConfirmError = `Type "yes" to proceed, or Esc to cancel.`
			} else if m.current == screenStabilizerVersionConfirm {
				if strings.EqualFold(strings.TrimSpace(m.stabilizerVersionConfirmInput.Value()), "yes") {
					if m.stabilizerSettingsState == nil {
						return m, nil
					}
					m.stabilizerVersionConfirmInput.Blur()
					m.stabilizerVersionApplyDone = false
					m.stabilizerVersionApplyFailed = false
					m.stabilizerVersionApplyLogs = nil
					m.stabilizerVersionApplyStepIdx = 0
					steps := stabilizerVersionApplySteps(
						m.stabilizerSettingsState.helmChartNamespace, m.stabilizerSettingsState.helmChartName,
						m.stabilizerVersionPatch, m.stabilizerVersionTarget)
					m.stabilizerVersionApplyPipeline = steps
					m.current = screenStabilizerVersionApply
					ch, cmd := launchStep(steps[0], m.cfg)
					m.stabilizerVersionApplyCh = ch
					return m, cmd
				}
				m.stabilizerVersionConfirmError = `Type "yes" to proceed, or Esc to cancel.`
			}
			return m, nil

		case tea.KeyBackspace:
			if m.current == screenMenu && len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
				return m, nil
			}
		}
	}

	if m.current == screenSettings && m.settingsEditing {
		var cmd tea.Cmd
		m.settingsInput, cmd = m.settingsInput.Update(msg)
		return m, cmd
	}

	if m.current == screenInstallConfirm {
		var cmd tea.Cmd
		m.installConfirmInput, cmd = m.installConfirmInput.Update(msg)
		return m, cmd
	}

	if m.current == screenUpdateConfirm {
		var cmd tea.Cmd
		m.updateConfirmInput, cmd = m.updateConfirmInput.Update(msg)
		return m, cmd
	}

	if m.current == screenPasswordChange {
		var cmd tea.Cmd
		if m.passwordConfirmFocus {
			m.passwordConfirmInput, cmd = m.passwordConfirmInput.Update(msg)
		} else {
			m.passwordNewInput, cmd = m.passwordNewInput.Update(msg)
		}
		return m, cmd
	}

	if m.current == screenHostnameChange {
		var cmd tea.Cmd
		m.hostnameInput, cmd = m.hostnameInput.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerZone {
		var cmd tea.Cmd
		m.stabilizerZoneInput, cmd = m.stabilizerZoneInput.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerNatsPassword {
		var cmd tea.Cmd
		m.stabilizerNatsPasswordInput, cmd = m.stabilizerNatsPasswordInput.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerNebula {
		var cmd tea.Cmd
		m.stabilizerNebulaInput, cmd = m.stabilizerNebulaInput.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerConfirm {
		var cmd tea.Cmd
		m.stabilizerConfirmInput, cmd = m.stabilizerConfirmInput.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerSettingsConfirm {
		var cmd tea.Cmd
		m.stabilizerSettingsConfirmInput, cmd = m.stabilizerSettingsConfirmInput.Update(msg)
		return m, cmd
	}

	if m.current == screenStabilizerVersionConfirm {
		var cmd tea.Cmd
		m.stabilizerVersionConfirmInput, cmd = m.stabilizerVersionConfirmInput.Update(msg)
		return m, cmd
	}

	if m.current == screenMenu {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			if keyMsg.Type == tea.KeyRunes {
				m.input += keyMsg.String()
			}
		}
	}

	return m, nil
}
