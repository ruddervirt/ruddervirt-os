// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type screen int

const (
	screenMenu screen = iota
	screenResult
	screenInstall
	screenSettings
	screenInstallPlanning
	screenInstallConfirm
	screenUpdateVersions
	screenUpdateChecking
	screenUpdateConfirm
	screenUpdate
	screenPasswordCheck
	screenPasswordChange
)

type stepOutputMsg string
type stepDoneMsg struct {
	label string
	err   error
}

// installPlanMsg carries computeInstallPlanCmd's result back into Update -
// one line per installSteps entry, same order/index.
type installPlanMsg struct {
	lines []string
}

type settingsSavedMsg struct {
	err error
}

type model struct {
	input        string
	result       string
	current      screen
	resultSource string
	// menuCursor is the main menu's arrow-key selection, an alternative to
	// typing a number/word into input - see menuOrder (view.go). Enter
	// submits input if it's non-empty, otherwise menuOrder[menuCursor].
	menuCursor int

	installStepIdx   int
	installLogs      []string
	installDone      bool
	installFailed    bool
	installCh        chan tea.Msg
	installPlanLines []string
	// installConfirmOrigin remembers which screen's Apply button started
	// screenInstallPlanning/screenInstallConfirm - both Settings' Apply and
	// the Update screen's Apply upgrades funnel into this same confirm
	// flow, so canceling (Esc) needs to know which one to return to
	// instead of always assuming Settings.
	installConfirmOrigin screen

	cfg                  Config
	settingsCursor       int
	settingsScroll       int
	settingsEditing      bool
	settingsSaving       bool
	settingsShowAdvanced bool
	settingsShowNetwork  bool
	settingsInput        textinput.Model
	settingsError        string

	// Picker sub-mode for select-type fields (settingField.options != nil):
	// Enter opens it instead of the free-text edit box above.
	settingsPicking     bool
	settingsPickCursor  int
	settingsPickScroll  int
	settingsPickOptions []string

	installConfirmInput textinput.Model
	installConfirmError string

	// screenUpdateVersions - "update" menu's landing page: a
	// ruddervirt-setup row (delegates into the updateChecking/... flow
	// below) plus the component version fields moved out of Settings
	// (settingField.updateScreen), all upgraded together via the same
	// install pipeline Settings' Apply uses. Same shape as the
	// settings*/settingsPick* fields above, just for this screen's rows
	// instead of settingsRows().
	updateVersionsCursor      int
	updateVersionsScroll      int
	updateVersionsPicking     bool
	updateVersionsPickCursor  int
	updateVersionsPickScroll  int
	updateVersionsPickOptions []string

	updateChecking      bool
	updateCheckErr      string
	updateLatestVersion string
	updateBinaryURL     string
	updateChecksumHex   string
	updateAlreadyLatest bool

	updateConfirmInput textinput.Model
	updateConfirmError string

	updateStepIdx   int
	updateLogs      []string
	updateDone      bool
	updateFailed    bool
	updateCh        chan tea.Msg
	updateInstalled bool

	// Forced admin-password-change flow, gating entry into "configure" -
	// see checkPasswordChangedCmd/password.go.
	passwordNewInput     textinput.Model
	passwordConfirmInput textinput.Model
	passwordConfirmFocus bool
	passwordSaving       bool
	passwordError        string

	termWidth  int
	termHeight int

	shellMode bool
	k9sMode   bool

	// Home screen's "Services" summary - nil until fetchServiceStatusesCmd
	// (status.go) reports back, refreshed every serviceStatusRefreshInterval
	// while the menu is shown. serviceStatusUpdatedAt tracks when that last
	// happened, purely so the view can show "updated Xs ago".
	serviceStatuses        []serviceStatus
	serviceStatusUpdatedAt time.Time

	// cachedK3sVersions/cachedAileronVersions hold the release tags
	// fetchK3sVersions/fetchAileronVersions found, for the Settings screen's
	// version fields to pick from - populated once, best-effort, via Init's
	// fetchK3sVersionsCmd/fetchAileronVersionsCmd.
	cachedK3sVersions     []string
	cachedAileronVersions []string
}

func initialModel() model {
	settingsInput := textinput.New()

	// No placeholder text: this TUI has no color styling to visually mark
	// placeholder vs. real content, so a "yes" hint here could easily be
	// mistaken for already-typed confirmation on a screen where that
	// distinction genuinely matters.
	installConfirmInput := textinput.New()
	updateConfirmInput := textinput.New()

	passwordNewInput := textinput.New()
	passwordNewInput.EchoMode = textinput.EchoPassword
	passwordNewInput.EchoCharacter = '•'
	passwordConfirmInput := textinput.New()
	passwordConfirmInput.EchoMode = textinput.EchoPassword
	passwordConfirmInput.EchoCharacter = '•'

	cfg, _ := loadConfig(configPath)
	if cfg.Network.InterfaceName == "" {
		// Pre-fill with the internet-facing interface if one can be found,
		// so Settings shows a sensible value instead of a blank field. Not
		// persisted here - only saved if the operator accepts/edits it, or
		// when resolveNetworkForInstall confirms it at install time.
		if iface, err := detectDefaultInterface(); err == nil {
			cfg.Network.InterfaceName = iface
		}
	}

	// cfg.System.PasswordChanged is false only until the admin password is
	// changed away from server.bu's well-known default - i.e. exactly the
	// window between first boot and the operator's first trip through
	// "configure". Skip the home menu and land straight on the same
	// password-check -> Settings flow selecting "configure" would trigger,
	// instead of making them find and type it themselves.
	current := screenMenu
	if !cfg.System.PasswordChanged {
		current = screenPasswordCheck
	}

	return model{
		current:              current,
		settingsInput:        settingsInput,
		installConfirmInput:  installConfirmInput,
		updateConfirmInput:   updateConfirmInput,
		passwordNewInput:     passwordNewInput,
		passwordConfirmInput: passwordConfirmInput,
		cfg:                  cfg,
		// Sane fallback until the first tea.WindowSizeMsg arrives.
		termWidth:  80,
		termHeight: 24,
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, fetchK3sVersionsCmd(), fetchAileronVersionsCmd(), fetchServiceStatusesCmd(m.cfg), tickServiceStatusCmd(), tickServiceStatusRenderCmd()}
	if m.current == screenPasswordCheck {
		// initialModel() starts here on first boot (see its comment) -
		// same command the menu's "configure" selection fires manually.
		cmds = append(cmds, checkPasswordChangedCmd())
	}
	return tea.Batch(cmds...)
}
