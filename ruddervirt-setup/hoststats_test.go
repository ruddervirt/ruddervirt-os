// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"testing"
)

func TestCpuPercentBetween(t *testing.T) {
	if _, ok := cpuPercentBetween(cpuSample{}, cpuSample{idle: 5, total: 10}); ok {
		t.Errorf("cpuPercentBetween with zero-value prev (no baseline yet) = ok, want !ok")
	}
	if _, ok := cpuPercentBetween(cpuSample{idle: 5, total: 10}, cpuSample{idle: 5, total: 10}); ok {
		t.Errorf("cpuPercentBetween with no elapsed ticks = ok, want !ok")
	}
	if _, ok := cpuPercentBetween(cpuSample{idle: 5, total: 10}, cpuSample{idle: 2, total: 8}); ok {
		t.Errorf("cpuPercentBetween with a counter that went backwards = ok, want !ok")
	}

	got, ok := cpuPercentBetween(cpuSample{idle: 100, total: 1000}, cpuSample{idle: 150, total: 1500})
	if !ok {
		t.Fatalf("cpuPercentBetween(...) ok = false, want true")
	}
	// idleDelta=50, totalDelta=500 -> 90% busy.
	if want := 90.0; got != want {
		t.Errorf("cpuPercentBetween(...) = %v, want %v", got, want)
	}
}

func TestReadCPUSample(t *testing.T) {
	sample, ok := readCPUSample()
	if !ok {
		t.Fatalf("readCPUSample() ok = false, want true (reading real /proc/stat)")
	}
	if sample.total == 0 {
		t.Errorf("readCPUSample() total = 0, want > 0")
	}
	if sample.idle > sample.total {
		t.Errorf("readCPUSample() idle = %d > total = %d", sample.idle, sample.total)
	}
}

func TestReadMemStats(t *testing.T) {
	used, total, pct, ok := readMemStats()
	if !ok {
		t.Fatalf("readMemStats() ok = false, want true (reading real /proc/meminfo)")
	}
	if total <= 0 {
		t.Errorf("readMemStats() totalGiB = %v, want > 0", total)
	}
	if used < 0 || used > total {
		t.Errorf("readMemStats() usedGiB = %v, want within [0, %v]", used, total)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("readMemStats() percent = %v, want within [0, 100]", pct)
	}
}

func TestFormatGiB(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{0, "0 GiB"},
		{412.4, "412 GiB"},
		{1023.9, "1024 GiB"},
		{1024, "1.0 TiB"},
		{1863, "1.8 TiB"},
	}
	for _, c := range cases {
		if got := formatGiB(c.v); got != c.want {
			t.Errorf("formatGiB(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestFetchVMCounts(t *testing.T) {
	t.Run("mixed phases", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte("Running\nRunning\nScheduling\n")}
		}}
		var running, total int
		var ok bool
		withFakeRunner(r, func() { running, total, ok = fetchVMCounts("/usr/local/bin/kubectl") })
		if !ok || running != 2 || total != 3 {
			t.Errorf("fetchVMCounts() = (%d, %d, %v), want (2, 3, true)", running, total, ok)
		}
	})

	t.Run("no VMs", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte("")}
		}}
		var running, total int
		var ok bool
		withFakeRunner(r, func() { running, total, ok = fetchVMCounts("/usr/local/bin/kubectl") })
		if !ok || running != 0 || total != 0 {
			t.Errorf("fetchVMCounts() = (%d, %d, %v), want (0, 0, true)", running, total, ok)
		}
	})

	t.Run("kubectl error", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{err: errFake}
		}}
		var ok bool
		withFakeRunner(r, func() { _, _, ok = fetchVMCounts("/usr/local/bin/kubectl") })
		if ok {
			t.Errorf("fetchVMCounts() ok = true, want false")
		}
	})
}

func TestFetchHostStatsSkipsClusterQueriesWhenUnconfigured(t *testing.T) {
	r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
		t.Errorf("unexpected command on an unconfigured system: %s %v", name, args)
		return commandOutcome{}
	}}
	var stats hostStats
	withConfigSaved(false, func() {
		withFakeRunner(r, func() { stats, _ = fetchHostStats(Config{}, cpuSample{}) })
	})
	if stats.vmKnown {
		t.Errorf("fetchHostStats() on an unconfigured system reported vmKnown = true, want false")
	}
	if stats.diskKnown {
		t.Errorf("fetchHostStats() on an unconfigured system reported diskKnown = true, want false")
	}
}

func TestFormatDiskUsage(t *testing.T) {
	cases := []struct {
		free, total float64
		want        string
	}{
		{412, 1863, "1451Gi used/1863Gi total - 78% (412Gi free)"},
		{1843.2, 2048, "205Gi used/2048Gi total - 10% (1843Gi free)"},
		{0, 0, "0Gi used/0Gi total - 0% (0Gi free)"},
	}
	for _, c := range cases {
		if got := formatDiskUsage(c.free, c.total); got != c.want {
			t.Errorf("formatDiskUsage(%v, %v) = %q, want %q", c.free, c.total, got, c.want)
		}
	}
}

func TestHostStatsLine(t *testing.T) {
	if got := hostStatsLine(hostStats{}); got != "" {
		t.Errorf("hostStatsLine(zero value) = %q, want empty string", got)
	}

	hs := hostStats{
		cpuPercent: 12, cpuKnown: true,
		memUsedGiB: 28.4, memTotalGiB: 62.7, memPercent: 45, memKnown: true,
		diskFreeGiB: 412, diskTotalGiB: 1863, diskKnown: true,
		vmRunning: 3, vmTotal: 5, vmKnown: true,
	}
	got := hostStatsLine(hs)
	want := "CPU 12%   Mem 45% (28 GiB / 63 GiB)   Disk 1451Gi used/1863Gi total - 78% (412Gi free)   VMs 3 running / 5 total"
	if got != want {
		t.Errorf("hostStatsLine(...) = %q, want %q", got, want)
	}
}
