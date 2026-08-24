package main

import (
	"testing"
	"time"
)

func TestRenderServiceStatuses(t *testing.T) {
	if got := renderServiceStatuses(nil, time.Time{}); got != "" {
		t.Errorf("renderServiceStatuses(nil, ...) = %q, want empty string", got)
	}
	if got := renderServiceStatuses([]serviceStatus{}, time.Time{}); got != "" {
		t.Errorf("renderServiceStatuses(empty, ...) = %q, want empty string", got)
	}

	got := renderServiceStatuses([]serviceStatus{
		{name: "k3s", state: "running"},
		{name: "kube-ovn", state: "not ready"},
	}, time.Time{})
	want := "Services\n  ● k3s       running\n  ● kube-ovn  not ready\n\n"
	if got != want {
		t.Errorf("renderServiceStatuses(...) = %q, want %q", got, want)
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
