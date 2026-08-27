// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

	t.Run("stabilizer-managed install adds a stabilizer row", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "get", "helmchart", "stabilizer") {
				return commandOutcome{out: []byte("helmchart.helm.cattle.io/stabilizer")}
			}
			return commandOutcome{}
		}}
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
			{name: "stabilizer", state: "ready"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("fetchServiceStatuses() = %+v, want %+v", got, want)
		}
	})

	t.Run("plain self-hosted install (no stabilizer) omits the stabilizer row entirely", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "-n", "ruddervirt-system", "wait", "deployment.apps/stabilizer") {
				t.Errorf("unexpected wait on deployment.apps/stabilizer with no stabilizer HelmChart present: %s %v", name, args)
			}
			return commandOutcome{}
		}}
		cfg := Config{Storage: StorageConfig{Engine: "rook-ceph"}}
		var got []serviceStatus
		withConfigSaved(true, func() {
			withFakeRunner(r, func() { got = fetchServiceStatuses(cfg) })
		})
		for _, st := range got {
			if st.name == "stabilizer" {
				t.Errorf("fetchServiceStatuses() = %+v, want no stabilizer row", got)
			}
		}
	})
}

// TestFetchServiceStatusesCmdDropsStorage confirms the storage row is
// filtered out of what reaches the home screen (fetchServiceStatusesCmd),
// while still being computed by fetchServiceStatuses itself - the install
// pipeline's completion gate (waitForServicesReadyStep) calls that
// directly and still needs the real signal.
func TestFetchServiceStatusesCmdDropsStorage(t *testing.T) {
	r := &fakeRunner{}
	cfg := Config{Storage: StorageConfig{Engine: "rook-ceph"}}
	var msg tea.Msg
	withConfigSaved(true, func() {
		withFakeRunner(r, func() { msg = fetchServiceStatusesCmd(cfg)() })
	})
	got := msg.(serviceStatusMsg).statuses
	want := []serviceStatus{
		{name: "k3s", state: "running"},
		{name: "kube-ovn", state: "ready"},
		{name: "kubevirt", state: "ready"},
		{name: "aileron", state: "ready"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fetchServiceStatusesCmd()() = %+v, want %+v", got, want)
	}
}

func TestCephClusterCapacity(t *testing.T) {
	t.Run("reports available and total bytes", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "cephcluster", "-n", "rook-ceph") {
				return commandOutcome{out: []byte("107374182400 214748364800")}
			}
			return commandOutcome{}
		}}
		var free, total float64
		var ok bool
		withFakeRunner(r, func() { free, total, ok = cephClusterCapacity("/usr/local/bin/kubectl") })
		if !ok || free != 100 || total != 200 {
			t.Errorf("cephClusterCapacity() = (%v, %v, %v), want (100, 200, true)", free, total, ok)
		}
	})

	t.Run("capacity not yet populated reports unknown", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte(" ")}
		}}
		var ok bool
		withFakeRunner(r, func() { _, _, ok = cephClusterCapacity("/usr/local/bin/kubectl") })
		if ok {
			t.Errorf("cephClusterCapacity() ok = true, want false")
		}
	})

	t.Run("kubectl error reports unknown", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{err: errFake}
		}}
		var ok bool
		withFakeRunner(r, func() { _, _, ok = cephClusterCapacity("/usr/local/bin/kubectl") })
		if ok {
			t.Errorf("cephClusterCapacity() ok = true, want false")
		}
	})
}

func TestLonghornCapacity(t *testing.T) {
	t.Run("sums across nodes and disks", func(t *testing.T) {
		body := `{"items":[
			{"status":{"diskStatus":{"disk1":{"storageAvailable":53687091200,"storageMaximum":107374182400}}}},
			{"status":{"diskStatus":{"disk1":{"storageAvailable":26843545600,"storageMaximum":53687091200}}}}
		]}`
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte(body)}
		}}
		var free, total float64
		var ok bool
		withFakeRunner(r, func() { free, total, ok = longhornCapacity("/usr/local/bin/kubectl") })
		if !ok || free != 75 || total != 150 {
			t.Errorf("longhornCapacity() = (%v, %v, %v), want (75, 150, true)", free, total, ok)
		}
	})

	t.Run("no nodes reports unknown", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte(`{"items":[]}`)}
		}}
		var ok bool
		withFakeRunner(r, func() { _, _, ok = longhornCapacity("/usr/local/bin/kubectl") })
		if ok {
			t.Errorf("longhornCapacity() ok = true, want false")
		}
	})

	t.Run("kubectl error reports unknown", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{err: errFake}
		}}
		var ok bool
		withFakeRunner(r, func() { _, _, ok = longhornCapacity("/usr/local/bin/kubectl") })
		if ok {
			t.Errorf("longhornCapacity() ok = true, want false")
		}
	})
}

func TestOpenebsVGCapacity(t *testing.T) {
	t.Run("brand new pool with no data written reports fully free, not fully used", func(t *testing.T) {
		// prepareOpenEBSDevice (storage.go) allocates the thin pool as
		// 95%VG up front - vg_free/vg_size would read this as ~5% free
		// immediately on a new install regardless of actual data written,
		// which is exactly the bug this test guards against. data_percent
		// (how much of the pool's own size is actually written) is what
		// should drive the reported free/used split instead.
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "lvs", openebsVGName+"/"+openebsThinPoolLV) {
				return commandOutcome{out: []byte("  214748364800  0.00\n")}
			}
			return commandOutcome{}
		}}
		var free, total float64
		var ok bool
		withFakeRunner(r, func() { free, total, ok = openebsVGCapacity() })
		if !ok || free != 200 || total != 200 {
			t.Errorf("openebsVGCapacity() = (%v, %v, %v), want (200, 200, true)", free, total, ok)
		}
	})

	t.Run("reports free proportional to data_percent", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "lvs", openebsVGName+"/"+openebsThinPoolLV) {
				return commandOutcome{out: []byte("  214748364800  25.00\n")}
			}
			return commandOutcome{}
		}}
		var free, total float64
		var ok bool
		withFakeRunner(r, func() { free, total, ok = openebsVGCapacity() })
		if !ok || free != 150 || total != 200 {
			t.Errorf("openebsVGCapacity() = (%v, %v, %v), want (150, 200, true)", free, total, ok)
		}
	})

	t.Run("lvs error (thin pool missing) reports unknown", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{err: errFake}
		}}
		var ok bool
		withFakeRunner(r, func() { _, _, ok = openebsVGCapacity() })
		if ok {
			t.Errorf("openebsVGCapacity() ok = true, want false")
		}
	})
}

func TestStorageEngineCapacityUnknownEngine(t *testing.T) {
	r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
		t.Errorf("unexpected command for an unknown engine: %s %v", name, args)
		return commandOutcome{}
	}}
	var ok bool
	withFakeRunner(r, func() { _, _, ok = storageEngineCapacity("/usr/local/bin/kubectl", "unknown-engine") })
	if ok {
		t.Errorf("storageEngineCapacity() ok = true, want false")
	}
}

func TestAileronReady(t *testing.T) {
	t.Run("deployment available", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "-n", "ruddervirt-system", "wait", "deployment.apps/aileron") {
				return commandOutcome{}
			}
			t.Errorf("unexpected command: %s %v", name, args)
			return commandOutcome{}
		}}
		var ready bool
		withFakeRunner(r, func() { ready = aileronReady("/usr/local/bin/kubectl") })
		if !ready {
			t.Errorf("aileronReady() = false, want true")
		}
	})

	t.Run("deployment not available", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{err: errFake}
		}}
		var ready bool
		withFakeRunner(r, func() { ready = aileronReady("/usr/local/bin/kubectl") })
		if ready {
			t.Errorf("aileronReady() = true, want false")
		}
	})
}
