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

	case stabilizerDetectedMsg:
		m.cachedStabilizerDetected = msg.present
		return m, nil

	case serviceStatusMsg:
		m.serviceStatuses = msg.statuses
		m.serviceStatusUpdatedAt = time.Now()
		return m, nil

	case serviceStatusTickMsg:
		if m.current == screenMenu {
			return m, tea.Batch(fetchServiceStatusesCmd(m.cfg), tickServiceStatusCmd())
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
		if m.current == screenUpdate {
			m.updateLogs = append(m.updateLogs, string(msg))
			return m, readFromCh(m.updateCh)
		}
		m.installLogs = append(m.installLogs, string(msg))
		return m, readFromCh(m.installCh)

	case stepDoneMsg:
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
			return m, fetchServiceStatusesCmd(m.cfg)

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
					field := m.settingsRows()[m.settingsCursor].field
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
				field := row.field
				versions := versionCache{K3s: m.cachedK3sVersions, Aileron: m.cachedAileronVersions, StabilizerDetected: m.cachedStabilizerDetected}
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

	if m.current == screenMenu {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			if keyMsg.Type == tea.KeyRunes {
				m.input += keyMsg.String()
			}
		}
	}

	return m, nil
}
