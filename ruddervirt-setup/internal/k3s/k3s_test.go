// SPDX-License-Identifier: GPL-3.0-only

package k3s

import (
	"strings"
	"testing"

	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/exec/exectest"
)

func TestParseInstalledK3sVersionOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"normal two-line output", "k3s version v1.34.5+k3s1 (abcdef12)\ngo version go1.23.4\n", "v1.34.5+k3s1", true},
		{"unparseable version token", "k3s version garbage\n", "", false},
		{"empty output", "", "", false},
		{"no k3s version line", "go version go1.23.4\n", "", false},
	}
	for _, c := range cases {
		got, ok := parseInstalledK3sVersionOutput(c.in)
		if ok != c.ok {
			t.Errorf("%s: parseInstalledK3sVersionOutput(%q) ok = %v, want %v", c.name, c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("%s: parseInstalledK3sVersionOutput(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestParseK3sVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want parsedK3sVersion
		ok   bool
	}{
		{"final release", "v1.34.5+k3s1", parsedK3sVersion{major: 1, minor: 34, patch: 5, build: 1}, true},
		{"release candidate", "v1.34.5-rc1+k3s1", parsedK3sVersion{major: 1, minor: 34, patch: 5, hasRC: true, rc: 1, build: 1}, true},
		{"unparseable", "garbage", parsedK3sVersion{}, false},
		{"missing +k3sN suffix", "v1.34.5", parsedK3sVersion{}, false},
	}
	for _, c := range cases {
		got, ok := ParseK3sVersion(c.in)
		if ok != c.ok {
			t.Errorf("%s: ParseK3sVersion(%q) ok = %v, want %v", c.name, c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("%s: ParseK3sVersion(%q) = %+v, want %+v", c.name, c.in, got, c.want)
		}
	}
}

func TestCompareK3sVersions(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int // sign only
		ok   bool
	}{
		{"equal", "v1.34.5+k3s1", "v1.34.5+k3s1", 0, true},
		{"a newer minor", "v1.35.0+k3s1", "v1.34.5+k3s1", 1, true},
		{"a older patch", "v1.34.4+k3s1", "v1.34.5+k3s1", -1, true},
		{"rc sorts below final of same version", "v1.34.5-rc1+k3s1", "v1.34.5+k3s1", -1, true},
		{"final sorts above rc of same version", "v1.34.5+k3s1", "v1.34.5-rc1+k3s1", 1, true},
		{"higher build sorts above", "v1.34.5+k3s2", "v1.34.5+k3s1", 1, true},
		{"unparseable a", "garbage", "v1.34.5+k3s1", 0, false},
		{"unparseable b", "v1.34.5+k3s1", "garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareK3sVersions(c.a, c.b)
		if ok != c.ok {
			t.Errorf("%s: CompareK3sVersions(%q, %q) ok = %v, want %v", c.name, c.a, c.b, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		sign := 0
		if got > 0 {
			sign = 1
		} else if got < 0 {
			sign = -1
		}
		if sign != c.want {
			t.Errorf("%s: CompareK3sVersions(%q, %q) = %d (sign %d), want sign %d", c.name, c.a, c.b, got, sign, c.want)
		}
	}
}

// testOutputMsg stands in for package main's stepOutputMsg, which isn't
// visible here, supplied via the wrap parameter step functions take.
type testOutputMsg string

func testWrap(l string) exec.StepMsg { return testOutputMsg(l) }

// drainStrings collects every testOutputMsg sent to ch without blocking -
// callers still hold ch open, so this only reads what's already buffered.
func drainStrings(ch chan exec.StepMsg) []string {
	var out []string
	for {
		select {
		case msg := <-ch:
			if s, ok := msg.(testOutputMsg); ok {
				out = append(out, string(s))
			}
		default:
			return out
		}
	}
}

func TestApplyStorageEngine(t *testing.T) {
	t.Run("unknown engine errors without touching the runner", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			t.Errorf("unexpected command for unknown engine: %s %v", name, args)
			return exectest.Outcome{}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() {
			err = applyStorageEngine(ch, testWrap, func(string, []byte) error { return nil }, "/usr/local/bin/kubectl", "made-up-engine")
		})
		if err == nil {
			t.Fatal("applyStorageEngine(made-up-engine) err = nil, want an error")
		}
	})

	t.Run("longhorn dispatches to applyGenericStorageEngine", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() {
			err = applyStorageEngine(ch, testWrap, func(string, []byte) error { return nil }, "/usr/local/bin/kubectl", "longhorn")
		})
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
		r := &exectest.FakeRunner{}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = applyRookCeph(ch, testWrap, "/usr/local/bin/kubectl") })
		if err != nil {
			t.Fatalf("applyRookCeph() err = %v, want nil", err)
		}
	})

	t.Run("apply failure short-circuits before any wait", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "apply", "-k") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = applyRookCeph(ch, testWrap, "/usr/local/bin/kubectl") })
		if err == nil {
			t.Fatal("applyRookCeph() err = nil, want an error")
		}
		for _, c := range r.Calls {
			if exectest.CmdContains("", strings.Fields(c), "cephcluster") {
				t.Errorf("expected no cephcluster wait after apply failed, but saw call %q", c)
			}
		}
	})
}

func TestApplyGenericStorageEngine(t *testing.T) {
	t.Run("success path", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() {
			err = applyGenericStorageEngine(ch, testWrap, "/usr/local/bin/kubectl", "openebs", "openebs", "local.csi.openebs.io")
		})
		if err != nil {
			t.Fatalf("applyGenericStorageEngine() err = %v, want nil", err)
		}
	})

	t.Run("apply failure short-circuits before CSI-driver polling", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "apply", "-k") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() {
			err = applyGenericStorageEngine(ch, testWrap, "/usr/local/bin/kubectl", "longhorn", "longhorn-system", "driver.longhorn.io")
		})
		if err == nil {
			t.Fatal("applyGenericStorageEngine() err = nil, want an error")
		}
		for _, c := range r.Calls {
			if exectest.CmdContains("", strings.Fields(c), "csidriver") {
				t.Errorf("expected no CSI-driver poll after apply failed, but saw call %q", c)
			}
		}
	})
}

func TestWaitForKubeOvnHealthy(t *testing.T) {
	t.Run("success path waits for every core workload", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = waitForKubeOvnHealthy(ch, testWrap, "/usr/local/bin/kubectl") })
		if err != nil {
			t.Fatalf("waitForKubeOvnHealthy() err = %v, want nil", err)
		}
		for _, w := range KubeOvnCoreWorkloads {
			found := false
			for _, c := range r.Calls {
				if exectest.CmdContains("", strings.Fields(c), "rollout", "status", w.Kind+"/"+w.Name) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected a rollout-status check for %s/%s, calls = %v", w.Kind, w.Name, r.Calls)
			}
		}
	})

	t.Run("first workload failing to roll out stops the wait immediately", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "rollout", "status", "ovs-ovn") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = waitForKubeOvnHealthy(ch, testWrap, "/usr/local/bin/kubectl") })
		if err == nil {
			t.Fatal("waitForKubeOvnHealthy() err = nil, want an error")
		}
		for _, c := range r.Calls {
			if exectest.CmdContains("", strings.Fields(c), "kube-ovn-cni") {
				t.Errorf("expected no check for later workloads after ovs-ovn failed, but saw call %q", c)
			}
		}
	})
}

func TestApplyKubeVirtCDIStep(t *testing.T) {
	t.Run("no markers present applies both components in order and marks both applied", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		ch := make(chan exec.StepMsg, 100)
		var written []string
		write := func(path string, data []byte) error {
			written = append(written, path+"="+string(data))
			return nil
		}
		var changed bool
		var err error
		exectest.WithFakeRunner(r, func() {
			changed, err = applyKubeVirtCDIStep(ch, testWrap, write, "/usr/local/bin/kubectl", "v1.2.0", "v1.60.0")
		})
		if err != nil {
			t.Fatalf("applyKubeVirtCDIStep() err = %v, want nil", err)
		}
		if !changed {
			t.Error("applyKubeVirtCDIStep() kubevirtCRChanged = false, want true when the KubeVirt CR was applied")
		}

		wantOrder := [][]string{
			{"apply", "-f", "kubevirt-operator.yaml"},
			{"wait", "crd/kubevirts.kubevirt.io"},
			{"apply", "-f", "kubevirt-cr.yaml"},
			{"apply", "-f", "cdi-operator.yaml"},
			{"wait", "crd/cdis.cdi.kubevirt.io"},
			{"apply", "-f", "cdi-cr.yaml"},
		}
		if len(r.Calls) < len(wantOrder) {
			t.Fatalf("len(r.Calls) = %d, want at least %d; calls = %v", len(r.Calls), len(wantOrder), r.Calls)
		}
		for i, want := range wantOrder {
			if !exectest.CmdContains("", strings.Fields(r.Calls[i]), want...) {
				t.Errorf("r.Calls[%d] = %q, want it to contain %v", i, r.Calls[i], want)
			}
		}

		if len(written) != 2 || !strings.Contains(written[0], "v1.2.0") || !strings.Contains(written[1], "v1.60.0") {
			t.Errorf("write calls = %v, want a KubeVirt v1.2.0 marker then a CDI v1.60.0 marker", written)
		}
	})

	t.Run("KubeVirt apply failure short-circuits before CDI is touched", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "apply", "-f", "kubevirt-operator.yaml") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		ch := make(chan exec.StepMsg, 100)
		var writeCalled bool
		write := func(path string, data []byte) error { writeCalled = true; return nil }
		var changed bool
		var err error
		exectest.WithFakeRunner(r, func() {
			changed, err = applyKubeVirtCDIStep(ch, testWrap, write, "/usr/local/bin/kubectl", "v1.2.0", "v1.60.0")
		})
		if err == nil {
			t.Fatal("applyKubeVirtCDIStep() err = nil, want an error")
		}
		if changed {
			t.Error("applyKubeVirtCDIStep() kubevirtCRChanged = true, want false when the KubeVirt apply itself failed")
		}
		if writeCalled {
			t.Error("expected no marker write after the KubeVirt apply failed")
		}
		for _, c := range r.Calls {
			if exectest.CmdContains("", strings.Fields(c), "cdi-operator.yaml") {
				t.Errorf("expected no CDI apply after KubeVirt's failed, but saw call %q", c)
			}
		}
	})
}

func TestRestartAileronIfNeeded(t *testing.T) {
	tests := []struct {
		name                                    string
		kubevirtCRChanged, aileronExistedBefore bool
		wantRestart                             bool
	}{
		{"CR unchanged, aileron already running", false, true, false},
		{"CR changed, aileron never existed (fresh install)", true, false, false},
		{"CR unchanged, aileron never existed", false, false, false},
		{"CR changed, aileron already running", true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &exectest.FakeRunner{}
			ch := make(chan exec.StepMsg, 100)
			var err error
			exectest.WithFakeRunner(r, func() {
				err = restartAileronIfNeeded(ch, testWrap, "/usr/local/bin/kubectl", tt.kubevirtCRChanged, tt.aileronExistedBefore)
			})
			if err != nil {
				t.Fatalf("restartAileronIfNeeded() err = %v, want nil", err)
			}
			restarted := false
			for _, c := range r.Calls {
				if exectest.CmdContains("", strings.Fields(c), "rollout", "restart", "deployment/aileron") {
					restarted = true
				}
			}
			if restarted != tt.wantRestart {
				t.Errorf("restarted = %v, want %v; calls = %v", restarted, tt.wantRestart, r.Calls)
			}
		})
	}
}
