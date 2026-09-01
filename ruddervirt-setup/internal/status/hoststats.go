// SPDX-License-Identifier: GPL-3.0-only

package status

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"

	"ruddervirt-setup/internal/exec"
)

// HostStats is the home screen's "System" summary: CPU/memory load, disk
// space, and running KubeVirt VMs. Each *Known flag is false until its
// reading has succeeded once, letting the renderer (internal/tui/render.go)
// omit a figure it couldn't determine instead of showing a misleading zero.
type HostStats struct {
	CPUPercent float64
	CPUKnown   bool

	MemUsedGiB  float64
	MemTotalGiB float64
	MemPercent  float64
	MemKnown    bool

	DiskFreeGiB  float64
	DiskTotalGiB float64
	DiskKnown    bool

	VMRunning int
	VMTotal   int
	VMKnown   bool
}

// CPUSample is one reading of /proc/stat's aggregate "cpu" line, in
// jiffies. /proc/stat only exposes cumulative counters, so two samples
// spanning one interval (see cpuPercentBetween) yield "CPU busy since last
// check". Exported but opaque (fields unexported): package main's model
// stashes the previous sample between fetches (hostStatsMsg).
type CPUSample struct {
	idle  uint64
	total uint64
}

// FetchHostStats is the synchronous body of package main's
// fetchHostStatsCmd, split out so it's callable without a running
// tea.Program (e.g. tests). Returns the CPU sample it read alongside
// stats, since the caller needs it as next round's baseline either way.
//
// engine is cfg.Storage.Engine; configSaved is injected (config.ConfigSaved)
// since this package can't import package main - see its fetchHostStats
// adapter.
func FetchHostStats(engine string, prevSample CPUSample, configSaved func() bool) (HostStats, CPUSample) {
	var stats HostStats
	sample := prevSample

	if cur, ok := readCPUSample(); ok {
		sample = cur
		if pct, ok := cpuPercentBetween(prevSample, cur); ok {
			stats.CPUPercent = pct
			stats.CPUKnown = true
		}
	}

	if used, total, pct, ok := readMemStats(); ok {
		stats.MemUsedGiB = used
		stats.MemTotalGiB = total
		stats.MemPercent = pct
		stats.MemKnown = true
	}

	// Disk capacity and VM counts both need a live cluster (or, for openebs,
	// root privilege to read the host's LVM state) - same gating as
	// FetchServiceStatuses (fresh/unconfigured system, no cached sudo ticket).
	const kubectlBin = "/usr/local/bin/kubectl"
	if configSaved() && HaveNonInteractiveSudo() {
		if free, total, ok := storageEngineCapacity(kubectlBin, engine); ok {
			stats.DiskFreeGiB = free
			stats.DiskTotalGiB = total
			stats.DiskKnown = true
		}
		if running, total, ok := fetchVMCounts(kubectlBin); ok {
			stats.VMRunning = running
			stats.VMTotal = total
			stats.VMKnown = true
		}
	}

	return stats, sample
}

// cpuIdleField is /proc/stat's "cpu" line field index (after the leading
// "cpu" token) holding idle time, per `man proc` (5).
const cpuIdleField = 3

func readCPUSample() (CPUSample, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return CPUSample{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return CPUSample{}, false
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < cpuIdleField+2 || fields[0] != "cpu" {
		return CPUSample{}, false
	}

	var total uint64
	for _, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return CPUSample{}, false
		}
		total += v
	}
	idle, err := strconv.ParseUint(fields[1+cpuIdleField], 10, 64)
	if err != nil {
		return CPUSample{}, false
	}
	return CPUSample{idle: idle, total: total}, true
}

// cpuPercentBetween reports CPU busy% across two samples spanning one
// refresh interval. Skipped when there's no earlier sample yet (first fetch
// after startup) or the counters didn't move (too-close reads, or a reset).
func cpuPercentBetween(prev, cur CPUSample) (float64, bool) {
	if prev.total == 0 || cur.total <= prev.total {
		return 0, false
	}
	totalDelta := cur.total - prev.total
	idleDelta := cur.idle - prev.idle
	return (1 - float64(idleDelta)/float64(totalDelta)) * 100, true
}

// bytesPerGiB converts /proc/meminfo's kB figures and the storage engines'
// byte-denominated capacity figures into the GiB units the renderer
// (internal/tui/render.go) displays.
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

// fetchVMCounts asks KubeVirt how many VirtualMachineInstances exist and
// how many are Running - best-effort like FetchServiceStatuses's kubectl
// checks, since a fresh/still-installing node has no vmi CRD yet.
func fetchVMCounts(kubectlBin string) (running, total int, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), StatusCheckTimeout)
	defer cancel()
	out, err := exec.RunNonInteractive(ctx, kubectlBin, "get", "vmi", "-A", "--no-headers", "-o", "custom-columns=PHASE:.status.phase").Output()
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
