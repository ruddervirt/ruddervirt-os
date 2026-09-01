// SPDX-License-Identifier: GPL-3.0-only

package status

import (
	"reflect"
	"testing"

	"ruddervirt-setup/internal/exec/exectest"
	"ruddervirt-setup/internal/storage"
)

func TestReadyState(t *testing.T) {
	if got := readyState(true); got != "ready" {
		t.Errorf("readyState(true) = %q, want %q", got, "ready")
	}
	if got := readyState(false); got != "not ready" {
		t.Errorf("readyState(false) = %q, want %q", got, "not ready")
	}
}

func noStabilizer() bool { return false }

func TestFetchServiceStatuses(t *testing.T) {
	t.Run("never configured reports nothing without touching the runner", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			t.Errorf("unexpected command on an unconfigured system: %s %v", name, args)
			return exectest.Outcome{}
		}}
		var got []ServiceStatus
		exectest.WithFakeRunner(r, func() { got = FetchServiceStatuses("openebs", func() bool { return false }, noStabilizer) })
		if got != nil {
			t.Errorf("FetchServiceStatuses() on an unconfigured system = %+v, want nil", got)
		}
	})

	configured := func() bool { return true }

	allUnknown := func(engine string) []ServiceStatus {
		names := []string{"k3s", "kube-ovn", "storage (" + engine + ")", "kubevirt", "aileron"}
		out := make([]ServiceStatus, len(names))
		for i, n := range names {
			out[i] = ServiceStatus{Name: n, State: "unknown"}
		}
		return out
	}

	t.Run("no cached sudo ticket reports unknown for everything", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "sudo", "-n", "true") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		var got []ServiceStatus
		exectest.WithFakeRunner(r, func() { got = FetchServiceStatuses("openebs", configured, noStabilizer) })
		if !reflect.DeepEqual(got, allUnknown("openebs")) {
			t.Errorf("FetchServiceStatuses() = %+v, want %+v", got, allUnknown("openebs"))
		}
	})

	t.Run("k3s not running reports not running for everything", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "systemctl", "is-active") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		var got []ServiceStatus
		exectest.WithFakeRunner(r, func() { got = FetchServiceStatuses("openebs", configured, noStabilizer) })
		want := []ServiceStatus{
			{Name: "k3s", State: "not running"},
			{Name: "kube-ovn", State: "not running"},
			{Name: "storage (openebs)", State: "not running"},
			{Name: "kubevirt", State: "not running"},
			{Name: "aileron", State: "not running"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("FetchServiceStatuses() = %+v, want %+v", got, want)
		}
	})

	t.Run("kube-ovn still rolling out is reflected while everything else is ready", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "rollout", "status", "ovs-ovn") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		var got []ServiceStatus
		exectest.WithFakeRunner(r, func() { got = FetchServiceStatuses("openebs", configured, noStabilizer) })
		want := []ServiceStatus{
			{Name: "k3s", State: "running"},
			{Name: "kube-ovn", State: "not ready"},
			{Name: "storage (openebs)", State: "ready"},
			{Name: "kubevirt", State: "ready"},
			{Name: "aileron", State: "ready"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("FetchServiceStatuses() = %+v, want %+v", got, want)
		}
	})

	t.Run("everything ready", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		var got []ServiceStatus
		exectest.WithFakeRunner(r, func() { got = FetchServiceStatuses("rook-ceph", configured, noStabilizer) })
		want := []ServiceStatus{
			{Name: "k3s", State: "running"},
			{Name: "kube-ovn", State: "ready"},
			{Name: "storage (rook-ceph)", State: "ready"},
			{Name: "kubevirt", State: "ready"},
			{Name: "aileron", State: "ready"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("FetchServiceStatuses() = %+v, want %+v", got, want)
		}
	})

	t.Run("stabilizer-managed install adds a stabilizer row", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		var got []ServiceStatus
		exectest.WithFakeRunner(r, func() { got = FetchServiceStatuses("rook-ceph", configured, func() bool { return true }) })
		want := []ServiceStatus{
			{Name: "k3s", State: "running"},
			{Name: "kube-ovn", State: "ready"},
			{Name: "storage (rook-ceph)", State: "ready"},
			{Name: "kubevirt", State: "ready"},
			{Name: "aileron", State: "ready"},
			{Name: "stabilizer", State: "ready"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("FetchServiceStatuses() = %+v, want %+v", got, want)
		}
	})

	t.Run("plain self-hosted install (no stabilizer) omits the stabilizer row entirely", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "-n", "ruddervirt-system", "wait", "deployment.apps/stabilizer") {
				t.Errorf("unexpected wait on deployment.apps/stabilizer with no stabilizer HelmChart present: %s %v", name, args)
			}
			return exectest.Outcome{}
		}}
		var got []ServiceStatus
		exectest.WithFakeRunner(r, func() { got = FetchServiceStatuses("rook-ceph", configured, noStabilizer) })
		for _, st := range got {
			if st.Name == "stabilizer" {
				t.Errorf("FetchServiceStatuses() = %+v, want no stabilizer row", got)
			}
		}
	})
}

func TestCephClusterCapacity(t *testing.T) {
	t.Run("reports available and total bytes", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "cephcluster", "-n", "rook-ceph") {
				return exectest.Outcome{Out: []byte("107374182400 214748364800")}
			}
			return exectest.Outcome{}
		}}
		var free, total float64
		var ok bool
		exectest.WithFakeRunner(r, func() { free, total, ok = cephClusterCapacity("/usr/local/bin/kubectl") })
		if !ok || free != 100 || total != 200 {
			t.Errorf("cephClusterCapacity() = (%v, %v, %v), want (100, 200, true)", free, total, ok)
		}
	})

	t.Run("capacity not yet populated reports unknown", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte(" ")}
		}}
		var ok bool
		exectest.WithFakeRunner(r, func() { _, _, ok = cephClusterCapacity("/usr/local/bin/kubectl") })
		if ok {
			t.Errorf("cephClusterCapacity() ok = true, want false")
		}
	})

	t.Run("kubectl error reports unknown", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		var ok bool
		exectest.WithFakeRunner(r, func() { _, _, ok = cephClusterCapacity("/usr/local/bin/kubectl") })
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
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte(body)}
		}}
		var free, total float64
		var ok bool
		exectest.WithFakeRunner(r, func() { free, total, ok = longhornCapacity("/usr/local/bin/kubectl") })
		if !ok || free != 75 || total != 150 {
			t.Errorf("longhornCapacity() = (%v, %v, %v), want (75, 150, true)", free, total, ok)
		}
	})

	t.Run("no nodes reports unknown", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte(`{"items":[]}`)}
		}}
		var ok bool
		exectest.WithFakeRunner(r, func() { _, _, ok = longhornCapacity("/usr/local/bin/kubectl") })
		if ok {
			t.Errorf("longhornCapacity() ok = true, want false")
		}
	})

	t.Run("kubectl error reports unknown", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		var ok bool
		exectest.WithFakeRunner(r, func() { _, _, ok = longhornCapacity("/usr/local/bin/kubectl") })
		if ok {
			t.Errorf("longhornCapacity() ok = true, want false")
		}
	})
}

func TestOpenebsVGCapacity(t *testing.T) {
	t.Run("brand new pool with no data written reports fully free, not fully used", func(t *testing.T) {
		// prepareOpenEBSDevice allocates the thin pool as 95%VG up front, so
		// vg_free/vg_size would read ~5% free on a new install regardless of
		// actual data written - the bug this test guards against.
		// data_percent should drive the free/used split instead.
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "lvs", storage.OpenebsVGName+"/"+storage.OpenebsThinPoolLV) {
				return exectest.Outcome{Out: []byte("  214748364800  0.00\n")}
			}
			return exectest.Outcome{}
		}}
		var free, total float64
		var ok bool
		exectest.WithFakeRunner(r, func() { free, total, ok = openebsVGCapacity() })
		if !ok || free != 200 || total != 200 {
			t.Errorf("openebsVGCapacity() = (%v, %v, %v), want (200, 200, true)", free, total, ok)
		}
	})

	t.Run("reports free proportional to data_percent", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "lvs", storage.OpenebsVGName+"/"+storage.OpenebsThinPoolLV) {
				return exectest.Outcome{Out: []byte("  214748364800  25.00\n")}
			}
			return exectest.Outcome{}
		}}
		var free, total float64
		var ok bool
		exectest.WithFakeRunner(r, func() { free, total, ok = openebsVGCapacity() })
		if !ok || free != 150 || total != 200 {
			t.Errorf("openebsVGCapacity() = (%v, %v, %v), want (150, 200, true)", free, total, ok)
		}
	})

	t.Run("lvs error (thin pool missing) reports unknown", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		var ok bool
		exectest.WithFakeRunner(r, func() { _, _, ok = openebsVGCapacity() })
		if ok {
			t.Errorf("openebsVGCapacity() ok = true, want false")
		}
	})
}

func TestStorageEngineCapacityUnknownEngine(t *testing.T) {
	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		t.Errorf("unexpected command for an unknown engine: %s %v", name, args)
		return exectest.Outcome{}
	}}
	var ok bool
	exectest.WithFakeRunner(r, func() { _, _, ok = storageEngineCapacity("/usr/local/bin/kubectl", "unknown-engine") })
	if ok {
		t.Errorf("storageEngineCapacity() ok = true, want false")
	}
}

func TestAileronReady(t *testing.T) {
	t.Run("deployment available", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "-n", "ruddervirt-system", "wait", "deployment.apps/aileron") {
				return exectest.Outcome{}
			}
			t.Errorf("unexpected command: %s %v", name, args)
			return exectest.Outcome{}
		}}
		var ready bool
		exectest.WithFakeRunner(r, func() { ready = AileronReady("/usr/local/bin/kubectl") })
		if !ready {
			t.Errorf("AileronReady() = false, want true")
		}
	})

	t.Run("deployment not available", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		var ready bool
		exectest.WithFakeRunner(r, func() { ready = AileronReady("/usr/local/bin/kubectl") })
		if ready {
			t.Errorf("AileronReady() = true, want false")
		}
	})
}
