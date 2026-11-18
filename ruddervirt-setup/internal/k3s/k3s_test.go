// SPDX-License-Identifier: GPL-3.0-only

package k3s

import (
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/exec/exectest"
	"ruddervirt-setup/internal/network"
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

// fakeFiles builds a readFile double over an in-memory file set - the
// injected-reader seam ReadMembership/RenderK3sConfigStep take, so these
// tests never touch /etc. A path mapped to nil reads back as a permission
// error (the mode-0600 config an operator placed with sudo); an unmapped
// path reads back as not-exist.
func fakeFiles(files map[string][]byte) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		data, ok := files[path]
		if !ok {
			return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		}
		if data == nil {
			return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
		}
		return data, nil
	}
}

// templateConfig is the stock server.bu template: its server/cluster-init
// hints are commented out, so it declares no membership.
const templateConfig = `# server: https://192.168.1.5:6443
# token: MYTOKEN
# cluster-init: true
bind-address: __LOCAL_IP__
node-ip: __LOCAL_IP__
tls-san:
  - "__LOCAL_IP__"
flannel-backend: none
disable:
  - "local-storage"
`

func TestReadMembership(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    Membership
		wantErr bool
	}{
		{"stock template declares nothing", []byte(templateConfig), Membership{}, false},
		{"joining node", []byte("server: https://10.0.0.5:6443\ntoken: sekrit\n"), Membership{JoinServer: "https://10.0.0.5:6443"}, false},
		{"server url is trimmed", []byte("server: \"  https://10.0.0.5:6443  \"\n"), Membership{JoinServer: "https://10.0.0.5:6443"}, false},
		{"joinable first node", []byte("cluster-init: true\n" + templateConfig), Membership{ClusterInit: true}, false},
		{"cluster-init explicitly false", []byte("cluster-init: false\n"), Membership{}, false},
		{"both keys", []byte("server: https://10.0.0.5:6443\ncluster-init: true\n"), Membership{JoinServer: "https://10.0.0.5:6443", ClusterInit: true}, false},
		{"empty file", []byte(""), Membership{}, false},
		{"malformed yaml", []byte("server: [unclosed\n"), Membership{}, true},
		{"wrong type for cluster-init", []byte("cluster-init: yesplease\n"), Membership{}, true},
	}
	for _, c := range cases {
		got, err := ReadMembership(fakeFiles(map[string][]byte{ClusterConfigPath: c.content}), ClusterConfigPath)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: ReadMembership() err = %v, wantErr %v", c.name, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("%s: ReadMembership() = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestReadMembershipMissingFile covers the "nothing declared yet" case - a
// node that hasn't rendered a k3s config at all must not read as an error,
// since every caller would then treat it the same as a corrupt one.
func TestReadMembershipMissingFile(t *testing.T) {
	got, err := ReadMembership(fakeFiles(nil), ClusterConfigPath)
	if err != nil {
		t.Fatalf("ReadMembership() err = %v, want nil for a missing file", err)
	}
	if got.Declared() {
		t.Errorf("ReadMembership() = %+v, want nothing declared", got)
	}
}

func TestNodeMembership(t *testing.T) {
	joining := []byte("server: https://10.0.0.5:6443\ntoken: sekrit\n")
	cases := []struct {
		name  string
		files map[string][]byte
		want  Membership
	}{
		{
			"live config wins over the template",
			map[string][]byte{ClusterConfigPath: joining, ConfigTemplatePath: []byte(templateConfig)},
			Membership{JoinServer: "https://10.0.0.5:6443"},
		},
		{
			"falls back to the template when the live config declares nothing",
			map[string][]byte{ClusterConfigPath: []byte(templateConfig), ConfigTemplatePath: []byte("cluster-init: true\n")},
			Membership{ClusterInit: true},
		},
		{
			"template-declared join, before the first render",
			map[string][]byte{ConfigTemplatePath: joining},
			Membership{JoinServer: "https://10.0.0.5:6443"},
		},
		{
			"nothing anywhere - today's single-node default",
			map[string][]byte{ClusterConfigPath: []byte(templateConfig), ConfigTemplatePath: []byte(templateConfig)},
			Membership{},
		},
		{
			"unreadable live config falls back rather than guessing from it",
			map[string][]byte{ClusterConfigPath: nil, ConfigTemplatePath: []byte("cluster-init: true\n")},
			Membership{ClusterInit: true},
		},
		{
			"malformed template reads as nothing declared",
			map[string][]byte{ConfigTemplatePath: []byte("server: [unclosed\n")},
			Membership{},
		},
	}
	for _, c := range cases {
		if got := NodeMembership(fakeFiles(c.files)); got != c.want {
			t.Errorf("%s: NodeMembership() = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestConfigIsOperatorOwned(t *testing.T) {
	joining := []byte("server: https://10.0.0.5:6443\ntoken: sekrit\n")
	cases := []struct {
		name       string
		files      map[string][]byte
		want       bool
		wantReason string
	}{
		{
			"hand-placed joining config",
			map[string][]byte{ClusterConfigPath: joining, ConfigTemplatePath: []byte(templateConfig)},
			true, "server: is set (joining https://10.0.0.5:6443)",
		},
		{
			"hand-placed cluster-init config",
			map[string][]byte{ClusterConfigPath: []byte("cluster-init: true\n"), ConfigTemplatePath: []byte(templateConfig)},
			true, "cluster-init: is set",
		},
		{
			// The operator declared membership in the template instead, so
			// rendering still owns the live config - that's the whole point
			// of that route (__LOCAL_IP__ keeps following the DHCP lease).
			"template declares membership too",
			map[string][]byte{ClusterConfigPath: joining, ConfigTemplatePath: joining},
			false, "",
		},
		{
			"stock single-node install",
			map[string][]byte{ClusterConfigPath: []byte(templateConfig), ConfigTemplatePath: []byte(templateConfig)},
			false, "",
		},
		{
			"no live config yet",
			map[string][]byte{ConfigTemplatePath: []byte(templateConfig)},
			false, "",
		},
		{
			"malformed live config is not claimed as the operator's",
			map[string][]byte{ClusterConfigPath: []byte("server: [unclosed\n"), ConfigTemplatePath: []byte(templateConfig)},
			false, "",
		},
	}
	for _, c := range cases {
		got, reason := ConfigIsOperatorOwned(fakeFiles(c.files))
		if got != c.want || reason != c.wantReason {
			t.Errorf("%s: ConfigIsOperatorOwned() = (%v, %q), want (%v, %q)", c.name, got, reason, c.want, c.wantReason)
		}
	}
}

// TestRenderK3sConfigStepSkipsOperatorOwnedConfig is the load-bearing case
// for joining a cluster: rendering over a hand-placed config would drop the
// server:/token: that makes this node a cluster member at all.
func TestRenderK3sConfigStepSkipsOperatorOwnedConfig(t *testing.T) {
	files := map[string][]byte{
		ClusterConfigPath:  []byte("server: https://10.0.0.5:6443\ntoken: sekrit\n"),
		ConfigTemplatePath: []byte(templateConfig),
	}
	ch := make(chan exec.StepMsg, 16)
	wrote := false
	err := RenderK3sConfigStep(
		network.NetworkConfig{Addressing: "static", StaticIP: "10.0.0.6", SvcCIDR: "10.43.0.0/16", PodCIDR: "10.42.0.0/16"},
		ch, func(line string) exec.StepMsg { return line },
		fakeFiles(files),
		func(path string, data []byte) error { wrote = true; return nil },
	)
	if err != nil {
		t.Fatalf("RenderK3sConfigStep() err = %v, want nil", err)
	}
	if wrote {
		t.Fatal("RenderK3sConfigStep() wrote the k3s config, want the operator-provided one left in place")
	}
	close(ch)
	var log []string
	for msg := range ch {
		log = append(log, msg.(string))
	}
	joined := strings.Join(log, "\n")
	if !strings.Contains(joined, "https://10.0.0.5:6443") || !strings.Contains(joined, "leaving the operator-provided config in place") {
		t.Errorf("RenderK3sConfigStep() log = %q, want it to say which cluster and that the config was left alone", joined)
	}
}

// TestRenderK3sConfigStepRendersTemplate is the other side of the same
// branch: with no operator-declared membership, placeholder substitution
// still happens exactly as before.
func TestRenderK3sConfigStepRendersTemplate(t *testing.T) {
	files := map[string][]byte{ConfigTemplatePath: []byte(templateConfig)}
	ch := make(chan exec.StepMsg, 16)
	var gotPath string
	var gotData []byte
	err := RenderK3sConfigStep(
		network.NetworkConfig{Addressing: "static", StaticIP: "10.0.0.6", SvcCIDR: "10.43.0.0/16", PodCIDR: "10.42.0.0/16"},
		ch, func(line string) exec.StepMsg { return line },
		fakeFiles(files),
		func(path string, data []byte) error { gotPath, gotData = path, data; return nil },
	)
	if err != nil {
		t.Fatalf("RenderK3sConfigStep() err = %v, want nil", err)
	}
	if gotPath != ClusterConfigPath {
		t.Errorf("RenderK3sConfigStep() wrote %q, want %q", gotPath, ClusterConfigPath)
	}
	if strings.Contains(string(gotData), "__LOCAL_IP__") {
		t.Errorf("RenderK3sConfigStep() output = %q, want __LOCAL_IP__ substituted", gotData)
	}
	if !strings.Contains(string(gotData), "10.0.0.6") {
		t.Errorf("RenderK3sConfigStep() output = %q, want the local IP rendered in", gotData)
	}
}

// TestRenderK3sConfigStepTemplateDeclaredJoin covers the template route: an
// operator who uncomments server:/token: in the template still gets normal
// rendering, and the join keys ride through into the live config.
func TestRenderK3sConfigStepTemplateDeclaredJoin(t *testing.T) {
	tmpl := "server: https://10.0.0.5:6443\ntoken: sekrit\n" + templateConfig
	files := map[string][]byte{
		ConfigTemplatePath: []byte(tmpl),
		ClusterConfigPath:  []byte(tmpl),
	}
	ch := make(chan exec.StepMsg, 16)
	var gotData []byte
	err := RenderK3sConfigStep(
		network.NetworkConfig{Addressing: "static", StaticIP: "10.0.0.6", SvcCIDR: "10.43.0.0/16", PodCIDR: "10.42.0.0/16"},
		ch, func(line string) exec.StepMsg { return line },
		fakeFiles(files),
		func(path string, data []byte) error { gotData = data; return nil },
	)
	if err != nil {
		t.Fatalf("RenderK3sConfigStep() err = %v, want nil", err)
	}
	if !strings.Contains(string(gotData), "server: https://10.0.0.5:6443") {
		t.Errorf("RenderK3sConfigStep() output = %q, want the template's join keys carried through", gotData)
	}
	if strings.Contains(string(gotData), "__LOCAL_IP__") {
		t.Errorf("RenderK3sConfigStep() output = %q, want __LOCAL_IP__ substituted", gotData)
	}
}

// TestRenderK3sConfigStepRefusesUnreadableConfig: an unreadable or malformed
// live config is almost always one an operator wrote by hand, so overwriting
// it would silently undo exactly the thing they set up.
func TestRenderK3sConfigStepRefusesUnreadableConfig(t *testing.T) {
	cases := []struct {
		name string
		live []byte
	}{
		// nil => the reader reports a permission error, i.e. a sudo-placed
		// mode-0600 config the unprivileged TUI can't read.
		{"unreadable", nil},
		{"malformed", []byte("server: [unclosed\n")},
	}
	for _, c := range cases {
		files := map[string][]byte{ClusterConfigPath: c.live, ConfigTemplatePath: []byte(templateConfig)}
		ch := make(chan exec.StepMsg, 16)
		wrote := false
		err := RenderK3sConfigStep(
			network.NetworkConfig{Addressing: "static", StaticIP: "10.0.0.6", SvcCIDR: "10.43.0.0/16", PodCIDR: "10.42.0.0/16"},
			ch, func(line string) exec.StepMsg { return line },
			fakeFiles(files),
			func(path string, data []byte) error { wrote = true; return nil },
		)
		if err == nil {
			t.Errorf("%s: RenderK3sConfigStep() err = nil, want a refusal", c.name)
		} else if !strings.Contains(err.Error(), "refusing to overwrite") {
			t.Errorf("%s: RenderK3sConfigStep() err = %v, want it to say it's refusing to overwrite", c.name, err)
		}
		if wrote {
			t.Errorf("%s: RenderK3sConfigStep() wrote the config anyway", c.name)
		}
	}
}

// TestParseMasterNodes covers what kube-ovn's masterNodes value is built
// from. Both halves are load-bearing: the IPv4-only filter (kube-ovn.yaml
// pins networking.stack to IPv4, and a mixed-family NODE_IPS list breaks
// ovs-ovn), and the node names, which ApplyKubeOvnStep uses to confirm THIS
// node registered before rendering - without that it could render
// masterNodes without the joining node and then label it master anyway,
// leaving an ovn-central replica that is not a raft member.
func TestParseMasterNodes(t *testing.T) {
	const nodes = `{"items":[
		{"metadata":{"name":"node-a"},"status":{"addresses":[
			{"type":"Hostname","address":"node-a"},
			{"type":"InternalIP","address":"10.0.0.1"}]}},
		{"metadata":{"name":"node-b"},"status":{"addresses":[
			{"type":"InternalIP","address":"fd00::2"},
			{"type":"InternalIP","address":"10.0.0.2"},
			{"type":"ExternalIP","address":"203.0.113.9"}]}}
	]}`

	got, err := parseMasterNodes([]byte(nodes), "node-role.kubernetes.io/control-plane")
	if err != nil {
		t.Fatalf("parseMasterNodes() err = %v, want nil", err)
	}
	if want := []string{"10.0.0.1", "10.0.0.2"}; !reflect.DeepEqual(got.IPs, want) {
		t.Errorf("parseMasterNodes().IPs = %v, want %v (IPv4 InternalIPs only)", got.IPs, want)
	}
	if want := []string{"node-a", "node-b"}; !reflect.DeepEqual(got.Names, want) {
		t.Errorf("parseMasterNodes().Names = %v, want %v", got.Names, want)
	}
	if got.Selector != "node-role.kubernetes.io/control-plane" {
		t.Errorf("parseMasterNodes().Selector = %q, want the selector it was given", got.Selector)
	}

	if _, err := parseMasterNodes([]byte("not json"), "x"); err == nil {
		t.Error("parseMasterNodes(garbage) err = nil, want an error so the caller tries the next selector")
	}

	empty, err := parseMasterNodes([]byte(`{"items":[]}`), "x")
	if err != nil {
		t.Fatalf("parseMasterNodes(empty) err = %v, want nil", err)
	}
	if len(empty.IPs) != 0 {
		t.Errorf("parseMasterNodes(empty).IPs = %v, want none", empty.IPs)
	}
}

// TestMasterNodesIncludes: k3s registers a node under its LOWERCASED
// hostname, so a host whose /etc/hostname carries any uppercase would never
// match itself with a case-sensitive compare - and ApplyKubeOvnStep would
// then wait out its full poll and fall back to the warning path on a
// perfectly healthy node.
func TestMasterNodesIncludes(t *testing.T) {
	m := masterNodes{Names: []string{"node-a", "node-b"}}
	cases := []struct {
		name string
		want bool
	}{
		{"node-a", true},
		{"node-b", true},
		{"NODE-A", true},
		{"node-c", false},
		{"", false},
		{"node", false},
	}
	for _, c := range cases {
		if got := m.includes(c.name); got != c.want {
			t.Errorf("masterNodes{%v}.includes(%q) = %v, want %v", m.Names, c.name, got, c.want)
		}
	}
	if (masterNodes{}).includes("node-a") {
		t.Error("an empty masterNodes includes nothing")
	}
}
