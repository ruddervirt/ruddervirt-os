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
)

type screen int

const (
	screenMenu screen = iota
	screenResult
	screenNetwork
	screenInstall
)

type stepOutputMsg string
type stepDoneMsg struct {
	label string
	err   error
}

type model struct {
	input         string
	result        string
	current       screen
	networkInputs [2]textinput.Model
	networkFocus  int
	resultSource  string

	installStepIdx int
	installLogs    []string
	installDone    bool
	installFailed  bool
	installCh      chan tea.Msg

	shellMode bool
}

var menuOptions = map[string]string{
	"1": "install",
	"2": "update",
	"3": "network",
	"4": "purge",
	"5": "shell",
	"6": "logout",
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
	run   func(ch chan<- tea.Msg)
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

// streamExec runs a single command and sends each output line to ch,
// followed by a stepDoneMsg when the command finishes.
func streamExec(label, name string, args ...string) func(ch chan<- tea.Msg) {
	return func(ch chan<- tea.Msg) {
		cmd := runPrivileged(name, args...)
		pr, pw, err := os.Pipe()
		if err != nil {
			ch <- stepDoneMsg{label: label, err: err}
			return
		}
		cmd.Stdout = pw
		cmd.Stderr = pw
		if err := cmd.Start(); err != nil {
			pw.Close()
			pr.Close()
			ch <- stepDoneMsg{label: label, err: err}
			return
		}
		pw.Close()
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			ch <- stepOutputMsg(scanner.Text())
		}
		pr.Close()
		ch <- stepDoneMsg{label: label, err: cmd.Wait()}
	}
}

var installSteps = []installStep{
	{
		label: "Installing packages",
		run: func(ch chan<- tea.Msg) {
			label := "Installing packages"
			pkgs := []string{"btop", "mdadm", "iperf3", "k3s-selinux"}
			var missing []string
			for _, p := range pkgs {
				if err := runPrivileged("/usr/bin/rpm", "-q", p).Run(); err != nil {
					missing = append(missing, p)
				}
			}
			if _, err := exec.LookPath("k9s"); err != nil {
				if _, statErr := os.Stat("/var/tmp/k9s.rpm"); statErr == nil {
					missing = append(missing, "/var/tmp/k9s.rpm")
				}
			}
			if len(missing) == 0 {
				ch <- stepOutputMsg("All packages already installed")
				ch <- stepDoneMsg{label: label}
				return
			}
			ch <- stepOutputMsg(fmt.Sprintf("Installing: %s", strings.Join(missing, ", ")))
			args := append([]string{"install", "--apply-live", "--allow-inactive", "--assumeyes"}, missing...)
			cmd := runPrivileged("/usr/bin/rpm-ostree", args...)
			pr, pw, err := os.Pipe()
			if err != nil {
				ch <- stepDoneMsg{label: label, err: err}
				return
			}
			cmd.Stdout = pw
			cmd.Stderr = pw
			if err := cmd.Start(); err != nil {
				pw.Close()
				pr.Close()
				ch <- stepDoneMsg{label: label, err: err}
				return
			}
			pw.Close()
			scanner := bufio.NewScanner(pr)
			for scanner.Scan() {
				ch <- stepOutputMsg(scanner.Text())
			}
			pr.Close()
			ch <- stepDoneMsg{label: label, err: cmd.Wait()}
		},
	},
	{
		label: "Installing etcdctl",
		run: func(ch chan<- tea.Msg) {
			label := "Installing etcdctl"
			const etcdVersion = "v3.5.21"
			if _, err := os.Stat("/opt/bin/etcdctl"); err == nil {
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
	},
	{
		label: "Rendering k3s config",
		run:   streamExec("Rendering k3s config", "/usr/local/bin/render-k3s-config"),
	},
	{
		label: "Writing k3s systemd unit",
		run: func(ch chan<- tea.Msg) {
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
		run:   streamExec("Enabling and starting k3s", "/usr/bin/systemctl", "enable", "--now", "k3s.service"),
	},
	{
		label: "Applying manifests",
		run:   streamExec("Applying manifests", "/usr/local/bin/prepare-k3s"),
	},
}

func launchStep(step installStep) (chan tea.Msg, tea.Cmd) {
	ch := make(chan tea.Msg, 100)
	go step.run(ch)
	return ch, func() tea.Msg { return <-ch }
}

func readFromCh(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func initialModel() model {
	ifaceInput := textinput.New()
	ifaceInput.Placeholder = "e.g. eno1"
	ifaceInput.Focus()

	ipInput := textinput.New()
	ipInput.Placeholder = "e.g. 192.168.10.2"

	return model{
		current:       screenMenu,
		networkInputs: [2]textinput.Model{ifaceInput, ipInput},
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func resolveInput(input string) (string, bool) {
	input = strings.ToLower(strings.TrimSpace(input))
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

	case stepOutputMsg:
		m.installLogs = append(m.installLogs, string(msg))
		return m, readFromCh(m.installCh)

	case stepDoneMsg:
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
		ch, cmd := launchStep(installSteps[m.installStepIdx])
		m.installCh = ch
		return m, cmd

	case tea.KeyMsg:
		switch msg.Type {

		case tea.KeyCtrlC:
			return m, tea.Quit

		case tea.KeyEsc:
			if m.current == screenInstall && !m.installDone && !m.installFailed {
				return m, nil
			}
			m.current = screenMenu
			m.input = ""
			m.result = ""
			m.resultSource = ""
			m.networkFocus = 0
			m.networkInputs[0].SetValue("")
			m.networkInputs[1].SetValue("")
			m.networkInputs[0].Focus()
			m.networkInputs[1].Blur()
			m.installStepIdx = 0
			m.installLogs = nil
			m.installDone = false
			m.installFailed = false
			m.installCh = nil
			return m, nil

		case tea.KeyTab:
			if m.current == screenNetwork {
				m.networkFocus = (m.networkFocus + 1) % 2
				if m.networkFocus == 0 {
					m.networkInputs[0].Focus()
					m.networkInputs[1].Blur()
				} else {
					m.networkInputs[1].Focus()
					m.networkInputs[0].Blur()
				}
				return m, nil
			}

		case tea.KeyEnter:
			if m.current == screenMenu {
				if label, ok := resolveInput(m.input); ok {
					m.input = ""
					switch label {
					case "logout":
						return m, tea.Quit
					case "network":
						m.current = screenNetwork
						return m, nil
					case "install":
						m.current = screenInstall
						m.installStepIdx = 0
						m.installLogs = nil
						m.installDone = false
						m.installFailed = false
						ch, cmd := launchStep(installSteps[0])
						m.installCh = ch
						return m, cmd
					case "shell":
						m.shellMode = true
						return m, tea.Quit
					default:
						m.result = label
						m.current = screenResult
					}
				} else {
					m.input = ""
				}
			} else if m.current == screenNetwork {
				iface := m.networkInputs[0].Value()
				ip := m.networkInputs[1].Value()
				if iface == "" || ip == "" {
					return m, nil
				}
				m.result = fmt.Sprintf("network: interface=%s ip=%s", iface, ip)
				m.resultSource = "network"
				m.current = screenResult
			}
			return m, nil

		case tea.KeyBackspace:
			if m.current == screenMenu && len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
				return m, nil
			}
		}
	}

	if m.current == screenNetwork {
		var cmd tea.Cmd
		m.networkInputs[m.networkFocus], cmd = m.networkInputs[m.networkFocus].Update(msg)
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

	case screenInstall:
		s := "\nInstalling RudderVirt...\n\n"
		for _, line := range m.installLogs {
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

	case screenNetwork:
		s := "\nNetwork Configuration\n\n"
		s += fmt.Sprintf("  Interface name: %s\n", m.networkInputs[0].View())
		s += fmt.Sprintf("  IP address:     %s\n", m.networkInputs[1].View())
		s += "\nTab to switch fields, Enter to confirm, Esc to go back.\n"
		return s

	case screenResult:
		return fmt.Sprintf("\n%s\n\nPress Esc to go back, Ctrl+C to quit.\n", m.result)

	default:
		s := "\nRudderVirt Setup\n\n"
		s += "  1. install\n"
		s += "  2. update\n"
		s += "  3. network\n"
		s += "  4. purge\n"
		s += "  5. shell\n"
		s += "  6. logout\n"
		s += fmt.Sprintf("\n> %s_\n\n", m.input)
		s += "Press ctrl+c to quit.\n"
		return s
	}
}

func main() {
	p := tea.NewProgram(initialModel())
	m, err := p.Run()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// launch shell if shellMode = true, allow us to return to ruddervirt setup as well
	if m.(model).shellMode {
		os.Setenv("RUDDERVIRT_SHELL", "1")
		fmt.Println("\nYou are exiting to a bash shell.")
		fmt.Println("Press ctrl+d or type \"exit\" to quit and type \"ruddervirt-setup\" to return to the menu.\n")
		syscall.Exec("/bin/bash", []string{"bash", "-l"}, os.Environ())
	}
}
