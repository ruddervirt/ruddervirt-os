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

	"ruddervirt-setup/internal/config"
	execpkg "ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/network"
	"ruddervirt-setup/internal/osupdate"
	"ruddervirt-setup/internal/status"
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

// downloadAndUntar downloads a .tar.gz from url and extracts only the
// etcdctl binary into destDir
func downloadAndUntar(url, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		// /opt is a symlink on FCOS; MkdirAll chokes on it, so read where it
		// points and create the real path directly.
		parent := filepath.Dir(destDir)
		link, readErr := os.Readlink(parent)
		if readErr != nil {
			return err
		}
		// link may be relative (e.g. "var/opt") - make it absolute
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
		if err := execpkg.RunPrivileged("/usr/bin/rpm", "-q", p).Run(); err != nil {
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

func planInstallPackages(cfg config.Config) string {
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

func planInstallEtcdctl(cfg config.Config) string {
	if etcdctlInstalled() {
		return "skip - etcdctl already installed"
	}
	return fmt.Sprintf("will download etcdctl %s", etcdVersion)
}

var installSteps = []installsteps.Step{
	{
		// Applies settings that need calling out to another system
		// component (nmcli, Zincati) - not settings later steps already
		// apply just by reading cfg directly (k3s version, pod/service CIDR, etc).
		Label: "Applying settings",
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			const label = "Applying settings"

			ch <- stepOutputMsg(fmt.Sprintf("Applying %s addressing on %s...", cfg.Network.Addressing, cfg.Network.InterfaceName))
			if err := network.ApplyNetworkConfig(cfg.Network); err != nil {
				ch <- stepDoneMsg{Label: label, Err: err}
				return
			}
			status := "off"
			if cfg.System.AutoUpdate {
				status = "on"
			}
			ch <- stepOutputMsg(fmt.Sprintf("Setting automatic updates: %s", status))
			if err := config.WriteZincatiConfig(cfg.System.AutoUpdate, config.ZincatiConfigPath); err != nil {
				ch <- stepDoneMsg{Label: label, Err: err}
				return
			}

			ch <- stepDoneMsg{Label: label}
		},
	},
	{
		Label: "Installing packages",
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			label := "Installing packages"
			missing := missingPackages()
			if len(missing) == 0 {
				ch <- stepOutputMsg("All packages already installed")
				ch <- stepDoneMsg{Label: label}
				return
			}
			ch <- stepOutputMsg(fmt.Sprintf("Installing: %s", strings.Join(missing, ", ")))
			args := append([]string{"install", "--apply-live", "--allow-inactive", "--assumeyes"}, missing...)
			ch <- stepDoneMsg{Label: label, Err: execpkg.RunStreamed(ch, wrapStepOutput, osupdate.RpmOstreeBinPath, args...)}
		},
		Plan: planInstallPackages,
	},
	{
		Label: "Installing etcdctl",
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			label := "Installing etcdctl"
			if etcdctlInstalled() {
				ch <- stepOutputMsg("etcdctl already installed")
				ch <- stepDoneMsg{Label: label}
				return
			}
			ch <- stepOutputMsg(fmt.Sprintf("Downloading etcdctl %s...", etcdVersion))
			url := fmt.Sprintf(
				"https://github.com/etcd-io/etcd/releases/download/%s/etcd-%s-linux-amd64.tar.gz",
				etcdVersion, etcdVersion,
			)
			// download to /tmp first, then move to /opt/bin to avoid permission issues
			if err := downloadAndUntar(url, "/tmp"); err != nil {
				ch <- stepDoneMsg{Label: label, Err: err}
				return
			}

			// use sudo to create /var/opt/bin
			if out, err := execpkg.RunPrivileged("/usr/bin/mkdir", "-p", "/var/opt/bin").CombinedOutput(); err != nil {
				ch <- stepDoneMsg{Label: label, Err: execpkg.WrapCmdErr(out, err)}
				return
			}

			// move binary into place with sudo
			if out, err := execpkg.RunPrivileged("/usr/bin/mv", "/tmp/etcdctl", "/opt/bin/etcdctl").CombinedOutput(); err != nil {
				ch <- stepDoneMsg{Label: label, Err: execpkg.WrapCmdErr(out, err)}
				return
			}

			ch <- stepOutputMsg("etcdctl installed successfully")
			ch <- stepDoneMsg{Label: label}
		},
		Plan: planInstallEtcdctl,
	},
	{
		Label: "Installing k3s",
		Run:   installK3sStep,
		Plan:  planK3sInstall,
	},
	{
		Label: "Downloading KubeVirt/CDI manifests",
		Run:   downloadKubeVirtCDIManifestsStep,
		Plan:  planKubeVirtCDIDownload,
	},
	{
		Label: "Rendering k3s config",
		Run:   renderK3sConfigStep,
	},
	{
		Label: "Writing k3s systemd unit",
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			label := "Writing k3s systemd unit"
			const unitPath = "/etc/systemd/system/k3s.service"
			ch <- stepOutputMsg(fmt.Sprintf("Writing %s...", unitPath))

			// write to tmp first to avoid permission issues
			if err := os.WriteFile("/tmp/k3s.service", []byte(k3sUnitContent), 0644); err != nil {
				ch <- stepDoneMsg{Label: label, Err: err}
				return
			}

			// move to /usr/bin with sudo
			if out, err := execpkg.RunPrivileged("/usr/bin/mv", "/tmp/k3s.service", unitPath).CombinedOutput(); err != nil {
				ch <- stepDoneMsg{Label: label, Err: execpkg.WrapCmdErr(out, err)}
				return
			}

			ch <- stepOutputMsg("Running systemctl daemon-reload...")
			out, err := execpkg.RunPrivileged("/usr/bin/systemctl", "daemon-reload").CombinedOutput()
			if s := strings.TrimSpace(string(out)); s != "" {
				ch <- stepOutputMsg(s)
			}
			ch <- stepDoneMsg{Label: label, Err: err}
		},
	},
	{
		Label: "Enabling and starting k3s",
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			const label = "Enabling and starting k3s"
			if err := execpkg.RunStreamed(ch, wrapStepOutput, "/usr/bin/systemctl", "enable", "k3s.service"); err != nil {
				ch <- stepDoneMsg{Label: label, Err: err}
				return
			}
			// Re-running install/update should apply a changed binary or
			// config, not just no-op like `enable --now` would on an
			// already-running service - restart if active, start otherwise.
			verb := "start"
			if execpkg.RunPrivileged("/usr/bin/systemctl", "is-active", "--quiet", "k3s.service").Run() == nil {
				verb = "restart"
			}
			ch <- stepDoneMsg{Label: label, Err: execpkg.RunStreamed(ch, wrapStepOutput, "/usr/bin/systemctl", verb, "k3s.service")}
		},
	},
	{
		Label: "Applying kube-ovn",
		Run:   applyKubeOvnStep,
		Plan:  planApplyKubeOvn,
	},
	{
		Label: "Preparing storage device",
		Run:   prepareStorageStep,
		Plan:  planStorageDevice,
	},
	{
		Label: "Applying manifests",
		Run:   prepareK3sStep,
		Plan:  planApplyManifests,
	},
	{
		Label: "Waiting for all services to become ready",
		Run:   waitForServicesReadyStep,
	},
}

// waitForServicesReadyStep is the last install step - earlier steps only
// wait for their piece to be *applied* (e.g. aileron's helm-install Job
// reaching Completed just means resources were created, not that pods rolled
// out), so without this "Install complete" could land while the home
// screen's Services summary still showed rows as "not ready". Blocks until
// every row (via status.go's fetchServiceStatuses) is ready.
func waitForServicesReadyStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Waiting for all services to become ready"
	ch <- stepOutputMsg(label + " (this can take a while)...")

	deadline := time.Now().Add(30 * time.Minute)
	for {
		statuses := fetchServiceStatuses(cfg)
		if allServicesReady(statuses) {
			ch <- stepDoneMsg{Label: label}
			return
		}
		if time.Now().After(deadline) {
			ch <- stepDoneMsg{Label: label, Err: fmt.Errorf("timed out waiting for: %s", notReadySummary(statuses))}
			return
		}
		time.Sleep(5 * time.Second)
	}
}

// allServicesReady reports whether every row fetchServiceStatuses returned
// is in its healthy terminal state - "running" for k3s, "ready" for
// everything else. An empty result (config not yet saved, or no usable sudo
// ticket) is never ready: there's nothing to confirm yet.
func allServicesReady(statuses []status.ServiceStatus) bool {
	if len(statuses) == 0 {
		return false
	}
	for _, st := range statuses {
		if st.State != "running" && st.State != "ready" {
			return false
		}
	}
	return true
}

// notReadySummary formats whichever rows weren't ready when
// waitForServicesReadyStep gave up, for its timeout error message.
func notReadySummary(statuses []status.ServiceStatus) string {
	var notReady []string
	for _, st := range statuses {
		if st.State != "running" && st.State != "ready" {
			notReady = append(notReady, fmt.Sprintf("%s=%s", st.Name, st.State))
		}
	}
	return strings.Join(notReady, ", ")
}

// computeInstallPlanCmd previews installSteps against cfg without changing
// system state - runs as its own goroutine (tea.Cmd), same pattern as
// checkForUpdateCmd, so a slow check can't block the UI thread.
func computeInstallPlanCmd(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		return installPlanMsg{lines: installsteps.ComputePlanLines(installSteps, cfg)}
	}
}
