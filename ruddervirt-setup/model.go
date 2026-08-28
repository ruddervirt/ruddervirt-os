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
	screenOSUpdate
	screenPasswordCheck
	screenPasswordChange
	screenHostnameChange
	screenStabilizerAileronCheck
	screenStabilizerWarning
	screenStabilizerZone
	screenStabilizerNatsPassword
	screenStabilizerNebula
	screenStabilizerPlanning
	screenStabilizerConfirm
	screenStabilizerAdopt
	screenStabilizerSettingsConfirm
	screenStabilizerSettingsApply
	screenStabilizerVersionConfirm
	screenStabilizerVersionApply
)

// isStabilizerWizardScreen reports whether s is one of the "Adopt to
// ruddervirt.com" wizard's pre-execution screens - used by app_update.go's
// shared Esc-cancel handling. Deliberately excludes
// screenStabilizerAileronCheck/screenStabilizerPlanning (brief,
// non-interactive checks, same as screenInstallPlanning - Esc is simply
// blocked there) and screenStabilizerAdopt (guarded separately, only
// cancelable once done/failed).
func isStabilizerWizardScreen(s screen) bool {
	switch s {
	case screenStabilizerWarning, screenStabilizerZone, screenStabilizerNatsPassword,
		screenStabilizerNebula, screenStabilizerConfirm:
		return true
	default:
		return false
	}
}

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

	// screenOSUpdate's streaming step-runner state for osUpdateSteps
	// (os_update.go) - same shape as updateStepIdx/Logs/Done/Failed/Ch
	// above, just its own fields since, unlike ruddervirt-setup's own
	// self-update, applying an OS update doesn't replace/re-exec this
	// process, so it returns to screenUpdateVersions on Esc instead of
	// tea.Quit-ing.
	osUpdateStepIdx int
	osUpdateLogs    []string
	osUpdateDone    bool
	osUpdateFailed  bool
	osUpdateCh      chan tea.Msg

	// Forced admin-password-change flow, gating entry into "configure" -
	// see checkPasswordChangedCmd/password.go.
	passwordNewInput     textinput.Model
	passwordConfirmInput textinput.Model
	passwordConfirmFocus bool
	passwordSaving       bool
	passwordError        string

	// Forced hostname-declaration flow, gating entry into "configure" (and
	// "update", and both Apply footers - see hostnameLocked/hostname.go)
	// just like the password fields above.
	hostnameInput  textinput.Model
	hostnameSaving bool
	hostnameError  string
	// hostnameChangeForUpdate records which flow forced screenHostnameChange
	// open, so the completion handlers (hostnameSetMsg/hostnameDeclaredMsg
	// in app_update.go) know where to resume once the hostname is declared:
	// false resumes the "configure" chain (password check, then Settings);
	// true skips straight to screenUpdateVersions, mirroring how "update"
	// already skips the password check entirely. Every entry point into
	// screenHostnameChange sets this explicitly, so a stale value from an
	// earlier visit can never leak into a later one.
	hostnameChangeForUpdate bool

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

	// Home screen's "System" summary (hoststats.go) - CPU/mem/disk/VM
	// counts, refreshed on the same cadence as serviceStatuses above.
	// prevCPUSample is the raw /proc/stat reading from the last fetch,
	// carried forward so the next one has a baseline to diff CPU% against
	// (see cpuPercentBetween) - zero until the first fetch completes, which
	// cpuPercentBetween treats as "no baseline yet" rather than a real 0%.
	hostStats          hostStats
	hostStatsUpdatedAt time.Time
	prevCPUSample      cpuSample

	// cachedK3sVersions/cachedAileronVersions hold the release tags
	// fetchK3sVersions/fetchAileronVersions found, for the Settings screen's
	// version fields to pick from - populated once, best-effort, via Init's
	// fetchK3sVersionsCmd/fetchAileronVersionsCmd.
	cachedK3sVersions     []string
	cachedAileronVersions []string

	// cachedSelfUpdateAvailable/cachedOSUpdateAvailable back the Update
	// screen's "available" icon (updateRowHasUpgrade, view.go) for the two
	// rows that aren't backed by a settingField (ruddervirt-setup itself,
	// and the OS) - populated once, best-effort, via Init's
	// checkSelfUpdateAvailableCmd (update.go)/checkOSUpdateAvailableCmd
	// (os_update.go), same shape as the two caches above.
	cachedSelfUpdateAvailable bool
	cachedOSUpdateAvailable   bool

	// cachedStabilizerDetected mirrors the two version caches above: whether
	// a "stabilizer" HelmChart is on the cluster, populated once via Init's
	// detectStabilizerCmd (aileron.go) - see its doc comment for why this
	// deliberately isn't refreshed periodically. Read by the Aileron
	// settingFields' locked funcs (config.go) instead of shelling out to
	// kubectl on every render - see stabilizerLocked/stabilizerChartPresent.
	// Explicitly re-fired once the "Adopt to ruddervirt.com" flow
	// (stabilizer.go) finishes, so this doesn't stay stale for the rest of
	// the process's life the way its own doc comment says it deliberately
	// otherwise does.
	cachedStabilizerDetected bool

	// "Adopt to ruddervirt.com" wizard (Settings -> Advanced -> new action
	// row, see settingsRow.isStabilizerAction) - collects the secrets
	// adopt.go/stabilizer.go need, then runs stabilizerSteps. Mirrors the
	// shape of the install*/installConfirm* fields above, just for this
	// flow's own screens instead of screenInstall/screenInstallConfirm.
	// NATS URL is fixed (defaultStabilizerNatsURL, stabilizer.go) and the
	// NATS username is always the zone name, so neither gets its own input
	// screen/field - only zone, the NATS password, and the Nebula config
	// path/URL are ever typed by the operator.
	stabilizerZoneInput         textinput.Model
	stabilizerNatsPasswordInput textinput.Model
	stabilizerNebulaInput       textinput.Model
	stabilizerNebulaResolving   bool
	// stabilizerNebulaContent holds the fetched-and-validated Nebula config
	// only until screenStabilizerConfirm launches stabilizerSteps, at which
	// point it's copied into pendingStabilizerNebulaConfig (stabilizer.go)
	// and cleared here - never persisted to Config.
	stabilizerNebulaContent string
	stabilizerError         string

	// stabilizerWillAdopt carries computeStabilizerPlanCmd's result
	// (stabilizer.go) into screenStabilizerConfirm's plan summary.
	stabilizerWillAdopt    bool
	stabilizerConfirmInput textinput.Model
	stabilizerConfirmError string

	stabilizerStepIdx int
	stabilizerLogs    []string
	stabilizerDone    bool
	stabilizerFailed  bool
	stabilizerCh      chan tea.Msg

	// Stabilizer settings (stabilizerSettingDefs, driven from
	// stabilizer-settings.yaml) - shown IN SITU as ordinary rows in the
	// main Settings table once cachedStabilizerDetected is true (see
	// settingsRows, view.go: settingsRow.stabilizerDef), not a separate
	// screen - browsing/picking/editing them reuses the exact same
	// settingsCursor/settingsPicking/settingsEditing/settingsInput/
	// settingsPickOptions machinery every Config-backed settingField row
	// already uses (app_update.go branches on row.stabilizerDef != nil at
	// the couple of points where the backing store actually differs: this
	// is live cluster state, not Config, so there's no settingField.get/set
	// to call).
	//
	// stabilizerSettingsState is nil until loadStabilizerSettingsStateCmd
	// (stabilizer_settings_tui.go) returns - fetched once
	// detectStabilizerCmd's result (stabilizerDetectedMsg) confirms
	// stabilizer is actually present, not unconditionally on every launch,
	// so a node that never adopted stabilizer never pays for the extra
	// kubectl round trip (or risks an unwanted sudo prompt) just from
	// opening Settings.
	stabilizerSettingsState *stabilizerSettingsState

	// stabilizerSettingsPending* hold one validated, not-yet-applied change
	// from the picker/free-text edit through screenStabilizerSettingsConfirm's
	// "yes" to stabilizerSettingsApplySteps - the one part of this that
	// still isn't fully in situ, since actually applying a change restarts
	// the whole stabilizer release and needs its own explicit warning +
	// real-time progress, the same way the overall Settings "Apply" already
	// gets its own confirm/progress screens instead of happening silently
	// inline.
	stabilizerSettingsPendingDef     stabilizerSettingDef
	stabilizerSettingsPendingValue   any
	stabilizerSettingsPendingCurrent any
	stabilizerSettingsConfirmInput   textinput.Model
	stabilizerSettingsConfirmError   string

	// screenStabilizerSettingsApply's streaming step-runner state - same
	// shape as the adopt wizard's stabilizerStepIdx/Logs/Done/Failed/Ch
	// (stabilizerSteps), just for stabilizerSettingsApplySteps
	// (stabilizer_settings_tui.go) instead, since it actually watches the
	// patch + rollout from inside the TUI rather than telling the operator
	// to run a kubectl command themselves (they have no shell to run it
	// in - the TUI holds the terminal).
	stabilizerSettingsApplyPipeline []installStep
	stabilizerSettingsApplyStepIdx  int
	stabilizerSettingsApplyLogs     []string
	stabilizerSettingsApplyDone     bool
	stabilizerSettingsApplyFailed   bool
	stabilizerSettingsApplyCh       chan tea.Msg

	// Guarded chart-version change (stabilizer_upgrade.go), reached from the
	// Update screen's "Aileron version" row once stabilizer is detected
	// (replacing that row's former hard "managed by stabilizer" lock - see
	// stabilizerLocked, config.go, and the versions.aileron special-casing
	// in app_update.go/view.go). A picker, same as every other
	// component-version row (m.updateVersionsPicking/PickOptions/PickCursor,
	// reused as-is) - options come from ruddervirt/aileron's GitHub releases
	// (stabilizerVersionPickerOptions, stabilizer_upgrade.go), which are kept
	// in strict lockstep with the stabilizer chart's own version by that
	// project's release CI, so there's no separate stabilizer release feed
	// to fetch.
	//
	// stabilizerVersionTarget/Patch/ClearedPins carry
	// planStabilizerVersionUpgrade's validated result from the picker
	// confirm into the confirm screen's summary and, on "yes", into
	// stabilizerVersionApplySteps - the patch is built once (at pick time),
	// not re-derived at confirm time, so what's shown is exactly what gets
	// applied.
	stabilizerVersionTarget       string
	stabilizerVersionPatch        []byte
	stabilizerVersionClearedPins  []string
	stabilizerVersionConfirmInput textinput.Model
	stabilizerVersionConfirmError string

	// screenStabilizerVersionApply's streaming step-runner state - same
	// shape as stabilizerSettingsApply*/stabilizerSteps above.
	stabilizerVersionApplyPipeline []installStep
	stabilizerVersionApplyStepIdx  int
	stabilizerVersionApplyLogs     []string
	stabilizerVersionApplyDone     bool
	stabilizerVersionApplyFailed   bool
	stabilizerVersionApplyCh       chan tea.Msg
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

	hostnameInput := textinput.New()

	stabilizerZoneInput := textinput.New()
	stabilizerNatsPasswordInput := textinput.New()
	stabilizerNatsPasswordInput.EchoMode = textinput.EchoPassword
	stabilizerNatsPasswordInput.EchoCharacter = '•'
	stabilizerNebulaInput := textinput.New()
	stabilizerConfirmInput := textinput.New()

	stabilizerSettingsConfirmInput := textinput.New()

	stabilizerVersionConfirmInput := textinput.New()

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
	//
	// The hostname check (cfg.System.HostnameDeclared) takes priority over
	// the password check - see hostname.go - so a fresh node walks the
	// operator through declaring a hostname first, then the password, then
	// finally into Settings. hostnameLocked() guards it too: the hostname
	// is only ever offered for change before install has proceeded (see its
	// doc comment) - once locked, there's nothing safe left to declare, so
	// this falls straight through to the password check instead.
	current := screenMenu
	switch {
	case !cfg.System.HostnameDeclared && hostnameIsDefault() && !hostnameLocked():
		current = screenHostnameChange
		hostnameInput.Focus()
	case !cfg.System.PasswordChanged:
		current = screenPasswordCheck
	}

	return model{
		current:                        current,
		settingsInput:                  settingsInput,
		installConfirmInput:            installConfirmInput,
		updateConfirmInput:             updateConfirmInput,
		passwordNewInput:               passwordNewInput,
		passwordConfirmInput:           passwordConfirmInput,
		hostnameInput:                  hostnameInput,
		stabilizerZoneInput:            stabilizerZoneInput,
		stabilizerNatsPasswordInput:    stabilizerNatsPasswordInput,
		stabilizerNebulaInput:          stabilizerNebulaInput,
		stabilizerConfirmInput:         stabilizerConfirmInput,
		stabilizerSettingsConfirmInput: stabilizerSettingsConfirmInput,
		stabilizerVersionConfirmInput:  stabilizerVersionConfirmInput,
		cfg:                            cfg,
		// Sane fallback until the first tea.WindowSizeMsg arrives.
		termWidth:  80,
		termHeight: 24,
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, fetchK3sVersionsCmd(), fetchAileronVersionsCmd(), detectStabilizerCmd(), fetchServiceStatusesCmd(m.cfg), fetchHostStatsCmd(m.cfg, m.prevCPUSample), tickServiceStatusCmd(), tickServiceStatusRenderCmd(), checkSelfUpdateAvailableCmd(), checkOSUpdateAvailableCmd()}
	if m.current == screenPasswordCheck {
		// initialModel() starts here on first boot (see its comment) -
		// same command the menu's "configure" selection fires manually.
		cmds = append(cmds, checkPasswordChangedCmd())
	}
	if !m.cfg.System.HostnameDeclared && (!hostnameIsDefault() || hostnameLocked()) {
		// Checked directly against the live hostname/lock state rather than
		// m.current == screenHostnameChange: main.go's boot-loop override
		// can force m.current back to screenMenu on a skipped/still-default
		// hostname (same as it does for screenPasswordCheck), and that must
		// NOT be mistaken for "already customized" here. Only fire this
		// when there's nothing left to safely declare - the hostname is
		// already non-default (changed outside this flow) or install has
		// already proceeded (hostnameLocked, see hostname.go) - to record
		// it silently instead of forcing the operator through a screen that
		// either has nothing to do or must never let them touch it again.
		// Same reasoning as passwordCheckMsg's "already changed outside
		// this flow" branch in password.go.
		cmds = append(cmds, finalizeHostnameDeclaredCmd(m.cfg))
	}
	return tea.Batch(cmds...)
}
