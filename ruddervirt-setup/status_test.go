// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"strings"
	"testing"
	"time"
)

func TestServiceStatusLine(t *testing.T) {
	if got := serviceStatusLine(nil); got != "" {
		t.Errorf("serviceStatusLine(nil) = %q, want empty string", got)
	}
	if got := serviceStatusLine([]serviceStatus{}); got != "" {
		t.Errorf("serviceStatusLine(empty) = %q, want empty string", got)
	}

	got := serviceStatusLine([]serviceStatus{
		{name: "k3s", state: "running"},
		{name: "kube-ovn", state: "not ready"},
	})
	want := "● k3s   ● kube-ovn"
	if got != want {
		t.Errorf("serviceStatusLine(...) = %q, want %q", got, want)
	}
}

func TestRenderHomeStatus(t *testing.T) {
	const wideEnoughToNeverWrap = 200

	if got := renderHomeStatus(nil, hostStats{}, time.Time{}, wideEnoughToNeverWrap); got != "" {
		t.Errorf("renderHomeStatus(nil, zero value, ...) = %q, want empty string", got)
	}

	statuses := []serviceStatus{
		{name: "k3s", state: "running"},
		{name: "kube-ovn", state: "not ready"},
	}
	hs := hostStats{cpuPercent: 12, cpuKnown: true}
	got := renderHomeStatus(statuses, hs, time.Time{}, wideEnoughToNeverWrap)
	want := "Status\n" +
		wrapIndented("● k3s   ● kube-ovn", 2, wideEnoughToNeverWrap) + "\n" +
		wrapIndented("CPU 12%", 2, wideEnoughToNeverWrap) + "\n\n"
	if got != want {
		t.Errorf("renderHomeStatus(...) = %q, want %q", got, want)
	}

	// Services alone (System not known yet) still renders, and vice versa.
	wantSvcOnly := "Status\n" + wrapIndented("● k3s   ● kube-ovn", 2, wideEnoughToNeverWrap) + "\n\n"
	if got := renderHomeStatus(statuses, hostStats{}, time.Time{}, wideEnoughToNeverWrap); got != wantSvcOnly {
		t.Errorf("renderHomeStatus(statuses, zero value, ...) = %q, want services-only block %q", got, wantSvcOnly)
	}
	wantSysOnly := "Status\n" + wrapIndented("CPU 12%", 2, wideEnoughToNeverWrap) + "\n\n"
	if got := renderHomeStatus(nil, hs, time.Time{}, wideEnoughToNeverWrap); got != wantSysOnly {
		t.Errorf("renderHomeStatus(nil, hs, ...) = %q, want system-only block %q", got, wantSysOnly)
	}
}

// TestRenderHomeStatusWrapsOnNarrowTerminal is a regression test: a long
// combined status line used to just run past the terminal edge on a narrow
// SSH window instead of wrapping.
func TestRenderHomeStatusWrapsOnNarrowTerminal(t *testing.T) {
	statuses := []serviceStatus{
		{name: "k3s", state: "running"},
		{name: "kube-ovn", state: "running"},
		{name: "kubevirt", state: "running"},
		{name: "cdi", state: "running"},
		{name: "stabilizer", state: "not ready"},
	}
	got := renderHomeStatus(statuses, hostStats{}, time.Time{}, 40)
	if strings.Count(got, "\n") < 3 {
		t.Errorf("renderHomeStatus at width 40 didn't wrap onto multiple lines:\n%q", got)
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

func TestReadyState(t *testing.T) {
	if got := readyState(true); got != "ready" {
		t.Errorf("readyState(true) = %q, want %q", got, "ready")
	}
	if got := readyState(false); got != "not ready" {
		t.Errorf("readyState(false) = %q, want %q", got, "not ready")
	}
}
