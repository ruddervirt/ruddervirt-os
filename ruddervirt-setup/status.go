package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
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

// renderServiceStatuses formats the home screen's "Services" summary -
// statuses is nil until fetchServiceStatusesCmd's first result lands, in
// which case this renders nothing rather than a block of blank rows.
func renderServiceStatuses(statuses []serviceStatus, updatedAt time.Time) string {
	if len(statuses) == 0 {
		return ""
	}
	nameWidth := 0
	for _, st := range statuses {
		if l := runewidth.StringWidth(st.name); l > nameWidth {
			nameWidth = l
		}
	}
	header := "Services"
	if !updatedAt.IsZero() {
		header = fmt.Sprintf("Services (updated %s ago)", formatAge(time.Since(updatedAt)))
	}
	var b strings.Builder
	b.WriteString(helpStyle.Render(header) + "\n")
	for _, st := range statuses {
		fmt.Fprintf(&b, "  %s %s  %s\n", stateBullet(st.state), fitCell(st.name, nameWidth), styleState(st.state))
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

	if !nonInteractiveSucceeds("/usr/bin/systemctl", "is-active", "--quiet", "k3s.service") {
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

	aileronReady := nonInteractiveSucceeds(kubectlBin, "-n", "kube-system", "wait", "--for=condition=complete", "job/helm-install-aileron", "--timeout=1s")
	statuses = append(statuses, serviceStatus{name: "aileron", state: readyState(aileronReady)})

	return statuses
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

func readyState(ready bool) string {
	if ready {
		return "ready"
	}
	return "not ready"
}
