package main

import (
	"reflect"
	"strings"
	"testing"
)

// cmdContains reports whether every one of substrs appears somewhere in
// name+args joined by spaces - used by fetchServiceStatuses' fake respond
// functions below to key off a command's shape without hardcoding the
// exact sudo/-n wrapping runNonInteractive applies.
func cmdContains(name string, args []string, substrs ...string) bool {
	line := strings.Join(append([]string{name}, args...), " ")
	for _, s := range substrs {
		if !strings.Contains(line, s) {
			return false
		}
	}
	return true
}

// hasField reports whether tok appears as an exact space-separated token in
// line - unlike cmdContains, which does substring matching and so would
// wrongly match "add" inside "ipv4.addresses".
func hasField(line, tok string) bool {
	for _, f := range strings.Fields(line) {
		if f == tok {
			return true
		}
	}
	return false
}

// withConfigSaved swaps configSaved for the duration of fn, always
// restoring the original afterward - fetchServiceStatuses tests use this
// instead of depending on the real /etc/ruddervirt path existing.
func withConfigSaved(saved bool, fn func()) {
	orig := configSaved
	configSaved = func() bool { return saved }
	defer func() { configSaved = orig }()
	fn()
}

func TestFetchServiceStatuses(t *testing.T) {
	t.Run("never configured reports nothing without touching the runner", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			t.Errorf("unexpected command on an unconfigured system: %s %v", name, args)
			return commandOutcome{}
		}}
		cfg := Config{Storage: StorageConfig{Engine: "openebs"}}
		var got []serviceStatus
		withConfigSaved(false, func() {
			withFakeRunner(r, func() { got = fetchServiceStatuses(cfg) })
		})
		if got != nil {
			t.Errorf("fetchServiceStatuses() on an unconfigured system = %+v, want nil", got)
		}
	})

	allUnknown := func(cfg Config) []serviceStatus {
		engine := cfg.Storage.Engine
		names := []string{"k3s", "kube-ovn", "storage (" + engine + ")", "kubevirt", "aileron"}
		out := make([]serviceStatus, len(names))
		for i, n := range names {
			out[i] = serviceStatus{name: n, state: "unknown"}
		}
		return out
	}

	t.Run("no cached sudo ticket reports unknown for everything", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "sudo", "-n", "true") {
				return commandOutcome{err: errFake}
			}
			return commandOutcome{}
		}}
		cfg := Config{Storage: StorageConfig{Engine: "openebs"}}
		var got []serviceStatus
		withConfigSaved(true, func() {
			withFakeRunner(r, func() { got = fetchServiceStatuses(cfg) })
		})
		if !reflect.DeepEqual(got, allUnknown(cfg)) {
			t.Errorf("fetchServiceStatuses() = %+v, want %+v", got, allUnknown(cfg))
		}
	})

	t.Run("k3s not running reports not running for everything", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "systemctl", "is-active") {
				return commandOutcome{err: errFake}
			}
			return commandOutcome{}
		}}
		cfg := Config{Storage: StorageConfig{Engine: "openebs"}}
		var got []serviceStatus
		withConfigSaved(true, func() {
			withFakeRunner(r, func() { got = fetchServiceStatuses(cfg) })
		})
		want := []serviceStatus{
			{name: "k3s", state: "not running"},
			{name: "kube-ovn", state: "not running"},
			{name: "storage (openebs)", state: "not running"},
			{name: "kubevirt", state: "not running"},
			{name: "aileron", state: "not running"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("fetchServiceStatuses() = %+v, want %+v", got, want)
		}
	})

	t.Run("kube-ovn still rolling out is reflected while everything else is ready", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "rollout", "status", "ovs-ovn") {
				return commandOutcome{err: errFake}
			}
			return commandOutcome{}
		}}
		cfg := Config{Storage: StorageConfig{Engine: "openebs"}}
		var got []serviceStatus
		withConfigSaved(true, func() {
			withFakeRunner(r, func() { got = fetchServiceStatuses(cfg) })
		})
		want := []serviceStatus{
			{name: "k3s", state: "running"},
			{name: "kube-ovn", state: "not ready"},
			{name: "storage (openebs)", state: "ready"},
			{name: "kubevirt", state: "ready"},
			{name: "aileron", state: "ready"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("fetchServiceStatuses() = %+v, want %+v", got, want)
		}
	})

	t.Run("everything ready", func(t *testing.T) {
		r := &fakeRunner{}
		cfg := Config{Storage: StorageConfig{Engine: "rook-ceph"}}
		var got []serviceStatus
		withConfigSaved(true, func() {
			withFakeRunner(r, func() { got = fetchServiceStatuses(cfg) })
		})
		want := []serviceStatus{
			{name: "k3s", state: "running"},
			{name: "kube-ovn", state: "ready"},
			{name: "storage (rook-ceph)", state: "ready"},
			{name: "kubevirt", state: "ready"},
			{name: "aileron", state: "ready"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("fetchServiceStatuses() = %+v, want %+v", got, want)
		}
	})
}
