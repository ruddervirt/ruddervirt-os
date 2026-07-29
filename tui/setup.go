package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

type screen int

const (
	screenMenu screen = iota
	screenResult
	screenInstall
	screenSettings
	screenInstallPlanning
	screenInstallConfirm
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

	installStepIdx   int
	installLogs      []string
	installDone      bool
	installFailed    bool
	installCh        chan tea.Msg
	installPlanLines []string

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
}

// settingsChromeLines is the number of lines the Settings screen spends on
// its header, footer/help text, scroll indicators, and table borders/header
// row - i.e. everything around the field rows themselves. Subtracted from
// the terminal height to decide how many fields can be shown at once.
const settingsChromeLines = 16

// networkSetupLabel is the toggle row that expands/collapses the local
// physical network fields (interface, addressing, and static IP details)
// nested beneath it.
func networkSetupLabel(expanded bool) string {
	if expanded {
		return "▾ Local physical network setup"
	}
	return "▸ Local physical network setup"
}

// advancedSettingsLabel is the toggle row that expands/collapses the
// advanced (rarely-changed) fields nested beneath it.
func advancedSettingsLabel(expanded bool) string {
	if expanded {
		return "▾ Advanced settings"
	}
	return "▸ Advanced settings"
}

// settingsRow is one row in the Settings table: either a real settingField
// (nested, i.e. indented, if it belongs to a collapsible group) or one of
// the synthetic toggle/action rows.
type settingsRow struct {
	field           settingField
	isNetworkToggle bool
	isToggle        bool
	isApply         bool
	nested          bool
}

// settingsRows lays out the Settings table in display order:
//
//  1. The "Apply" row, always first - "configure"'s equivalent of the old
//     standalone "install" menu item. It sits above the rest of the fields
//     so it's always visible right away instead of buried at the bottom,
//     behind however many fields happen to be expanded.
//  2. The "Local physical network setup" toggle row (where
//     interface_name/addressing used to sit directly) - expanding it
//     reveals the interface and addressing fields, plus (only when
//     addressing is static) the static IP/prefix/gateway/DNS fields, all
//     nested one level.
//  3. Any remaining plain fields (automatic updates, k3s version, ...).
//  4. The "Advanced settings" toggle row, then its fields when expanded.
//
// Every toggle/action row's own position is fixed regardless of its
// group's expand state - only what's nested beneath it grows or shrinks -
// so toggling one never shifts the cursor onto a different field.
func (m model) settingsRows() []settingsRow {
	var plain []settingsRow
	var network []settingsRow
	var advanced []settingField

	for _, f := range settingFields {
		switch {
		case f.advanced:
			advanced = append(advanced, f)
		case f.networkSetup:
			network = append(network, settingsRow{field: f, nested: true})
		case f.staticOnly:
			if m.cfg.Network.Addressing == "static" {
				network = append(network, settingsRow{field: f, nested: true})
			}
		default:
			plain = append(plain, settingsRow{field: f})
		}
	}

	rows := []settingsRow{{isApply: true}, {isNetworkToggle: true}}
	if m.settingsShowNetwork {
		rows = append(rows, network...)
	}
	rows = append(rows, plain...)
	rows = append(rows, settingsRow{isToggle: true})
	if m.settingsShowAdvanced {
		for _, f := range advanced {
			rows = append(rows, settingsRow{field: f, nested: true})
		}
	}
	return rows
}

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

// fitCell truncates s with an ellipsis if it's longer than width, or pads
// it with spaces to exactly width otherwise, keeping the Settings table's
// columns aligned regardless of content length.
func fitCell(s string, width int) string {
	w := runewidth.StringWidth(s)
	if w > width {
		return runewidth.Truncate(s, width, "…")
	}
	return s + strings.Repeat(" ", width-w)
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

var menuOptions = map[string]string{
	"1": "configure",
	"2": "k9s",
	"3": "shell",
	"4": "update",
	"5": "logout",
}

// saveSettingCmd persists cfg after a settings-field edit - nothing more.
// Settings only checks and records intent; install is the one place that
// ever actually applies it to the running system (see the "Applying
// settings" install step below). Runs as a tea.Cmd (its own goroutine)
// since saveConfig shells out via sudo and would otherwise block the UI
// loop.
func saveSettingCmd(cfg Config) tea.Cmd {
	return func() tea.Msg {
		return settingsSavedMsg{err: saveConfig(cfg, configPath)}
	}
}

const k3sUnitContent = `[Unit]
Description=Run K3s
Wants=network-online.target
After=network-online.target

[Service]
Type=notify
EnvironmentFile=-/etc/default/%N
EnvironmentFile=-/etc/sysconfig/%N
EnvironmentFile=-/etc/systemd/system/%N.env
KillMode=process
Delegate=yes
LimitNOFILE=1048576
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
TimeoutStartSec=0
Restart=always
RestartSec=5s
ExecStartPre=/bin/mkdir -p /etc/rancher/k3s
ExecStartPre=-/sbin/modprobe br_netfilter
ExecStartPre=-/sbin/modprobe overlay
ExecStartPre=-/sbin/modprobe iscsi_tcp
ExecStartPre=-/sbin/modprobe dm_crypt
ExecStartPre=-/sbin/modprobe nfs
ExecStartPre=-/sbin/modprobe wireguard
ExecStartPre=-/sbin/modprobe tun
ExecStartPre=-/sbin/modprobe geneve
ExecStartPre=-/sbin/modprobe openvswitch
ExecStartPre=-/sbin/modprobe ip_tables
ExecStartPre=-/sbin/modprobe iptable_nat
ExecStart=/bin/bash -c '/usr/local/bin/k3s server'

[Install]
WantedBy=multi-user.target
`

type installStep struct {
	label string
	run   func(cfg Config, ch chan<- tea.Msg)
	// plan, if non-nil, returns a one-line read-only preview of what run
	// will actually do for cfg - e.g. "skip - k3s v1.34.5+k3s1 already
	// installed" vs "will download k3s v1.34.5+k3s1". Must not mutate
	// state, shell out via runPrivileged/sudo, or assume a live k3s API
	// exists - this runs before the operator has even typed "yes" to
	// confirm Apply, possibly on a node where nothing has been installed
	// yet. nil means there's no cheap way to predict skip-vs-do (or the
	// step is already cheap/idempotent regardless) - computeInstallPlanCmd
	// falls back to a generic "will run" line in that case.
	plan func(cfg Config) string
}

// downloadAndUntar downloads a .tar.gz from url and extracts only the
// etcdctl binary into destDir
func downloadAndUntar(url, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		// /opt is a symlink on FCOS. MkdirAll chokes on it, so read
		// where it points and create the real path directly.
		parent := filepath.Dir(destDir)
		link, readErr := os.Readlink(parent)
		if readErr != nil {
			return err
		}
		// link may be relative (e.g. "var/opt"), make it absolute
		if !filepath.IsAbs(link) {
			link = filepath.Join("/", link)
		}
		realDest := filepath.Join(link, filepath.Base(destDir))
		if mkErr := os.MkdirAll(realDest, 0755); mkErr != nil {
			return mkErr
		}
		destDir = realDest
	}

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if !strings.HasSuffix(header.Name, "etcdctl") {
			continue
		}

		outPath := filepath.Join(destDir, "etcdctl")
		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		io.Copy(f, tarReader)
		f.Close()
		os.Chmod(outPath, 0755)
		break
	}
	return nil
}

func runPrivileged(name string, args ...string) *exec.Cmd {
	if os.Getuid() != 0 {
		return exec.Command("sudo", append([]string{name}, args...)...)
	}
	return exec.Command(name, args...)
}

// runStreamed runs a privileged command and sends each output line to ch,
// returning the command's final error (if any) instead of a stepDoneMsg, so
// composite steps can chain several of these before reporting done/failed.
func runStreamed(ch chan<- tea.Msg, name string, args ...string) error {
	cmd := runPrivileged(name, args...)
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return err
	}
	pw.Close()
	scanner := bufio.NewScanner(pr)
	for scanner.Scan() {
		ch <- stepOutputMsg(scanner.Text())
	}
	pr.Close()
	return cmd.Wait()
}

// installPackageNames is the list of rpm-ostree layered packages required
// on top of the base FCOS image - checked by both "Installing packages"
// and its plan preview, so the two can never drift.
var installPackageNames = []string{"btop", "mdadm", "iperf3", "k3s-selinux", "lvm2"}

// missingPackages returns which of installPackageNames (plus, if k9s isn't
// already on PATH and its rpm was staged at /var/tmp/k9s.rpm) still need
// installing.
func missingPackages() []string {
	var missing []string
	for _, p := range installPackageNames {
		if err := runPrivileged("/usr/bin/rpm", "-q", p).Run(); err != nil {
			missing = append(missing, p)
		}
	}
	if _, err := exec.LookPath("k9s"); err != nil {
		if _, statErr := os.Stat("/var/tmp/k9s.rpm"); statErr == nil {
			missing = append(missing, "/var/tmp/k9s.rpm")
		}
	}
	return missing
}

func planInstallPackages(cfg Config) string {
	missing := missingPackages()
	if len(missing) == 0 {
		return "skip - all packages already installed"
	}
	return fmt.Sprintf("will install: %s", strings.Join(missing, ", "))
}

const etcdctlPath = "/opt/bin/etcdctl"
const etcdVersion = "v3.5.21"

func etcdctlInstalled() bool {
	_, err := os.Stat(etcdctlPath)
	return err == nil
}

func planInstallEtcdctl(cfg Config) string {
	if etcdctlInstalled() {
		return "skip - etcdctl already installed"
	}
	return fmt.Sprintf("will download etcdctl %s", etcdVersion)
}

var installSteps = []installStep{
	{
		// Applies every setting that isn't already applied simply by
		// existing in installSteps' own args (k3s version, pod/service
		// CIDR, etc. are handled by later steps that just read cfg
		// directly) - this is specifically for settings whose "applying"
		// means calling out to another system component (nmcli, Zincati).
		label: "Applying settings",
		run: func(cfg Config, ch chan<- tea.Msg) {
			const label = "Applying settings"

			ch <- stepOutputMsg(fmt.Sprintf("Applying %s addressing on %s...", cfg.Network.Addressing, cfg.Network.InterfaceName))
			if err := applyNetworkConfig(cfg.Network); err != nil {
				ch <- stepDoneMsg{label: label, err: err}
				return
			}
			status := "off"
			if cfg.System.AutoUpdate {
				status = "on"
			}
			ch <- stepOutputMsg(fmt.Sprintf("Setting automatic updates: %s", status))
			if err := writeZincatiConfig(cfg.System.AutoUpdate, zincatiConfigPath); err != nil {
				ch <- stepDoneMsg{label: label, err: err}
				return
			}

			ch <- stepDoneMsg{label: label}
		},
	},
	{
		label: "Installing packages",
		run: func(cfg Config, ch chan<- tea.Msg) {
			label := "Installing packages"
			missing := missingPackages()
			if len(missing) == 0 {
				ch <- stepOutputMsg("All packages already installed")
				ch <- stepDoneMsg{label: label}
				return
			}
			ch <- stepOutputMsg(fmt.Sprintf("Installing: %s", strings.Join(missing, ", ")))
			args := append([]string{"install", "--apply-live", "--allow-inactive", "--assumeyes"}, missing...)
			ch <- stepDoneMsg{label: label, err: runStreamed(ch, "/usr/bin/rpm-ostree", args...)}
		},
		plan: planInstallPackages,
	},
	{
		label: "Installing etcdctl",
		run: func(cfg Config, ch chan<- tea.Msg) {
			label := "Installing etcdctl"
			if etcdctlInstalled() {
				ch <- stepOutputMsg("etcdctl already installed")
				ch <- stepDoneMsg{label: label}
				return
			}
			ch <- stepOutputMsg(fmt.Sprintf("Downloading etcdctl %s...", etcdVersion))
			url := fmt.Sprintf(
				"https://github.com/etcd-io/etcd/releases/download/%s/etcd-%s-linux-amd64.tar.gz",
				etcdVersion, etcdVersion,
			)
			// download to /tmp first, then move to /opt/bin to avoid permission issues
			if err := downloadAndUntar(url, "/tmp"); err != nil {
				ch <- stepDoneMsg{label: label, err: err}
				return
			}

			// use sudo to create /var/opt/bin
			if out, err := runPrivileged("/usr/bin/mkdir", "-p", "/var/opt/bin").CombinedOutput(); err != nil {
				ch <- stepDoneMsg{label: label, err: fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)}
				return
			}

			// move binary into place with sudo
			if out, err := runPrivileged("/usr/bin/mv", "/tmp/etcdctl", "/opt/bin/etcdctl").CombinedOutput(); err != nil {
				ch <- stepDoneMsg{label: label, err: fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)}
				return
			}

			ch <- stepOutputMsg("etcdctl installed successfully")
			ch <- stepDoneMsg{label: label}
		},
		plan: planInstallEtcdctl,
	},
	{
		label: "Installing k3s",
		run:   installK3sStep,
		plan:  planK3sInstall,
	},
	{
		label: "Downloading KubeVirt/CDI manifests",
		run:   downloadKubeVirtCDIManifestsStep,
		plan:  planKubeVirtCDIDownload,
	},
	{
		label: "Rendering k3s config",
		run:   renderK3sConfigStep,
	},
	{
		label: "Writing k3s systemd unit",
		run: func(cfg Config, ch chan<- tea.Msg) {
			label := "Writing k3s systemd unit"
			const unitPath = "/etc/systemd/system/k3s.service"
			ch <- stepOutputMsg(fmt.Sprintf("Writing %s...", unitPath))

			// write to tmp first to avoid permission issues
			if err := os.WriteFile("/tmp/k3s.service", []byte(k3sUnitContent), 0644); err != nil {
				ch <- stepDoneMsg{label: label, err: err}
				return
			}

			// move to /usr/bin with sudo
			if out, err := runPrivileged("/usr/bin/mv", "/tmp/k3s.service", unitPath).CombinedOutput(); err != nil {
				ch <- stepDoneMsg{label: label, err: fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)}
				return
			}

			ch <- stepOutputMsg("Running systemctl daemon-reload...")
			out, err := runPrivileged("/usr/bin/systemctl", "daemon-reload").CombinedOutput()
			if s := strings.TrimSpace(string(out)); s != "" {
				ch <- stepOutputMsg(s)
			}
			ch <- stepDoneMsg{label: label, err: err}
		},
	},
	{
		label: "Enabling and starting k3s",
		run: func(cfg Config, ch chan<- tea.Msg) {
			const label = "Enabling and starting k3s"
			if err := runStreamed(ch, "/usr/bin/systemctl", "enable", "k3s.service"); err != nil {
				ch <- stepDoneMsg{label: label, err: err}
				return
			}
			// Re-running install/update should actually apply a changed
			// binary or config, not just no-op like `enable --now` would
			// on an already-running service - so restart if active, start
			// otherwise.
			verb := "start"
			if runPrivileged("/usr/bin/systemctl", "is-active", "--quiet", "k3s.service").Run() == nil {
				verb = "restart"
			}
			ch <- stepDoneMsg{label: label, err: runStreamed(ch, "/usr/bin/systemctl", verb, "k3s.service")}
		},
	},
	{
		label: "Applying kube-ovn",
		run:   applyKubeOvnStep,
		plan:  planApplyKubeOvn,
	},
	{
		label: "Preparing storage device",
		run:   prepareStorageStep,
		plan:  planStorageDevice,
	},
	{
		label: "Applying manifests",
		run:   prepareK3sStep,
		plan:  planApplyManifests,
	},
}

func launchStep(step installStep, cfg Config) (chan tea.Msg, tea.Cmd) {
	ch := make(chan tea.Msg, 100)
	go step.run(cfg, ch)
	return ch, func() tea.Msg { return <-ch }
}

// computeInstallPlanCmd previews installSteps against cfg without changing
// any system state - runs as its own goroutine (tea.Cmd), same pattern as
// checkForUpdateCmd, so a slow individual check can't block the UI thread
// while "Computing install plan..." is shown.
func computeInstallPlanCmd(cfg Config) tea.Cmd {
	return func() tea.Msg {
		lines := make([]string, len(installSteps))
		for i, step := range installSteps {
			if step.plan != nil {
				lines[i] = step.plan(cfg)
			} else {
				lines[i] = "will run"
			}
		}
		return installPlanMsg{lines: lines}
	}
}

func readFromCh(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
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

	return model{
		current:              screenMenu,
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
	return tea.Batch(textinput.Blink, fetchK3sVersionsCmd(), fetchAileronVersionsCmd())
}

// k3sVersionsFetchedMsg carries fetchK3sVersions' result back into Update -
// run as a tea.Cmd (not synchronously in initialModel) so a slow or
// unreachable network doesn't delay the TUI's first paint.
type k3sVersionsFetchedMsg struct {
	versions []string
}

func fetchK3sVersionsCmd() tea.Cmd {
	return func() tea.Msg {
		versions, _ := fetchK3sVersions() // best-effort - cycling just no-ops if this fails
		return k3sVersionsFetchedMsg{versions: versions}
	}
}

// aileronVersionsFetchedMsg carries fetchAileronVersions' result back into
// Update - same reasoning as k3sVersionsFetchedMsg above.
type aileronVersionsFetchedMsg struct {
	versions []string
}

func fetchAileronVersionsCmd() tea.Cmd {
	return func() tea.Msg {
		versions, _ := fetchAileronVersions() // best-effort - cycling just no-ops if this fails
		return aileronVersionsFetchedMsg{versions: versions}
	}
}

func resolveInput(input string) (string, bool) {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "install" {
		// "install" is a historical alias only - applying settings (the
		// old standalone "install" menu item) now lives inside the
		// "configure" flow's Apply row. "update" is NOT aliased here:
		// it's now its own real menu item (self-updating ruddervirt-setup
		// from the latest GitHub release), so it falls through to the
		// generic label match below instead.
		input = "configure"
	}
	if label, ok := menuOptions[input]; ok {
		return label, true
	}
	for _, label := range menuOptions {
		if input == label {
			return label, true
		}
	}
	return "", false
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		m.settingsScroll = clampScroll(m.settingsScroll, m.settingsCursor, m.settingsVisibleRows())
		return m, nil

	case k3sVersionsFetchedMsg:
		cachedK3sVersions = msg.versions
		return m, nil

	case aileronVersionsFetchedMsg:
		cachedAileronVersions = msg.versions
		return m, nil

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
			return m, nil

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
					options := field.options(&m.cfg)
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

func (m model) View() string {
	switch m.current {

	case screenInstallPlanning:
		return "\nComputing install plan...\n"

	case screenInstallConfirm:
		s := "\nConfirm Install\n\n"
		s += "This will restart k3s, causing a brief interruption to the\n"
		s += "Kubernetes API and any running workloads. The full process can\n"
		s += "take 30+ minutes, mostly waiting for storage to become ready.\n"
		s += "\nPlan:\n"
		labelWidth := 0
		for _, step := range installSteps {
			if l := runewidth.StringWidth(step.label); l > labelWidth {
				labelWidth = l
			}
		}
		for i, step := range installSteps {
			line := "will run"
			if i < len(m.installPlanLines) && m.installPlanLines[i] != "" {
				line = m.installPlanLines[i]
			}
			s += fmt.Sprintf("  %s  %s\n", fitCell(step.label, labelWidth), line)
		}
		s += fmt.Sprintf("\nType \"yes\" to proceed, or Esc to cancel:\n  %s\n", m.installConfirmInput.View())
		if m.installConfirmError != "" {
			s += fmt.Sprintf("\n%s\n", m.installConfirmError)
		}
		return s

	case screenInstall:
		s := "\nInstalling RudderVirt...\n\n"
		visible := m.installVisibleLogLines()
		lines := m.installLogs
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, line := range lines {
			s += line + "\n"
		}
		if m.installDone {
			s += "\nInstall complete. Press Esc to return to menu.\n"
		} else if m.installFailed {
			s += "\nInstall failed. Press Esc to return to menu.\n"
		} else if m.installStepIdx < len(installSteps) {
			s += fmt.Sprintf("\nRunning: %s...\n", installSteps[m.installStepIdx].label)
		}
		return s

	case screenResult:
		return fmt.Sprintf("\n%s\n\nPress Esc to go back, Ctrl+C to quit.\n", m.result)

	case screenUpdateChecking:
		return "\nChecking for updates...\n"

	case screenPasswordCheck:
		s := "\nChecking admin account credentials...\n"
		if m.passwordError != "" {
			s += fmt.Sprintf("\nError: %s\n\nPress Esc to go back, Ctrl+C to quit.\n", m.passwordError)
		}
		return s

	case screenPasswordChange:
		s := "\nChange Admin Password\n\n"
		s += "This node is still using the well-known default password. Set a\n"
		s += "new admin password before continuing to configure.\n\n"
		if !m.passwordConfirmFocus {
			s += fmt.Sprintf("New password:      %s\n", m.passwordNewInput.View())
			s += "Confirm password:\n"
		} else {
			s += fmt.Sprintf("New password:      %s\n", strings.Repeat("•", len(m.passwordNewInput.Value())))
			s += fmt.Sprintf("Confirm password:  %s\n", m.passwordConfirmInput.View())
		}
		if m.passwordSaving {
			s += "\nSaving...\n"
		} else if m.passwordError != "" {
			s += fmt.Sprintf("\nError: %s\n", m.passwordError)
		}
		s += "\nEnter to confirm each field, Esc to cancel.\n"
		return s

	case screenUpdateConfirm:
		s := "\nUpdate ruddervirt-setup\n\n"
		s += fmt.Sprintf("Current version: %s\nLatest version:  %s\n", version, m.updateLatestVersion)
		s += "\nThis will download and verify (SHA256) the new binary, then\n"
		s += "replace /usr/local/bin/ruddervirt-setup. The menu will restart\n"
		s += "on the new version afterward.\n"
		s += fmt.Sprintf("\nType \"yes\" to proceed, or Esc to cancel:\n  %s\n", m.updateConfirmInput.View())
		if m.updateConfirmError != "" {
			s += fmt.Sprintf("\n%s\n", m.updateConfirmError)
		}
		return s

	case screenUpdate:
		s := "\nUpdating ruddervirt-setup...\n\n"
		visible := m.installVisibleLogLines()
		lines := m.updateLogs
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, line := range lines {
			s += line + "\n"
		}
		if m.updateDone {
			s += "\nUpdate complete. Restarting into the new version...\n"
		} else if m.updateFailed {
			s += "\nUpdate failed. Press Esc to return to menu.\n"
		} else if m.updateStepIdx < len(updateSteps) {
			s += fmt.Sprintf("\nRunning: %s...\n", updateSteps[m.updateStepIdx].label)
		}
		return s

	case screenSettings:
		if m.settingsPicking {
			field := m.settingsRows()[m.settingsCursor].field
			options := m.settingsPickOptions

			width := runewidth.StringWidth(field.label) + 4
			for _, o := range options {
				if l := runewidth.StringWidth(o) + 4; l > width {
					width = l
				}
			}
			termWidth := m.termWidth
			if termWidth <= 0 {
				termWidth = 80
			}
			if width > termWidth-2 {
				width = termWidth - 2
			}
			if width < 20 {
				width = 20
			}

			topBorder := "┌" + strings.Repeat("─", width) + "┐"
			bottomBorder := "└" + strings.Repeat("─", width) + "┘"

			s := fmt.Sprintf("\nSettings\n\nSelect %s:\n\n", field.label)

			visible := m.settingsVisibleRows()
			start := m.settingsPickScroll
			end := start + visible
			if end > len(options) {
				end = len(options)
			}
			if start > end {
				start = end
			}
			if start > 0 {
				s += fmt.Sprintf("  ↑ %d more above\n", start)
			}

			s += topBorder + "\n"
			for i := start; i < end; i++ {
				cursor := "  "
				if i == m.settingsPickCursor {
					cursor = "> "
				}
				s += fmt.Sprintf("│%s%s│\n", cursor, fitCell(options[i], width-2))
			}
			s += bottomBorder + "\n"
			if end < len(options) {
				s += fmt.Sprintf("  ↓ %d more below\n", len(options)-end)
			}

			if m.settingsError != "" {
				s += fmt.Sprintf("\nError: %s\n", m.settingsError)
			}
			s += "\nEnter to select, Esc to cancel.\n"
			return s
		}

		rows := m.settingsRows()
		total := len(rows)

		rowLabel := func(r settingsRow) string {
			switch {
			case r.isNetworkToggle:
				return networkSetupLabel(m.settingsShowNetwork)
			case r.isToggle:
				return advancedSettingsLabel(m.settingsShowAdvanced)
			case r.isApply:
				return "▶ Apply (install/re-apply)"
			case r.nested:
				return "  " + r.field.label
			default:
				return r.field.label
			}
		}
		rowValue := func(r settingsRow) string {
			if r.isNetworkToggle || r.isToggle || r.isApply {
				return ""
			}
			if r.field.locked != nil {
				if locked, reason := r.field.locked(&m.cfg); locked {
					return reason
				}
			}
			return r.field.get(&m.cfg)
		}

		labelWidth := len("Setting")
		valueWidth := len("Value")
		for _, r := range rows {
			if l := runewidth.StringWidth(rowLabel(r)); l > labelWidth {
				labelWidth = l
			}
			if l := runewidth.StringWidth(rowValue(r)); l > valueWidth {
				valueWidth = l
			}
		}

		termWidth := m.termWidth
		if termWidth <= 0 {
			termWidth = 80
		}
		const tableOverhead = 11 // "│" + cursor(3) + "│ " + " │ " + " │"
		for labelWidth+valueWidth+tableOverhead > termWidth && valueWidth > 12 {
			valueWidth--
		}
		for labelWidth+valueWidth+tableOverhead > termWidth && labelWidth > 20 {
			labelWidth--
		}

		topBorder := "┌───┬" + strings.Repeat("─", labelWidth+2) + "┬" + strings.Repeat("─", valueWidth+2) + "┐"
		sepBorder := "├───┼" + strings.Repeat("─", labelWidth+2) + "┼" + strings.Repeat("─", valueWidth+2) + "┤"
		bottomBorder := "└───┴" + strings.Repeat("─", labelWidth+2) + "┴" + strings.Repeat("─", valueWidth+2) + "┘"

		s := "\nSettings\n\n"

		visible := m.settingsVisibleRows()
		start := m.settingsScroll
		end := start + visible
		if end > total {
			end = total
		}
		if start > end {
			start = end
		}
		if start > 0 {
			s += fmt.Sprintf("  ↑ %d more above\n", start)
		}

		s += topBorder + "\n"
		s += fmt.Sprintf("│   │ %s │ %s │\n", fitCell("Setting", labelWidth), fitCell("Value", valueWidth))
		s += sepBorder + "\n"
		for i := start; i < end; i++ {
			cursor := "   "
			if i == m.settingsCursor {
				cursor = " > "
			}
			r := rows[i]
			s += fmt.Sprintf("│%s│ %s │ %s │\n", cursor, fitCell(rowLabel(r), labelWidth), fitCell(rowValue(r), valueWidth))
		}
		s += bottomBorder + "\n"

		if end < total {
			s += fmt.Sprintf("  ↓ %d more below\n", total-end)
		}

		if m.settingsEditing {
			s += fmt.Sprintf("\nEditing %s:\n  %s\n", rows[m.settingsCursor].field.label, m.settingsInput.View())
			if m.settingsError != "" {
				s += fmt.Sprintf("\nError: %s\n", m.settingsError)
			}
			s += "\nEnter to save, Esc to cancel.\n"
		} else {
			if m.settingsSaving {
				s += "\nSaving...\n"
			} else if m.settingsError != "" {
				s += fmt.Sprintf("\nError saving: %s\n", m.settingsError)
			}
			s += "\nUp/Down to select, Enter to edit/choose, Esc to go back.\n"
		}
		return s

	default:
		s := fmt.Sprintf("\nRudderVirt Setup (%s)\n\n", version)
		s += "  1. configure\n"
		s += "  2. k9s\n"
		s += "  3. shell\n"
		s += "  4. update\n"
		s += "  5. logout\n"
		s += fmt.Sprintf("\n> %s_\n\n", m.input)
		s += "Press ctrl+c to quit.\n"
		return s
	}
}

func main() {
	// RUDDERVIRT_SHELL tells ruddervirt-shell.sh not to re-launch this menu
	// when a login shell starts inside one of the runShell() sessions below.
	os.Setenv("RUDDERVIRT_SHELL", "1")

	for {
		p := tea.NewProgram(initialModel())
		m, err := p.Run()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		switch fm := m.(model); {
		case fm.shellMode:
			runShell()
		case fm.k9sMode:
			runK9s()
		case fm.updateInstalled:
			// main() ignores os.Args entirely, so re-execing with the same
			// argv is safe - this hands control straight to the new binary
			// instead of making the admin log back in to pick it up.
			exe := "/usr/local/bin/ruddervirt-setup"
			fmt.Println("\nUpdate installed. Restarting into the new version...")
			if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
				fmt.Printf("Could not restart %s: %v\n", exe, err)
				fmt.Println("The update was installed; it will take effect next login.")
			}
		default:
			return
		}
	}
}

// runShell drops into an interactive bash login shell as a child process
// (not syscall.Exec) so that exiting it - "exit" or Ctrl+D - returns control
// to main, which loops back into the menu instead of ending the session.
func runShell() {
	fmt.Println("\nYou are exiting to a bash shell. Type \"exit\" or press ctrl+d to return to the menu.")
	cmd := exec.Command("/bin/bash", "-l")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// bash exiting non-zero (e.g. a failed last command) isn't an error for
	// us - just return to the menu either way.
	_ = cmd.Run()
}

// runK9s launches k9s as a child process (same pattern as runShell), with
// an explicit --kubeconfig since k9s - unlike `k3s kubectl` - has no
// built-in default pointing at /etc/rancher/k3s/k3s.yaml, and setting
// KUBECONFIG here wouldn't survive runPrivileged's sudo wrapping anyway.
func runK9s() {
	fmt.Println("\nLaunching k9s. Press ctrl+c or type \":quit\" to return to the menu.")
	cmd := runPrivileged("k9s", "--kubeconfig", "/etc/rancher/k3s/k3s.yaml")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}
