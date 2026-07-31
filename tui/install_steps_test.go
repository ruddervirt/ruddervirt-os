package main

import "testing"

func TestAllServicesReady(t *testing.T) {
	tests := []struct {
		name     string
		statuses []serviceStatus
		want     bool
	}{
		{"nil is not ready", nil, false},
		{"empty is not ready", []serviceStatus{}, false},
		{
			"all healthy",
			[]serviceStatus{
				{name: "k3s", state: "running"},
				{name: "kube-ovn", state: "ready"},
				{name: "aileron", state: "ready"},
			},
			true,
		},
		{
			"one not ready",
			[]serviceStatus{
				{name: "k3s", state: "running"},
				{name: "kube-ovn", state: "not ready"},
			},
			false,
		},
		{
			"k3s not running blocks even if others say ready",
			[]serviceStatus{
				{name: "k3s", state: "not running"},
				{name: "kube-ovn", state: "ready"},
			},
			false,
		},
		{
			"unknown state blocks",
			[]serviceStatus{
				{name: "k3s", state: "unknown"},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allServicesReady(tt.statuses); got != tt.want {
				t.Errorf("allServicesReady(%v) = %v, want %v", tt.statuses, got, tt.want)
			}
		})
	}
}

func TestNotReadySummary(t *testing.T) {
	got := notReadySummary([]serviceStatus{
		{name: "k3s", state: "running"},
		{name: "kube-ovn", state: "not ready"},
		{name: "aileron", state: "unknown"},
	})
	want := "kube-ovn=not ready, aileron=unknown"
	if got != want {
		t.Errorf("notReadySummary(...) = %q, want %q", got, want)
	}

	if got := notReadySummary(nil); got != "" {
		t.Errorf("notReadySummary(nil) = %q, want empty string", got)
	}
}
