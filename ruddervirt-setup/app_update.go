package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		m.settingsScroll = clampScroll(m.settingsScroll, m.settingsCursor, m.settingsVisibleRows())
		return m, nil

	case k3sVersionsFetchedMsg:
		m.cachedK3sVersions = msg.versions
		return m, nil

	case aileronVersionsFetchedMsg:
		m.cachedAileronVersions = msg.versions
		return m, nil

	case serviceStatusMsg:
		m.serviceStatuses = msg.statuses
		return m, nil

	case serviceStatusTickMsg:
		if m.current == screenMenu {
			return m, tea.Batch(fetchServiceStatusesCmd(m.cfg), tickServiceStatusCmd())
		}
		return m, tickServiceStatusCmd()

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
		m.settingsCursor = 1
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
			if m.current == screenInstallConfirm {
				// Cancel back into configure/settings, not all the way
				// out to the main menu - this was reached from there.
				m.current = screenSettings
				m.installConfirmInput.SetValue("")
				m.installConfirmInput.Blur()
				m.installConfirmError = ""
				m.installPlanLines = nil
				return m, nil
			}
			if m.current == screenUpdateConfirm {
				// Cancel back to the main menu, not settings - "update"
				// is reached directly from there, unlike Apply.
				m.current = screenMenu
				m.updateConfirmInput.SetValue("")
				m.updateConfirmInput.Blur()
				m.updateConfirmError = ""
				return m, fetchServiceStatusesCmd(m.cfg)
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
				m.settingsScroll = clampScroll(m.settingsScroll, m.settingsCursor, m.settingsVisibleRows())
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
				if m.settingsCursor < len(m.settingsRows())-1 {
					m.settingsCursor++
				}
				m.settingsScroll = clampScroll(m.settingsScroll, m.settingsCursor, m.settingsVisibleRows())
				return m, nil
			}

		case tea.KeyEnter:
			if m.current == screenMenu {
				if label, ok := resolveInput(m.input); ok {
					m.input = ""
					switch label {
					case "logout":
						return m, tea.Quit
					case "configure":
						if m.cfg.System.PasswordChanged {
							m.current = screenSettings
							// Land on the network toggle (row 1), not Apply
							// (row 0) - Apply is pinned at the top for
							// visibility, not meant to grab the cursor by
							// default.
							m.settingsCursor = 1
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
				row := m.settingsRows()[m.settingsCursor]
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
				if row.isApply {
					if err := resolveNetworkForInstall(&m.cfg); err != nil {
						m.settingsError = err.Error()
						return m, nil
					}
					m.current = screenInstallPlanning
					m.installPlanLines = nil
					return m, computeInstallPlanCmd(m.cfg)
				}
				field := row.field
				if field.locked != nil {
					if locked, reason := field.locked(&m.cfg); locked {
						m.settingsError = reason
						return m, nil
					}
				}
				if field.options != nil {
					options := field.options(&m.cfg, versionCache{K3s: m.cachedK3sVersions, Aileron: m.cachedAileronVersions})
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
			} else if m.current == screenPasswordChange {
				if !m.passwordConfirmFocus {
					if m.passwordNewInput.Value() == "" {
						m.passwordError = "password must not be empty"
						return m, nil
					}
					m.passwordNewInput.Blur()
					m.passwordConfirmInput.Focus()
					m.passwordConfirmFocus = true
					m.passwordError = ""
					return m, nil
				}
				newVal := m.passwordNewInput.Value()
				if len(newVal) < minAdminPasswordLength {
					m.passwordError = fmt.Sprintf("password must be at least %d characters", minAdminPasswordLength)
					return m, nil
				}
				if newVal != m.passwordConfirmInput.Value() {
					m.passwordError = "passwords do not match"
					m.passwordConfirmInput.SetValue("")
					return m, nil
				}
				m.passwordSaving = true
				m.passwordError = ""
				return m, setAdminPasswordCmd(newVal)
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

	if m.current == screenMenu {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			if keyMsg.Type == tea.KeyRunes {
				m.input += keyMsg.String()
			}
		}
	}

	return m, nil
}
