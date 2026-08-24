// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

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
				ch <- stepDoneMsg{label: label, err: wrapCmdErr(out, err)}
				return
			}

			// move binary into place with sudo
			if out, err := runPrivileged("/usr/bin/mv", "/tmp/etcdctl", "/opt/bin/etcdctl").CombinedOutput(); err != nil {
				ch <- stepDoneMsg{label: label, err: wrapCmdErr(out, err)}
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
				ch <- stepDoneMsg{label: label, err: wrapCmdErr(out, err)}
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
	{
		label: "Waiting for all services to become ready",
		run:   waitForServicesReadyStep,
	},
}

// waitForServicesReadyStep is the last install step - every earlier step
// only waits for its own piece to be *applied* (e.g. aileron's helm-install
// Job reaching Completed just means its chart's resources got created, not
// that the resulting pods have actually rolled out), so without this,
// "Install complete" could land while the home screen's own Services
// summary (status.go's fetchServiceStatuses, the same check this step
// reuses) would still show several rows as "not ready". Blocks until every
// row it reports is ready, so a successful install always leaves the
// operator looking at a fully-ready cluster.
func waitForServicesReadyStep(cfg Config, ch chan<- tea.Msg) {
	const label = "Waiting for all services to become ready"
	ch <- stepOutputMsg(label + " (this can take a while)...")

	deadline := time.Now().Add(30 * time.Minute)
	for {
		statuses := fetchServiceStatuses(cfg)
		if allServicesReady(statuses) {
			ch <- stepDoneMsg{label: label}
			return
		}
		if time.Now().After(deadline) {
			ch <- stepDoneMsg{label: label, err: fmt.Errorf("timed out waiting for: %s", notReadySummary(statuses))}
			return
		}
		time.Sleep(5 * time.Second)
	}
}

// allServicesReady reports whether every row fetchServiceStatuses returned
// is in its healthy terminal state - "running" for k3s itself (see
// fetchServiceStatuses), "ready" (readyState) for everything else. An empty
// result (config not yet saved, or no usable sudo ticket - see
// fetchServiceStatuses) is never ready: there's nothing to confirm yet.
func allServicesReady(statuses []serviceStatus) bool {
	if len(statuses) == 0 {
		return false
	}
	for _, st := range statuses {
		if st.state != "running" && st.state != "ready" {
			return false
		}
	}
	return true
}

// notReadySummary formats whichever rows weren't ready when
// waitForServicesReadyStep gave up, for its timeout error message.
func notReadySummary(statuses []serviceStatus) string {
	var notReady []string
	for _, st := range statuses {
		if st.state != "running" && st.state != "ready" {
			notReady = append(notReady, fmt.Sprintf("%s=%s", st.name, st.state))
		}
	}
	return strings.Join(notReady, ", ")
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
