// SPDX-License-Identifier: GPL-3.0-only

package tui

import (
	"strings"
	"testing"
	"time"

	"ruddervirt-setup/internal/status"
)

func TestServiceStatusLine(t *testing.T) {
	if got := serviceStatusLine(nil); got != "" {
		t.Errorf("serviceStatusLine(nil) = %q, want empty string", got)
	}
	if got := serviceStatusLine([]status.ServiceStatus{}); got != "" {
		t.Errorf("serviceStatusLine(empty) = %q, want empty string", got)
	}

	got := serviceStatusLine([]status.ServiceStatus{
		{Name: "k3s", State: "running"},
		{Name: "kube-ovn", State: "not ready"},
	})
	want := "● k3s   ● kube-ovn"
	if got != want {
		t.Errorf("serviceStatusLine(...) = %q, want %q", got, want)
	}
}

func TestRenderHomeStatus(t *testing.T) {
	const wideEnoughToNeverWrap = 200

	if got := RenderHomeStatus(nil, status.HostStats{}, time.Time{}, wideEnoughToNeverWrap); got != "" {
		t.Errorf("RenderHomeStatus(nil, zero value, ...) = %q, want empty string", got)
	}

	statuses := []status.ServiceStatus{
		{Name: "k3s", State: "running"},
		{Name: "kube-ovn", State: "not ready"},
	}
	hs := status.HostStats{CPUPercent: 12, CPUKnown: true}
	got := RenderHomeStatus(statuses, hs, time.Time{}, wideEnoughToNeverWrap)
	want := "Status\n" +
		WrapIndented("● k3s   ● kube-ovn", 2, wideEnoughToNeverWrap) + "\n" +
		WrapIndented("CPU 12%", 2, wideEnoughToNeverWrap) + "\n\n"
	if got != want {
		t.Errorf("RenderHomeStatus(...) = %q, want %q", got, want)
	}

	// Services alone (System not known yet) still renders, and vice versa.
	wantSvcOnly := "Status\n" + WrapIndented("● k3s   ● kube-ovn", 2, wideEnoughToNeverWrap) + "\n\n"
	if got := RenderHomeStatus(statuses, status.HostStats{}, time.Time{}, wideEnoughToNeverWrap); got != wantSvcOnly {
		t.Errorf("RenderHomeStatus(statuses, zero value, ...) = %q, want services-only block %q", got, wantSvcOnly)
	}
	wantSysOnly := "Status\n" + WrapIndented("CPU 12%", 2, wideEnoughToNeverWrap) + "\n\n"
	if got := RenderHomeStatus(nil, hs, time.Time{}, wideEnoughToNeverWrap); got != wantSysOnly {
		t.Errorf("RenderHomeStatus(nil, hs, ...) = %q, want system-only block %q", got, wantSysOnly)
	}
}

// TestRenderHomeStatusWrapsOnNarrowTerminal is a regression test: a long
// combined status line used to just run past the terminal edge on a narrow
// SSH window instead of wrapping.
func TestRenderHomeStatusWrapsOnNarrowTerminal(t *testing.T) {
	statuses := []status.ServiceStatus{
		{Name: "k3s", State: "running"},
		{Name: "kube-ovn", State: "running"},
		{Name: "kubevirt", State: "running"},
		{Name: "cdi", State: "running"},
		{Name: "stabilizer", State: "not ready"},
	}
	got := RenderHomeStatus(statuses, status.HostStats{}, time.Time{}, 40)
	if strings.Count(got, "\n") < 3 {
		t.Errorf("RenderHomeStatus at width 40 didn't wrap onto multiple lines:\n%q", got)
	}
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if l := len([]rune(line)); l > 40 {
			t.Errorf("line %q is %d runes wide, want <= 40", line, l)
		}
	}
}

func TestFormatAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{3 * time.Second, "3s"},
		{90 * time.Second, "1m30s"},
		{-2 * time.Second, "0s"},
	}
	for _, c := range cases {
		if got := formatAge(c.d); got != c.want {
			t.Errorf("formatAge(%v) = %q, want %q", c.d, got, c.want)
		}
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

func TestFormatDiskUsage(t *testing.T) {
	cases := []struct {
		free, total float64
		want        string
	}{
		{412, 1863, "1.4Ti used/1.8Ti total - 78% (412Gi free)"},
		{1843.2, 2048, "205Gi used/2.0Ti total - 10% (1.8Ti free)"},
		{0, 0, "0Gi used/0Gi total - 0% (0Gi free)"},
		{500, 10569, "9.8Ti used/10.3Ti total - 95% (500Gi free)"},
	}
	for _, c := range cases {
		if got := formatDiskUsage(c.free, c.total); got != c.want {
			t.Errorf("formatDiskUsage(%v, %v) = %q, want %q", c.free, c.total, got, c.want)
		}
	}
}

func TestHostStatsLine(t *testing.T) {
	if got := hostStatsLine(status.HostStats{}); got != "" {
		t.Errorf("hostStatsLine(zero value) = %q, want empty string", got)
	}

	hs := status.HostStats{
		CPUPercent: 12, CPUKnown: true,
		MemUsedGiB: 28.4, MemTotalGiB: 62.7, MemPercent: 45, MemKnown: true,
		DiskFreeGiB: 412, DiskTotalGiB: 1863, DiskKnown: true,
		VMRunning: 3, VMTotal: 5, VMKnown: true,
	}
	got := hostStatsLine(hs)
	want := "CPU 12%   Mem 45% (28 GiB / 63 GiB)   Disk 1.4Ti used/1.8Ti total - 78% (412Gi free)   VMs 3 running / 5 total"
	if got != want {
		t.Errorf("hostStatsLine(...) = %q, want %q", got, want)
	}
}
