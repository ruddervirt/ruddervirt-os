// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/status"
)

// kubectlBinPath mirrors the literal fetchServiceStatuses uses - kept as its
// own constant since several package-main files (stabilizer TUI/CLI
// screens, app.go) need it. internal/status and internal/k3s each keep
// their own local copy, matching this codebase's convention of not sharing
// a single kubectl-path constant across packages.
const kubectlBinPath = "/usr/local/bin/kubectl"

// serviceStatusRefreshInterval is how often the home screen's "Services"
// summary re-polls while it's on screen. Long enough that a full sweep of
// checks (each up to status.StatusCheckTimeout) has finished well before
// the next one fires, short enough that a state change shows up without
// the operator having to navigate away and back.
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
// serviceStatusRefreshInterval's actual re-fetch. bubbletea only calls
// View() on a processed message, and the menu screen otherwise only gets
// the 15s refresh tick, which redraws right as each fetch completes.
// Without this, "updated Xs ago" would repaint once near zero then sit
// frozen for the rest of the cycle instead of counting up.
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

// serviceStatusMsg carries fetchServiceStatusesCmd's result back into
// Update - same "runs as a tea.Cmd, never synchronously" reasoning as
// k3sVersionsFetchedMsg.
type serviceStatusMsg struct {
	statuses []status.ServiceStatus
}

// fetchServiceStatuses/fetchServiceStatusesCmd adapt
// internal/status.FetchServiceStatuses's config.Config-free signature to the
// rest of package main - same wrapping pattern as k3s_bridge.go/kubevirt_bridge.go.
func fetchServiceStatuses(cfg config.Config) []status.ServiceStatus {
	return status.FetchServiceStatuses(cfg.Storage.Engine, config.ConfigSaved, stabilizerChartPresent)
}

// fetchServiceStatusesCmd is best-effort, same spirit as
// fetchK3sVersionsCmd/fetchAileronVersionsCmd - a slow or unreachable
// cluster just leaves status rows showing "unknown" rather than blocking the TUI.
//
// Drops the storage row before it reaches the home screen -
// fetchServiceStatuses still computes it (waitForServicesReadyStep needs the
// real signal), but its "wait --for=condition=Ready pods --all" check isn't
// reliable enough to show unprompted: it can report "not ready" even when
// every pod is healthy (e.g. under timing pressure), with nothing the
// operator can do about it from this screen anyway.
func fetchServiceStatusesCmd(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		statuses := fetchServiceStatuses(cfg)
		display := make([]status.ServiceStatus, 0, len(statuses))
		for _, st := range statuses {
			if strings.HasPrefix(st.Name, "storage (") {
				continue
			}
			display = append(display, st)
		}
		return serviceStatusMsg{statuses: display}
	}
}

// hostStatsMsg carries fetchHostStatsCmd's result back into Update. sample
// is the raw /proc/stat reading internal/status.FetchHostStats took while
// computing stats.CPUPercent - stashed in the model (prevCPUSample) so the
// next fetch has a baseline to diff against.
type hostStatsMsg struct {
	stats  status.HostStats
	sample status.CPUSample
}

func fetchHostStatsCmd(cfg config.Config, prevSample status.CPUSample) tea.Cmd {
	return func() tea.Msg {
		stats, sample := status.FetchHostStats(cfg.Storage.Engine, prevSample, config.ConfigSaved)
		return hostStatsMsg{stats: stats, sample: sample}
	}
}
