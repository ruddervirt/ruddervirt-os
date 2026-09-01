// SPDX-License-Identifier: GPL-3.0-only

package status

import (
	"testing"

	"ruddervirt-setup/internal/exec/exectest"
)

func TestCpuPercentBetween(t *testing.T) {
	if _, ok := cpuPercentBetween(CPUSample{}, CPUSample{idle: 5, total: 10}); ok {
		t.Errorf("cpuPercentBetween with zero-value prev (no baseline yet) = ok, want !ok")
	}
	if _, ok := cpuPercentBetween(CPUSample{idle: 5, total: 10}, CPUSample{idle: 5, total: 10}); ok {
		t.Errorf("cpuPercentBetween with no elapsed ticks = ok, want !ok")
	}
	if _, ok := cpuPercentBetween(CPUSample{idle: 5, total: 10}, CPUSample{idle: 2, total: 8}); ok {
		t.Errorf("cpuPercentBetween with a counter that went backwards = ok, want !ok")
	}

	got, ok := cpuPercentBetween(CPUSample{idle: 100, total: 1000}, CPUSample{idle: 150, total: 1500})
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

func TestFetchVMCounts(t *testing.T) {
	t.Run("mixed phases", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("Running\nRunning\nScheduling\n")}
		}}
		var running, total int
		var ok bool
		exectest.WithFakeRunner(r, func() { running, total, ok = fetchVMCounts("/usr/local/bin/kubectl") })
		if !ok || running != 2 || total != 3 {
			t.Errorf("fetchVMCounts() = (%d, %d, %v), want (2, 3, true)", running, total, ok)
		}
	})

	t.Run("no VMs", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("")}
		}}
		var running, total int
		var ok bool
		exectest.WithFakeRunner(r, func() { running, total, ok = fetchVMCounts("/usr/local/bin/kubectl") })
		if !ok || running != 0 || total != 0 {
			t.Errorf("fetchVMCounts() = (%d, %d, %v), want (0, 0, true)", running, total, ok)
		}
	})

	t.Run("kubectl error", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		var ok bool
		exectest.WithFakeRunner(r, func() { _, _, ok = fetchVMCounts("/usr/local/bin/kubectl") })
		if ok {
			t.Errorf("fetchVMCounts() ok = true, want false")
		}
	})
}

func TestFetchHostStatsSkipsClusterQueriesWhenUnconfigured(t *testing.T) {
	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		t.Errorf("unexpected command on an unconfigured system: %s %v", name, args)
		return exectest.Outcome{}
	}}
	var stats HostStats
	exectest.WithFakeRunner(r, func() {
		stats, _ = FetchHostStats("openebs", CPUSample{}, func() bool { return false })
	})
	if stats.VMKnown {
		t.Errorf("FetchHostStats() on an unconfigured system reported VMKnown = true, want false")
	}
	if stats.DiskKnown {
		t.Errorf("FetchHostStats() on an unconfigured system reported DiskKnown = true, want false")
	}
}
