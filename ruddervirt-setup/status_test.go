// SPDX-License-Identifier: GPL-3.0-only

package main

import (
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
	if got := renderHomeStatus(nil, hostStats{}, time.Time{}); got != "" {
		t.Errorf("renderHomeStatus(nil, zero value, ...) = %q, want empty string", got)
	}

	statuses := []serviceStatus{
		{name: "k3s", state: "running"},
		{name: "kube-ovn", state: "not ready"},
	}
	hs := hostStats{cpuPercent: 12, cpuKnown: true}
	got := renderHomeStatus(statuses, hs, time.Time{})
	want := "Status\n  ● k3s   ● kube-ovn\n  CPU 12%\n\n"
	if got != want {
		t.Errorf("renderHomeStatus(...) = %q, want %q", got, want)
	}

	// Services alone (System not known yet) still renders, and vice versa.
	if got := renderHomeStatus(statuses, hostStats{}, time.Time{}); got != "Status\n  ● k3s   ● kube-ovn\n\n" {
		t.Errorf("renderHomeStatus(statuses, zero value, ...) = %q, want services-only block", got)
	}
	if got := renderHomeStatus(nil, hs, time.Time{}); got != "Status\n  CPU 12%\n\n" {
		t.Errorf("renderHomeStatus(nil, hs, ...) = %q, want system-only block", got)
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
