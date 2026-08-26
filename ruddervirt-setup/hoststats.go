// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

// hostStats is the home screen's "System" summary - CPU/memory load, disk
// space remaining, and how many KubeVirt VMs are running - refreshed on the
// same cadence as serviceStatus (status.go)'s Services summary. Each *Known
// flag is false until its own reading has succeeded at least once, letting
// renderHostStats omit a figure it couldn't determine instead of showing a
// misleading zero.
type hostStats struct {
	cpuPercent float64
	cpuKnown   bool

	memUsedGiB  float64
	memTotalGiB float64
	memPercent  float64
	memKnown    bool

	diskFreeGiB  float64
	diskTotalGiB float64
	diskKnown    bool

	vmRunning int
	vmTotal   int
	vmKnown   bool
}

// hostStatsMsg carries fetchHostStatsCmd's result back into Update, same
// "runs as a tea.Cmd, never synchronously" reasoning as serviceStatusMsg.
// sample is the raw /proc/stat reading fetchHostStats took while computing
// stats.cpuPercent - Update stashes it in the model (prevCPUSample) so the
// next fetch has a baseline to diff against.
type hostStatsMsg struct {
	stats  hostStats
	sample cpuSample
}

func fetchHostStatsCmd(cfg Config, prevSample cpuSample) tea.Cmd {
	return func() tea.Msg {
		stats, sample := fetchHostStats(cfg, prevSample)
		return hostStatsMsg{stats: stats, sample: sample}
	}
}

// fetchHostStats is the synchronous body of fetchHostStatsCmd - split out
// so it's callable without a running tea.Program (e.g. tests). Returns the
// CPU sample it read alongside stats, since the caller needs it as next
// round's baseline regardless of whether cpuPercentBetween could use it
// yet.
func fetchHostStats(cfg Config, prevSample cpuSample) (hostStats, cpuSample) {
	var stats hostStats
	sample := prevSample

	if cur, ok := readCPUSample(); ok {
		sample = cur
		if pct, ok := cpuPercentBetween(prevSample, cur); ok {
			stats.cpuPercent = pct
			stats.cpuKnown = true
		}
	}

	if used, total, pct, ok := readMemStats(); ok {
		stats.memUsedGiB = used
		stats.memTotalGiB = total
		stats.memPercent = pct
		stats.memKnown = true
	}

	if free, total, ok := readDiskStats(diskStatsPath); ok {
		stats.diskFreeGiB = free
		stats.diskTotalGiB = total
		stats.diskKnown = true
	}

	// VM counts need a live cluster - same "nothing meaningful to report on
	// a fresh/unconfigured system" and "no cached sudo ticket" gating as
	// fetchServiceStatuses, since kubectl needs both a saved config and a
	// root kubeconfig to answer.
	if configSaved() && haveNonInteractiveSudo() {
		if running, total, ok := fetchVMCounts(kubectlBinPath); ok {
			stats.vmRunning = running
			stats.vmTotal = total
			stats.vmKnown = true
		}
	}

	return stats, sample
}

// kubectlBinPath mirrors the literal fetchServiceStatuses (status.go) uses -
// kept as its own constant here since hoststats.go's kubectl call is
// otherwise unrelated to that function.
const kubectlBinPath = "/usr/local/bin/kubectl"

// cpuSample is one reading of /proc/stat's aggregate "cpu" line, in
// jiffies. /proc/stat only ever exposes cumulative counters, never an
// instantaneous percentage, so two samples spanning one refresh interval -
// see cpuPercentBetween - are what turn into "CPU busy since last check".
type cpuSample struct {
	idle  uint64
	total uint64
}

// cpuIdleField is /proc/stat's "cpu" line field index (after the leading
// "cpu" token) holding idle time, per `man proc` (5).
const cpuIdleField = 3

func readCPUSample() (cpuSample, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return cpuSample{}, false
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < cpuIdleField+2 || fields[0] != "cpu" {
		return cpuSample{}, false
	}

	var total uint64
	for _, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return cpuSample{}, false
		}
		total += v
	}
	idle, err := strconv.ParseUint(fields[1+cpuIdleField], 10, 64)
	if err != nil {
		return cpuSample{}, false
	}
	return cpuSample{idle: idle, total: total}, true
}

// cpuPercentBetween reports CPU busy% across two samples spanning one
// refresh interval. Meaningless - and skipped - when there's no earlier
// sample yet (the very first fetch after startup) or the counters didn't
// move (two reads too close together, or a counter reset).
func cpuPercentBetween(prev, cur cpuSample) (float64, bool) {
	if prev.total == 0 || cur.total <= prev.total {
		return 0, false
	}
	totalDelta := cur.total - prev.total
	idleDelta := cur.idle - prev.idle
	return (1 - float64(idleDelta)/float64(totalDelta)) * 100, true
}

// bytesPerGiB converts /proc/meminfo's kB figures and syscall.Statfs_t's
// block counts into the GiB units renderHostStats displays.
const bytesPerGiB = 1024 * 1024 * 1024

func readMemStats() (usedGiB, totalGiB, percent float64, ok bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, false
	}
	defer f.Close()

	var totalKB, availKB uint64
	var haveTotal, haveAvail bool
	scanner := bufio.NewScanner(f)
	for scanner.Scan() && !(haveTotal && haveAvail) {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalKB, err = strconv.ParseUint(fields[1], 10, 64)
			haveTotal = err == nil
		case "MemAvailable:":
			availKB, err = strconv.ParseUint(fields[1], 10, 64)
			haveAvail = err == nil
		}
	}
	if !haveTotal || !haveAvail || totalKB == 0 {
		return 0, 0, 0, false
	}

	const bytesPerKB = 1024
	totalGiB = float64(totalKB) * bytesPerKB / bytesPerGiB
	usedGiB = float64(totalKB-availKB) * bytesPerKB / bytesPerGiB
	percent = float64(totalKB-availKB) / float64(totalKB) * 100
	return usedGiB, totalGiB, percent, true
}

// diskStatsPath is statfs'd for the home screen's "Disk" figure - "/" since
// this is a single-disk all-in-one install (see README's Target Hardware
// Requirements) and VM/cluster storage all lives under the same root
// filesystem.
const diskStatsPath = "/"

func readDiskStats(path string) (freeGiB, totalGiB float64, ok bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}
	freeGiB = float64(stat.Bavail) * float64(stat.Bsize) / bytesPerGiB
	totalGiB = float64(stat.Blocks) * float64(stat.Bsize) / bytesPerGiB
	return freeGiB, totalGiB, true
}

// fetchVMCounts asks KubeVirt how many VirtualMachineInstances exist and how
// many are actually Running - best-effort like fetchServiceStatuses's own
// kubectl checks, since a fresh/unconfigured node or one still installing
// has no vmi CRD to ask yet.
func fetchVMCounts(kubectlBin string) (running, total int, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), statusCheckTimeout)
	defer cancel()
	out, err := runNonInteractive(ctx, kubectlBin, "get", "vmi", "-A", "--no-headers", "-o", "custom-columns=PHASE:.status.phase").Output()
	if err != nil {
		return 0, 0, false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return 0, 0, true
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		total++
		if line == "Running" {
			running++
		}
	}
	return running, total, true
}

// formatGiB renders a GiB figure the way the home screen's Mem figure wants
// it - switching to TiB above 1024 so a well-stocked server's total doesn't
// read as an unwieldy 4-5 digit GiB number.
func formatGiB(v float64) string {
	if v >= 1024 {
		return fmt.Sprintf("%.1f TiB", v/1024)
	}
	return fmt.Sprintf("%.0f GiB", v)
}

// formatDiskUsage renders the home screen's Disk figure as
// "MGi used/NGi total - X% (YGi free)" (used%, colored via
// styleUsagePercent) - this line already carries CPU/Mem/VMs alongside it,
// so every figure needs to earn its width, but leads with "used" (like Mem's
// figure) rather than "free" so a glance at the number matches the colored
// percent right next to it instead of reading as its inverse.
func formatDiskUsage(freeGiB, totalGiB float64) string {
	usedGiB := totalGiB - freeGiB
	usedPercent := 0.0
	if totalGiB > 0 {
		usedPercent = usedGiB / totalGiB * 100
	}
	return fmt.Sprintf("%.0fGi used/%.0fGi total - %s (%.0fGi free)", usedGiB, totalGiB, styleUsagePercent(usedPercent), freeGiB)
}

// hostStatsLine formats the home screen's System row - CPU/mem/disk/VM
// figures joined onto one line, or "" until fetchHostStatsCmd's first
// result has at least one figure to show. Combined with serviceStatusLine
// (status.go) under a single "Status" header by renderHomeStatus (view.go)
// instead of each getting its own header/blank-line overhead.
func hostStatsLine(hs hostStats) string {
	var parts []string
	if hs.cpuKnown {
		parts = append(parts, fmt.Sprintf("CPU %s", styleUsagePercent(hs.cpuPercent)))
	}
	if hs.memKnown {
		parts = append(parts, fmt.Sprintf("Mem %s (%s / %s)", styleUsagePercent(hs.memPercent), formatGiB(hs.memUsedGiB), formatGiB(hs.memTotalGiB)))
	}
	if hs.diskKnown {
		parts = append(parts, fmt.Sprintf("Disk %s", formatDiskUsage(hs.diskFreeGiB, hs.diskTotalGiB)))
	}
	if hs.vmKnown {
		parts = append(parts, fmt.Sprintf("VMs %d running / %d total", hs.vmRunning, hs.vmTotal))
	}
	return strings.Join(parts, "   ")
}
