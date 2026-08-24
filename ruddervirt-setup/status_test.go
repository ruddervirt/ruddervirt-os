package main

import "testing"

func TestRenderServiceStatuses(t *testing.T) {
	if got := renderServiceStatuses(nil); got != "" {
		t.Errorf("renderServiceStatuses(nil) = %q, want empty string", got)
	}
	if got := renderServiceStatuses([]serviceStatus{}); got != "" {
		t.Errorf("renderServiceStatuses(empty) = %q, want empty string", got)
	}

	got := renderServiceStatuses([]serviceStatus{
		{name: "k3s", state: "running"},
		{name: "kube-ovn", state: "not ready"},
	})
	want := "Services:\n  k3s       running\n  kube-ovn  not ready\n\n"
	if got != want {
		t.Errorf("renderServiceStatuses(...) = %q, want %q", got, want)
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
