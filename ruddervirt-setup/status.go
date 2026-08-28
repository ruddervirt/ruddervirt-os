// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// statusCheckTimeout bounds every individual check below - short enough
// that a slow/unreachable API can't make the home screen feel stuck, since
// these all run as a background tea.Cmd, never blocking keyboard input.
// Padded well past each kubectl call's own --timeout=1s so a slow VM's
// process-startup/TLS-handshake overhead alone can't make an otherwise-met
// condition read as a context-cancelled failure.
const statusCheckTimeout = 5 * time.Second

// serviceStatusRefreshInterval is how often the home screen's "Services"
// summary re-polls while it's on screen. Long enough that a full sweep of
// checks (each up to statusCheckTimeout) has finished well before the next
// one fires, short enough that a state change shows up without the operator
// having to navigate away and back.
const serviceStatusRefreshInterval = 15 * time.Second

// serviceStatusTickMsg drives fetchServiceStatuses's periodic refresh -
// see tickServiceStatusCmd.
type serviceStatusTickMsg time.Time

// tickServiceStatusCmd schedules the next periodic refresh. It self-repeats
// via the serviceStatusTickMsg case in Update, which only re-fetches while
// m.current == screenMenu, but always reschedules itself regardless of
// screen so the ticking keeps running in the background rather than needing
// to be restarted on every screen change.
func tickServiceStatusCmd() tea.Cmd {
	return tea.Tick(serviceStatusRefreshInterval, func(t time.Time) tea.Msg {
		return serviceStatusTickMsg(t)
	})
}

// serviceStatusRenderInterval drives a redraw-only tick, separate from
// serviceStatusRefreshInterval's actual re-fetch - bubbletea only calls
// View() in response to a processed message, and on the menu screen the
// only thing that otherwise fires periodically is the 15s refresh tick
// itself, which redraws right as each fetch completes. Without this,
// "updated Xs ago" would only ever be repainted at that moment (elapsed
// time close to zero) and then sit frozen for the rest of the 15s cycle,
// instead of visibly counting up.
const serviceStatusRenderInterval = 1 * time.Second

// serviceStatusRenderTickMsg carries no data - its only job is to make
// Update return a new tea.Cmd so bubbletea repaints, letting the "updated
// Xs ago" hint's time.Since call re-evaluate against the current clock.
type serviceStatusRenderTickMsg time.Time

// tickServiceStatusRenderCmd mirrors tickServiceStatusCmd's self-repeating
// pattern - always reschedules regardless of screen, same reasoning as
// there, so it doesn't need restarting on every screen change.
func tickServiceStatusRenderCmd() tea.Cmd {
	return tea.Tick(serviceStatusRenderInterval, func(t time.Time) tea.Msg {
		return serviceStatusRenderTickMsg(t)
	})
}

// serviceStatus is one row of the home screen's "Services" summary.
type serviceStatus struct {
	name  string
	state string
}

// serviceStatusMsg carries fetchServiceStatusesCmd's result back into
// Update - same "runs as a tea.Cmd, never synchronously" reasoning as
// k3sVersionsFetchedMsg (setup.go).
type serviceStatusMsg struct {
	statuses []serviceStatus
}

// fetchServiceStatusesCmd is best-effort, same spirit as
// fetchK3sVersionsCmd/fetchAileronVersionsCmd - a slow or unreachable
// cluster just leaves the home screen's status rows showing "unknown"
// rather than blocking the TUI.
//
// Drops the storage row before it ever reaches the home screen -
// fetchServiceStatuses still computes it (waitForServicesReadyStep, the
// install pipeline's completion gate, needs the real signal), but its
// "wait --for=condition=Ready pods --all" check isn't reliable enough to
// show unprompted here: it can report "not ready" even when every pod in
// the namespace is actually healthy (e.g. under a VM's timing pressure),
// and there's nothing the operator can do about it from this screen
// anyway.
func fetchServiceStatusesCmd(cfg Config) tea.Cmd {
	return func() tea.Msg {
		statuses := fetchServiceStatuses(cfg)
		display := make([]serviceStatus, 0, len(statuses))
		for _, st := range statuses {
			if strings.HasPrefix(st.name, "storage (") {
				continue
			}
			display = append(display, st)
		}
		return serviceStatusMsg{statuses: display}
	}
}

// serviceStatusLine formats the home screen's Services row - all services
// condensed onto one line (state conveyed by the bullet's color, see
// stateBullet), or "" when statuses is nil (fetchServiceStatusesCmd's first
// result hasn't landed yet). Combined with hostStatsLine (hoststats.go)
// under a single "Status" header by renderHomeStatus (view.go) instead of
// each getting its own header/blank-line overhead.
func serviceStatusLine(statuses []serviceStatus) string {
	if len(statuses) == 0 {
		return ""
	}
	parts := make([]string, len(statuses))
	for i, st := range statuses {
		parts[i] = fmt.Sprintf("%s %s", stateBullet(st.state), st.name)
	}
	return strings.Join(parts, "   ")
}

// renderHomeStatus formats the home screen's combined "Status" block -
// Services (serviceStatusLine) and System (hostStatsLine, hoststats.go)
// used to render as two separate headered blocks; merged into one here so
// their header/blank-line overhead isn't paid twice, leaving more room for
// the menu below. Renders nothing until at least one of the two rows has
// something to show.
func renderHomeStatus(statuses []serviceStatus, hs hostStats, updatedAt time.Time) string {
	svcLine := serviceStatusLine(statuses)
	sysLine := hostStatsLine(hs)
	if svcLine == "" && sysLine == "" {
		return ""
	}
	header := "Status"
	if !updatedAt.IsZero() {
		header = fmt.Sprintf("Status (updated %s ago)", formatAge(time.Since(updatedAt)))
	}
	var b strings.Builder
	b.WriteString(helpStyle.Render(header) + "\n")
	if svcLine != "" {
		b.WriteString("  " + svcLine + "\n")
	}
	if sysLine != "" {
		b.WriteString("  " + sysLine + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// formatAge renders d the way the home screen's "updated ... ago" hint
// wants it - whole seconds/minutes, never a sub-second fraction.
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}

// nonInteractiveSucceeds runs a bounded, non-interactive command and
// reports only whether it exited clean - the shape every check below
// needs (a plain existence/readiness probe, never any output).
func nonInteractiveSucceeds(name string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), statusCheckTimeout)
	defer cancel()
	return runNonInteractive(ctx, name, args...).Run() == nil
}

// k3sServiceActive reports whether the k3s.service systemd unit is
// currently active. This is the mandatory gate before trusting ANY kubectl
// call, not just an optimization: before "Installing k3s" has actually run,
// /usr/local/bin/k3s is only a placeholder text file with no `#!` shebang
// (see server.bu) - POSIX shell's ENOEXEC fallback silently reinterprets an
// unrecognized-but-executable file as a no-op shell script instead of
// failing, so `kubectl` (which execs through the /usr/local/bin/kubectl
// wrapper's `exec k3s kubectl "$@"`) reports a false SUCCESS (exit 0, no
// output) instead of a real "k3s not found" error. Checking k3s.service
// first means a kubectl-based check can never be fooled by that placeholder
// into reporting a resource as present/ready when k3s was never installed
// at all.
func k3sServiceActive() bool {
	return nonInteractiveSucceeds("/usr/bin/systemctl", "is-active", "--quiet", "k3s.service")
}

// haveNonInteractiveSudo reports whether a cached sudo ticket lets
// nonInteractiveSucceeds's checks actually reach kubectl/systemctl - if
// not (e.g. a fresh boot, or the ticket has expired), every check below
// would fail identically, so callers short-circuit to "unknown" instead of
// running (and waiting out the timeout for) each one individually.
func haveNonInteractiveSudo() bool {
	if os.Getuid() == 0 {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusCheckTimeout)
	defer cancel()
	return DefaultRunner.Run(ctx, "sudo", "-n", "true") == nil
}

// fetchServiceStatuses is the synchronous body of fetchServiceStatusesCmd -
// split out so it's callable without a running tea.Program (e.g. tests).
// Returns nil (rendered as nothing, see renderServiceStatuses) rather than
// probing systemctl/kubectl at all if the operator has never even run
// "configure" - there's nothing meaningful to report on a fresh system.
func fetchServiceStatuses(cfg Config) []serviceStatus {
	if !configSaved() {
		return nil
	}

	engine := cfg.Storage.Engine
	names := []string{"k3s", "kube-ovn", fmt.Sprintf("storage (%s)", engine), "kubevirt", "aileron"}

	unknown := func() []serviceStatus {
		out := make([]serviceStatus, len(names))
		for i, n := range names {
			out[i] = serviceStatus{name: n, state: "unknown"}
		}
		return out
	}

	// No cached sudo ticket - every check below would fail identically
	// (and each would burn statusCheckTimeout doing it), so report unknown
	// up front instead.
	if !haveNonInteractiveSudo() {
		return unknown()
	}

	const kubectlBin = "/usr/local/bin/kubectl"

	if !k3sServiceActive() {
		out := unknown()
		out[0].state = "not running"
		for i := 1; i < len(out); i++ {
			out[i].state = "not running"
		}
		return out
	}

	statuses := []serviceStatus{{name: "k3s", state: "running"}}

	kubeOvnReady := true
	for _, w := range kubeOvnCoreWorkloads {
		if !nonInteractiveSucceeds(kubectlBin, "-n", "kube-system", "rollout", "status", w.kind+"/"+w.name, "--timeout=1s") {
			kubeOvnReady = false
			break
		}
	}
	statuses = append(statuses, serviceStatus{name: "kube-ovn", state: readyState(kubeOvnReady)})

	statuses = append(statuses, serviceStatus{
		name:  fmt.Sprintf("storage (%s)", engine),
		state: readyState(storageEngineReady(kubectlBin, engine)),
	})

	// KubeVirt's and CDI's own operators set condition=Available once fully
	// rolled out - both must be true for the combined "kubevirt" row here.
	kubevirtReady := nonInteractiveSucceeds(kubectlBin, "-n", "kubevirt", "wait", "--for=condition=Available", "kubevirt/kubevirt", "--timeout=1s") &&
		nonInteractiveSucceeds(kubectlBin, "wait", "--for=condition=Available", "cdi/cdi", "--timeout=1s")
	statuses = append(statuses, serviceStatus{name: "kubevirt", state: readyState(kubevirtReady)})

	statuses = append(statuses, serviceStatus{name: "aileron", state: readyState(aileronReady(kubectlBin))})

	// Only shown for a stabilizer-managed install (see stabilizerChartPresent,
	// aileron.go) - a plain self-hosted node has no "stabilizer" Deployment
	// at all, so unconditionally waiting on it would just read as a
	// permanently "not ready" row for everyone else.
	if stabilizerChartPresent() {
		statuses = append(statuses, serviceStatus{name: "stabilizer", state: readyState(stabilizerReady(kubectlBin))})
	}

	return statuses
}

// stabilizerReady reports whether the stabilizer Deployment - alongside
// Aileron's own, in the same ruddervirt-system namespace - has become
// Available.
func stabilizerReady(kubectlBin string) bool {
	return nonInteractiveSucceeds(kubectlBin, "-n", "ruddervirt-system", "wait", "--for=condition=Available", "deployment.apps/stabilizer", "--timeout=1s")
}

// aileronReady reports whether Aileron itself is up - not just whether
// ruddervirt-setup's own install of it succeeded. Waiting on the
// "helm-install-aileron" Job applyAileron (aileron.go) creates only reflects
// that; once a "stabilizer" HelmChart takes over Aileron's management (see
// stabilizerChartPresent), the "aileron" HelmChart and that Job may no
// longer exist at all, which would otherwise always read as "not ready"
// regardless of Aileron's actual health. Waiting on the Deployment directly
// instead - in ruddervirt-system, aileron-helmchart.yaml's targetNamespace -
// works either way, since that's where it lands regardless of which chart
// put it there.
func aileronReady(kubectlBin string) bool {
	return nonInteractiveSucceeds(kubectlBin, "-n", "ruddervirt-system", "wait", "--for=condition=Available", "deployment.apps/aileron", "--timeout=1s")
}

// storageEngineReady mirrors applyStorageEngine's (k3s.go) per-engine
// readiness signal, just probed instantly instead of blocked on.
func storageEngineReady(kubectlBin, engine string) bool {
	switch engine {
	case "rook-ceph":
		return nonInteractiveSucceeds(kubectlBin, "-n", "rook-ceph", "wait", "--for=condition=Ready", "cephcluster/rook-ceph", "--timeout=1s")
	case "longhorn":
		return nonInteractiveSucceeds(kubectlBin, "-n", "longhorn-system", "wait", "--for=condition=Ready", "pods", "--all", "--timeout=1s")
	case "openebs":
		return nonInteractiveSucceeds(kubectlBin, "-n", "openebs", "wait", "--for=condition=Ready", "pods", "--all", "--timeout=1s")
	default:
		return false
	}
}

// storageEngineCapacity asks the storage engine itself how much space is
// left for VM data - unlike statfs("/") (this home screen's old approach),
// this reflects the engine's own usable capacity rather than the root
// filesystem, which for rook-ceph doesn't even have one: bluestore OSDs
// consume the raw partition directly, never mounted, so df/statfs report
// nothing meaningful about it at all.
func storageEngineCapacity(kubectlBin, engine string) (freeGiB, totalGiB float64, ok bool) {
	switch engine {
	case "rook-ceph":
		return cephClusterCapacity(kubectlBin)
	case "longhorn":
		return longhornCapacity(kubectlBin)
	case "openebs":
		return openebsVGCapacity()
	default:
		return 0, 0, false
	}
}

// cephClusterCapacity reads the CephCluster CR's own capacity summary - the
// rook operator periodically populates status.ceph.capacity from `ceph df`
// itself, so this needs nothing beyond a single kubectl get (no toolbox pod
// exec required).
func cephClusterCapacity(kubectlBin string) (freeGiB, totalGiB float64, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), statusCheckTimeout)
	defer cancel()
	out, err := runNonInteractive(ctx, kubectlBin, "get", "cephcluster", "-n", "rook-ceph", "rook-ceph",
		"-o", "jsonpath={.status.ceph.capacity.bytesAvailable} {.status.ceph.capacity.bytesTotal}").Output()
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, false
	}
	avail, err1 := strconv.ParseFloat(fields[0], 64)
	total, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil || total == 0 {
		return 0, 0, false
	}
	return avail / bytesPerGiB, total / bytesPerGiB, true
}

// longhornNodeList is the handful of nodes.longhorn.io CR fields
// longhornCapacity needs. Each node reports its own disks' capacity under
// status.diskStatus, summed across every node/disk for a cluster-wide total
// - this appliance is usually single-node, but a multi-node setup (see
// README's network table) reports the same CR shape either way.
type longhornNodeList struct {
	Items []struct {
		Status struct {
			DiskStatus map[string]struct {
				StorageAvailable int64 `json:"storageAvailable"`
				StorageMaximum   int64 `json:"storageMaximum"`
			} `json:"diskStatus"`
		} `json:"status"`
	} `json:"items"`
}

func longhornCapacity(kubectlBin string) (freeGiB, totalGiB float64, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), statusCheckTimeout)
	defer cancel()
	out, err := runNonInteractive(ctx, kubectlBin, "get", "nodes.longhorn.io", "-n", "longhorn-system", "-o", "json").Output()
	if err != nil {
		return 0, 0, false
	}
	var list longhornNodeList
	if err := json.Unmarshal(out, &list); err != nil {
		return 0, 0, false
	}
	var freeBytes, totalBytes int64
	for _, item := range list.Items {
		for _, disk := range item.Status.DiskStatus {
			freeBytes += disk.StorageAvailable
			totalBytes += disk.StorageMaximum
		}
	}
	if totalBytes == 0 {
		return 0, 0, false
	}
	return float64(freeBytes) / bytesPerGiB, float64(totalBytes) / bytesPerGiB, true
}

// openebsVGCapacity reads the LVM thin pool's own size and data usage -
// vg_free/vg_size (the VG's unallocated space) was tried first, but that's
// wrong for a thin pool: prepareOpenEBSDevice (storage.go) allocates the
// pool as 95%VG up front, so vg_free reports ~5% free immediately on a
// brand new install regardless of how much data any thin volume has
// actually written, reading as a nearly-full disk from day one. The pool
// LV's own data_percent - how much of its provisioned size is actually
// written - is what tracks real usage; lv_size is the pool's own size (the
// 95%VG), i.e. the real ceiling for VM data even though the underlying VG
// is technically 100%"used" the moment the pool exists.
//
// The LVM LocalPV CSI driver has no capacity API of its own, and the
// volume group already lives on this host (see prepareOpenEBSDevice), so
// there's no cluster round-trip needed at all. --units b sidesteps LVM's
// g/G (binary vs. decimal GiB/GB) unit ambiguity entirely by asking for
// exact bytes instead - data_percent isn't a size field, so --units doesn't
// touch it.
func openebsVGCapacity() (freeGiB, totalGiB float64, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), statusCheckTimeout)
	defer cancel()
	out, err := runNonInteractive(ctx, lvsBin, "--noheadings", "--units", "b", "--nosuffix", "-o", "lv_size,data_percent", openebsVGName+"/"+openebsThinPoolLV).Output()
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, false
	}
	sizeBytes, err1 := strconv.ParseFloat(fields[0], 64)
	dataPercent, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil || sizeBytes == 0 {
		return 0, 0, false
	}
	totalGiB = sizeBytes / bytesPerGiB
	freeGiB = totalGiB * (1 - dataPercent/100)
	return freeGiB, totalGiB, true
}

func readyState(ready bool) string {
	if ready {
		return "ready"
	}
	return "not ready"
}
