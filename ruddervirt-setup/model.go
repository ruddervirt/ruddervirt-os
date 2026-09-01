// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/hostname"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/network"
	"ruddervirt-setup/internal/stabilizer/settings"
	"ruddervirt-setup/internal/status"
	"ruddervirt-setup/internal/tui/screens"
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
	screenOSUpdateConfirm
	screenPowerOptions
	screenPowerConfirm
	screenPowerApply
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
// ruddervirt.com" wizard's pre-execution screens, used by app.go's shared
// Esc-cancel handling. Excludes screenStabilizerAileronCheck/Planning (brief
// non-interactive checks, Esc blocked like screenInstallPlanning) and
// screenStabilizerAdopt (guarded separately, cancelable only once done/failed).
func isStabilizerWizardScreen(s screen) bool {
	switch s {
	case screenStabilizerWarning, screenStabilizerZone, screenStabilizerNatsPassword,
		screenStabilizerNebula, screenStabilizerConfirm:
		return true
	default:
		return false
	}
}

// stepOutputMsg/stepDoneMsg are aliases (not new types) for
// installsteps.StepOutputMsg/StepDoneMsg, so every existing package-main
// reference (msg.Label/msg.Err, app.go's `case stepDoneMsg:`) keeps working
// unchanged rather than duplicating a second, incompatible message shape.
type stepOutputMsg = installsteps.StepOutputMsg
type stepDoneMsg = installsteps.StepDoneMsg

// installPlanMsg carries computeInstallPlanCmd's result back into Update -
// one line per installSteps entry, same order/index.
type installPlanMsg struct {
	lines []string
}

type settingsSavedMsg struct {
	err error
}

type model struct {
	// menu is the main menu / result screen sub-model (screenMenu,
	// screenResult) - see screens.MenuModel's doc comment.
	menu    screens.MenuModel
	current screen

	// install is the install wizard's sub-model (screenInstall,
	// screenInstallPlanning, screenInstallConfirm) - see
	// screens.InstallModel's doc comment.
	install screens.InstallModel
	// installConfirmOrigin remembers which screen's Apply started
	// screenInstallPlanning/screenInstallConfirm (Settings' Apply and the
	// Update screen's Apply upgrades both funnel into this confirm flow), so
	// Esc knows which one to return to.
	installConfirmOrigin screen

	cfg config.Config
	// settings is the Settings screen's sub-model (screenSettings) - see
	// screens.SettingsModel's doc comment. settingsSaving/settingsError stay
	// here rather than on settings itself since screenUpdateVersions
	// (screens.UpdateModel) also reads/writes them - genuinely cross-group
	// state, not Settings-local.
	settings       screens.SettingsModel
	settingsSaving bool
	settingsError  string

	// update is the update-versions wizard's sub-model (screenUpdateVersions,
	// screenUpdateChecking, screenUpdateConfirm, screenUpdate) - see
	// screens.UpdateModel's doc comment.
	update screens.UpdateModel

	// osUpdate is the OS-update screen's sub-model (screenOSUpdate) - see
	// screens.OSUpdateModel's doc comment. Unlike update above (whose
	// pipeline replaces/re-execs this process on success), an OS update
	// doesn't replace this process, so it returns to screenUpdateVersions on
	// Esc instead of tea.Quit-ing.
	osUpdate screens.OSUpdateModel

	// power is the main menu's "power options" submenu's sub-model
	// (screenPowerOptions, screenPowerConfirm, screenPowerApply) - see
	// screens.PowerModel's doc comment. Like update above (and unlike
	// osUpdate), a successful run never returns to this process: the host
	// goes down with it.
	power screens.PowerModel
	// powerConfirmOrigin remembers which screen sent the operator into
	// screenPowerConfirm - normally screenPowerOptions (the submenu itself),
	// but screenOSUpdate's post-success "press r to reboot" shortcut sends
	// them there directly too (see the "r" key handling in app.go), so Esc
	// must cancel back to wherever they actually came from, same reasoning
	// as installConfirmOrigin above.
	powerConfirmOrigin screen

	// Forced admin-password-change flow, gating entry into "configure" - see
	// checkPasswordChangedCmd/password.go.
	password screens.PasswordModel

	// Forced hostname-declaration flow, gating entry into "configure" (and
	// "update", and both Apply footers - see hostnameLocked/hostname.go),
	// same as password above.
	hostname screens.HostnameModel
	// hostnameChangeForUpdate records which flow forced screenHostnameChange
	// open, so the completion handlers (screens.HostnameSetMsg/
	// hostnameDeclaredMsg in app.go) know where to resume: false resumes the
	// "configure" chain (password check, then Settings); true skips straight
	// to screenUpdateVersions, mirroring how "update" skips the password
	// check. Every entry point sets this explicitly so no stale value leaks
	// from an earlier visit.
	hostnameChangeForUpdate bool

	termWidth  int
	termHeight int

	shellMode bool
	k9sMode   bool

	// Home screen's "Services" summary - nil until fetchServiceStatusesCmd
	// (status_bridge.go) reports back, refreshed every
	// serviceStatusRefreshInterval while the menu is shown.
	// serviceStatusUpdatedAt tracks when that happened, for "updated Xs ago".
	serviceStatuses        []status.ServiceStatus
	serviceStatusUpdatedAt time.Time

	// Home screen's "System" summary (internal/status) - CPU/mem/disk/VM
	// counts, refreshed on the same cadence as serviceStatuses above.
	// prevCPUSample is the raw /proc/stat reading from the last fetch,
	// carried forward as the baseline for the next CPU% diff (see
	// internal/status's cpuPercentBetween) - zero until the first fetch
	// completes, which cpuPercentBetween treats as "no baseline" not 0%.
	hostStats          status.HostStats
	hostStatsUpdatedAt time.Time
	prevCPUSample      status.CPUSample

	// cachedK3sVersions/cachedAileronVersions hold the release tags
	// fetchK3sVersions/fetchAileronVersions found, for the Update screen's
	// version fields, populated once via Init's
	// fetchK3sVersionsCmd/fetchAileronVersionsCmd. kube-ovn/Multus need no
	// equivalent: their options come from a hand-curated allowlist
	// (supported-versions.yaml, supportedVersionsAtLeast), not a live fetch.
	cachedK3sVersions     []string
	cachedAileronVersions []string

	// cachedSelfUpdateAvailable/cachedOSUpdateAvailable back the Update
	// screen's "available" icon (updateRowHasUpgrade, view.go) for the two
	// rows not backed by a config.SettingField (ruddervirt-setup itself, and
	// the OS) - populated once via Init's
	// checkSelfUpdateAvailableCmd (update.go)/checkOSUpdateAvailableCmd
	// (os_update.go).
	cachedSelfUpdateAvailable bool
	cachedOSUpdateAvailable   bool

	// cachedStabilizerDetected: whether a "stabilizer" HelmChart is on the
	// cluster, populated once via Init's detectStabilizerCmd
	// (aileron_bridge.go, see its doc comment for why this isn't refreshed
	// periodically). Read by the Aileron config.SettingFields' locked funcs
	// (config.go) instead of shelling out to kubectl on every render - see
	// stabilizerLocked/stabilizerChartPresent. Re-fired once the "Adopt to
	// ruddervirt.com" flow (stabilizer.go) finishes, so it isn't stale for
	// the rest of the process's life.
	cachedStabilizerDetected bool

	// stabilizerAdopt is the "Adopt to ruddervirt.com" wizard's sub-model
	// (screenStabilizerAileronCheck through screenStabilizerAdopt, Settings
	// -> Advanced -> new action row, see
	// screens.SettingsRow.IsStabilizerAction) - see
	// screens.StabilizerAdoptModel's doc comment.
	stabilizerAdopt screens.StabilizerAdoptModel

	// Stabilizer settings (stabilizerSettingDefs, from
	// stabilizer-settings.yaml) - shown in situ as ordinary rows in the main
	// Settings table once cachedStabilizerDetected is true (see
	// screens.SettingsModel.Rows: SettingsRow.StabilizerDef), reusing the
	// same settings.Cursor/Picking/Editing/Input/PickOptions machinery every
	// config.Config-backed config.SettingField row uses (app.go branches on
	// row.StabilizerDef != nil where the backing store differs: live cluster
	// state, not config.Config, so no config.SettingField.get/set).
	//
	// stabilizerSettingsState is nil until loadStabilizerSettingsStateCmd
	// (stabilizer_settings_tui.go) returns - fetched only once
	// detectStabilizerCmd confirms stabilizer is present, so a node that
	// never adopted it never pays for the extra kubectl round trip (or risks
	// an unwanted sudo prompt) just from opening Settings. Read by both
	// screens.SettingsModel (in situ rows) and screens.UpdateModel (the
	// versions.aileron special-casing) - see SettingsViewParams/
	// UpdateViewParams - so it stays on the root Model, same convention as
	// cachedStabilizerDetected above.
	stabilizerSettingsState *settings.StabilizerSettingsState

	// stabilizerSettings is screenStabilizerSettingsConfirm/Apply's
	// sub-model - the single-setting-change confirm+apply flow reached from
	// an ordinary in-situ Settings row - see
	// screens.StabilizerSettingsModel's doc comment.
	stabilizerSettings screens.StabilizerSettingsModel

	// stabilizerVersion is screenStabilizerVersionConfirm/Apply's sub-model -
	// the guarded chart-version-change confirm+apply flow reached from the
	// Update screen's "Aileron version" row once stabilizer is detected
	// (replacing that row's former hard "managed by stabilizer" lock - see
	// stabilizerLocked, config.go, and the versions.aileron special-casing in
	// app.go) - see screens.StabilizerVersionModel's doc comment.
	stabilizerVersion screens.StabilizerVersionModel
}

func initialModel() model {
	settingsInput := textinput.New()

	// No placeholder text: with no color styling to mark placeholder vs. real
	// content, a "yes" hint could be mistaken for already-typed confirmation.
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

	powerConfirmInput := textinput.New()

	cfg, _ := config.LoadConfig(config.ConfigPath)
	if cfg.Network.InterfaceName == "" {
		// Pre-fill with the internet-facing interface so Settings shows a
		// sensible value instead of blank. Not persisted here - only saved if
		// the operator accepts/edits it, or at install time via
		// network.ResolveNetworkForInstall.
		if iface, err := network.DetectDefaultInterface(); err == nil {
			cfg.Network.InterfaceName = iface
		}
	}

	// cfg.System.PasswordChanged is false only between first boot and the
	// operator's first trip through "configure" (i.e. the password is still
	// server.bu's well-known default). Skip the home menu and land straight
	// on the password-check -> Settings flow instead of making the operator
	// find "configure" themselves.
	//
	// The hostname check takes priority - see hostname.go - so a fresh node
	// declares a hostname first, then the password, then Settings.
	// hostnameLocked() guards it too: once install has proceeded there's
	// nothing safe left to declare, so this falls through to the password
	// check instead.
	current := screenMenu
	switch {
	case !cfg.System.HostnameDeclared && hostname.HostnameIsDefault() && !hostnameLocked():
		current = screenHostnameChange
		hostnameInput.Focus()
	case !cfg.System.PasswordChanged:
		current = screenPasswordCheck
	}

	return model{
		current:  current,
		settings: screens.SettingsModel{Input: settingsInput},
		install:  screens.InstallModel{ConfirmInput: installConfirmInput},
		update:   screens.UpdateModel{ConfirmInput: updateConfirmInput},
		password: screens.PasswordModel{NewInput: passwordNewInput, ConfirmInput: passwordConfirmInput},
		hostname: screens.HostnameModel{Input: hostnameInput},
		stabilizerAdopt: screens.StabilizerAdoptModel{
			ZoneInput:         stabilizerZoneInput,
			NatsPasswordInput: stabilizerNatsPasswordInput,
			NebulaInput:       stabilizerNebulaInput,
			ConfirmInput:      stabilizerConfirmInput,
		},
		stabilizerSettings: screens.StabilizerSettingsModel{ConfirmInput: stabilizerSettingsConfirmInput},
		stabilizerVersion:  screens.StabilizerVersionModel{ConfirmInput: stabilizerVersionConfirmInput},
		power:              screens.PowerModel{ConfirmInput: powerConfirmInput},
		cfg:                cfg,
		// Fallback until the first tea.WindowSizeMsg arrives.
		termWidth:  80,
		termHeight: 24,
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, fetchK3sVersionsCmd(), fetchAileronVersionsCmd(), detectStabilizerCmd(), fetchServiceStatusesCmd(m.cfg), fetchHostStatsCmd(m.cfg, m.prevCPUSample), tickServiceStatusCmd(), tickServiceStatusRenderCmd(), checkSelfUpdateAvailableCmd(), checkOSUpdateAvailableCmd()}
	if m.current == screenPasswordCheck {
		// initialModel() starts here on first boot - same command the menu's
		// "configure" selection fires manually.
		cmds = append(cmds, checkPasswordChangedCmd())
	}
	if !m.cfg.System.HostnameDeclared && (!hostname.HostnameIsDefault() || hostnameLocked()) {
		// Checked against live hostname/lock state, not m.current ==
		// screenHostnameChange: main.go's boot-loop override can force
		// m.current back to screenMenu on a skipped/still-default hostname,
		// which must not be mistaken for "already customized" here. Only
		// fires when nothing's left to safely declare - hostname changed
		// outside this flow, or install already proceeded (hostnameLocked) -
		// to record it silently rather than force a screen with nothing to
		// do. Same reasoning as password.go's "already changed outside this
		// flow" branch.
		cmds = append(cmds, finalizeHostnameDeclaredCmd(m.cfg))
	}
	return tea.Batch(cmds...)
}
