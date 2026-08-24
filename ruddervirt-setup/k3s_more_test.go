// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// drainStrings collects every stepOutputMsg sent to ch without blocking the
// caller - callers still hold ch open, so this only reads what's already
// buffered.
func drainStrings(ch chan tea.Msg) []string {
	var out []string
	for {
		select {
		case msg := <-ch:
			if s, ok := msg.(stepOutputMsg); ok {
				out = append(out, string(s))
			}
		default:
			return out
		}
	}
}

func TestApplyStorageEngine(t *testing.T) {
	t.Run("unknown engine errors without touching the runner", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			t.Errorf("unexpected command for unknown engine: %s %v", name, args)
			return commandOutcome{}
		}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = applyStorageEngine(ch, "/usr/local/bin/kubectl", "made-up-engine") })
		if err == nil {
			t.Fatal("applyStorageEngine(made-up-engine) err = nil, want an error")
		}
	})

	t.Run("longhorn dispatches to applyGenericStorageEngine", func(t *testing.T) {
		r := &fakeRunner{}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = applyStorageEngine(ch, "/usr/local/bin/kubectl", "longhorn") })
		if err != nil {
			t.Fatalf("applyStorageEngine(longhorn) err = %v, want nil", err)
		}
		lines := strings.Join(drainStrings(ch), "\n")
		if !strings.Contains(lines, "Applying longhorn") {
			t.Errorf("output = %q, want it to mention applying longhorn", lines)
		}
	})
}

func TestApplyRookCeph(t *testing.T) {
	t.Run("success path applies, waits for the CRD, then waits for Ready", func(t *testing.T) {
		r := &fakeRunner{}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = applyRookCeph(ch, "/usr/local/bin/kubectl") })
		if err != nil {
			t.Fatalf("applyRookCeph() err = %v, want nil", err)
		}
	})

	t.Run("apply failure short-circuits before any wait", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "apply", "-k") {
				return commandOutcome{err: errFake}
			}
			return commandOutcome{}
		}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = applyRookCeph(ch, "/usr/local/bin/kubectl") })
		if err == nil {
			t.Fatal("applyRookCeph() err = nil, want an error")
		}
		for _, c := range r.calls {
			if cmdContains("", strings.Fields(c), "cephcluster") {
				t.Errorf("expected no cephcluster wait after apply failed, but saw call %q", c)
			}
		}
	})
}

func TestApplyGenericStorageEngine(t *testing.T) {
	t.Run("success path", func(t *testing.T) {
		r := &fakeRunner{}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() {
			err = applyGenericStorageEngine(ch, "/usr/local/bin/kubectl", "openebs", "openebs", "local.csi.openebs.io")
		})
		if err != nil {
			t.Fatalf("applyGenericStorageEngine() err = %v, want nil", err)
		}
	})

	t.Run("apply failure short-circuits before CSI-driver polling", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "apply", "-k") {
				return commandOutcome{err: errFake}
			}
			return commandOutcome{}
		}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() {
			err = applyGenericStorageEngine(ch, "/usr/local/bin/kubectl", "longhorn", "longhorn-system", "driver.longhorn.io")
		})
		if err == nil {
			t.Fatal("applyGenericStorageEngine() err = nil, want an error")
		}
		for _, c := range r.calls {
			if cmdContains("", strings.Fields(c), "csidriver") {
				t.Errorf("expected no CSI-driver poll after apply failed, but saw call %q", c)
			}
		}
	})
}

func TestWaitForKubeOvnHealthy(t *testing.T) {
	t.Run("success path waits for every core workload", func(t *testing.T) {
		r := &fakeRunner{}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = waitForKubeOvnHealthy(ch, "/usr/local/bin/kubectl") })
		if err != nil {
			t.Fatalf("waitForKubeOvnHealthy() err = %v, want nil", err)
		}
		for _, w := range kubeOvnCoreWorkloads {
			found := false
			for _, c := range r.calls {
				if cmdContains("", strings.Fields(c), "rollout", "status", w.kind+"/"+w.name) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected a rollout-status check for %s/%s, calls = %v", w.kind, w.name, r.calls)
			}
		}
	})

	t.Run("first workload failing to roll out stops the wait immediately", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "rollout", "status", "ovs-ovn") {
				return commandOutcome{err: errFake}
			}
			return commandOutcome{}
		}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = waitForKubeOvnHealthy(ch, "/usr/local/bin/kubectl") })
		if err == nil {
			t.Fatal("waitForKubeOvnHealthy() err = nil, want an error")
		}
		for _, c := range r.calls {
			if cmdContains("", strings.Fields(c), "kube-ovn-cni") {
				t.Errorf("expected no check for later workloads after ovs-ovn failed, but saw call %q", c)
			}
		}
	})
}
