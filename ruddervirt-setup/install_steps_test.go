// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"testing"

	"ruddervirt-setup/internal/status"
)

func TestAllServicesReady(t *testing.T) {
	tests := []struct {
		name     string
		statuses []status.ServiceStatus
		want     bool
	}{
		{"nil is not ready", nil, false},
		{"empty is not ready", []status.ServiceStatus{}, false},
		{
			"all healthy",
			[]status.ServiceStatus{
				{Name: "k3s", State: "running"},
				{Name: "kube-ovn", State: "ready"},
				{Name: "aileron", State: "ready"},
			},
			true,
		},
		{
			"one not ready",
			[]status.ServiceStatus{
				{Name: "k3s", State: "running"},
				{Name: "kube-ovn", State: "not ready"},
			},
			false,
		},
		{
			"k3s not running blocks even if others say ready",
			[]status.ServiceStatus{
				{Name: "k3s", State: "not running"},
				{Name: "kube-ovn", State: "ready"},
			},
			false,
		},
		{
			"unknown state blocks",
			[]status.ServiceStatus{
				{Name: "k3s", State: "unknown"},
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
	got := notReadySummary([]status.ServiceStatus{
		{Name: "k3s", State: "running"},
		{Name: "kube-ovn", State: "not ready"},
		{Name: "aileron", State: "unknown"},
	})
	want := "kube-ovn=not ready, aileron=unknown"
	if got != want {
		t.Errorf("notReadySummary(...) = %q, want %q", got, want)
	}

	if got := notReadySummary(nil); got != "" {
		t.Errorf("notReadySummary(nil) = %q, want empty string", got)
	}
}
