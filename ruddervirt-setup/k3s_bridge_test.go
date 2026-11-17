// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec/exectest"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/k3s"
	"ruddervirt-setup/internal/storage"
	versionspkg "ruddervirt-setup/internal/versions"
)

// drainStrings collects every stepOutputMsg sent to ch without blocking the
// caller - callers still hold ch open, so this only reads what's already
// buffered. Used by adopt_test.go and other package-main tests that drive
// installsteps.Step-shaped functions directly.
func drainStrings(ch chan installsteps.StepMsg) []string {
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

func TestResolveK3sVersion(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"unset falls back to default", config.Config{}, versionspkg.DefaultK3sVersion},
		{"whitespace-only falls back to default", config.Config{Versions: config.VersionsConfig{K3s: "   "}}, versionspkg.DefaultK3sVersion},
		{"explicit value is trimmed and passed through", config.Config{Versions: config.VersionsConfig{K3s: "  v1.30.0+k3s1  "}}, "v1.30.0+k3s1"},
	}
	for _, c := range cases {
		if got := resolveK3sVersion(c.cfg); got != c.want {
			t.Errorf("%s: resolveK3sVersion() = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestJoinPlanHelpersOnANodeWithNoK3sConfig pins the default (single-node)
// branch of the join-detection previews, which is what every existing install
// gets. The joining branches are covered where their logic lives, against an
// injected reader - see internal/k3s's TestNodeMembership and
// TestConfigIsOperatorOwned - since these helpers deliberately hardcode
// os.ReadFile and the real /etc paths.
func TestJoinPlanHelpersOnANodeWithNoK3sConfig(t *testing.T) {
	if _, err := os.Stat(k3s.ClusterConfigPath); err == nil {
		t.Skipf("%s exists on this host: the no-config branch isn't reachable", k3s.ClusterConfigPath)
	}

	if line, ok := planConfigReadProblem(); ok {
		t.Errorf("planConfigReadProblem() = (%q, true), want no problem reported for an absent config", line)
	}
	if line, ok := planJoinSkip(); ok {
		t.Errorf("planJoinSkip() = (%q, true), want a node with no k3s config to preview its steps normally", line)
	}
	if got := planRenderK3sConfig(config.Config{}); !strings.Contains(got, "will render") {
		t.Errorf("planRenderK3sConfig() = %q, want it to say the config will be rendered", got)
	}
	if got := planApplyKubeOvn(config.Config{}); !strings.Contains(got, "kube-ovn") {
		t.Errorf("planApplyKubeOvn() = %q, want the normal kube-ovn preview", got)
	}
}

// TestSkipWhenJoiningIsANoOpWithoutAJoinTarget: the guard must never swallow
// a step on a node that bootstraps its own cluster.
func TestSkipWhenJoiningIsANoOpWithoutAJoinTarget(t *testing.T) {
	if _, err := os.Stat(k3s.ClusterConfigPath); err == nil {
		t.Skipf("%s exists on this host: the no-config branch isn't reachable", k3s.ClusterConfigPath)
	}
	ch := make(chan installsteps.StepMsg, 8)
	if skipWhenJoining("Applying kube-ovn", ch) {
		t.Fatal("skipWhenJoining() = true, want false with no cluster to join")
	}
	if lines := drainStrings(ch); len(lines) != 0 {
		t.Errorf("skipWhenJoining() logged %v, want nothing", lines)
	}
}

// TestJoinPlanHelpersOnAJoiningNode is the mirror of the test above, and the
// one that actually runs on a node configured to join a cluster - including
// `make test-container` with a hand-placed /etc/rancher/k3s/config.yaml,
// which is how the join path gets exercised against a real filesystem rather
// than an injected reader. Exactly one of the two tests runs per environment.
func TestJoinPlanHelpersOnAJoiningNode(t *testing.T) {
	server := k3s.NodeMembership(os.ReadFile).JoinServer
	if server == "" {
		t.Skip("this host isn't configured to join a cluster")
	}

	line, ok := planJoinSkip()
	if !ok {
		t.Fatalf("planJoinSkip() = (%q, false), want the cluster-wide steps previewed as skipped", line)
	}
	if !strings.Contains(line, server) {
		t.Errorf("planJoinSkip() = %q, want it to name the cluster at %s", line, server)
	}
	if got := planRenderK3sConfig(config.Config{}); !strings.Contains(got, "leaving it as-is") {
		t.Errorf("planRenderK3sConfig() = %q, want it to say the operator's config is left alone", got)
	}
	if got := planApplyManifests(config.Config{}); !strings.Contains(got, "skip - joining") {
		t.Errorf("planApplyManifests() = %q, want a skip line", got)
	}

	ch := make(chan installsteps.StepMsg, 8)
	if !skipWhenJoining("Applying kube-ovn", ch) {
		t.Fatal("skipWhenJoining() = false, want true on a joining node")
	}
	lines := drainStrings(ch)
	if len(lines) != 1 || !strings.Contains(lines[0], server) {
		t.Errorf("skipWhenJoining() logged %v, want one line naming the cluster", lines)
	}
}

// TestJoinPlanHelpersOnAnUnreadableConfig: a config placed with sudo at mode
// 0600 must surface as a loud plan line rather than a silent "new cluster"
// preview, since the pipeline then refuses to render over it (see
// internal/k3s.RenderK3sConfigStep). Runs under `make test-container` with
// such a config in place; skipped everywhere else.
func TestJoinPlanHelpersOnAnUnreadableConfig(t *testing.T) {
	if _, err := os.ReadFile(k3s.ClusterConfigPath); !errors.Is(err, os.ErrPermission) {
		t.Skipf("%s is readable or absent on this host", k3s.ClusterConfigPath)
	}

	line, ok := planConfigReadProblem()
	if !ok {
		t.Fatal("planConfigReadProblem() reported no problem, want the unreadable config called out")
	}
	if !strings.Contains(line, "cannot read") || !strings.Contains(line, "0644") {
		t.Errorf("planConfigReadProblem() = %q, want it to name the problem and the fix", line)
	}
	for name, got := range map[string]string{
		"planRenderK3sConfig": planRenderK3sConfig(config.Config{}),
		"planApplyKubeOvn":    planApplyKubeOvn(config.Config{}),
		"planApplyManifests":  planApplyManifests(config.Config{}),
	} {
		if !strings.Contains(got, "cannot read") {
			t.Errorf("%s() = %q, want the unreadable-config warning, not a normal preview", name, got)
		}
	}
}

// TestPrepareStorageStepRefusesAnEngineMismatch: the engine is cluster-wide
// but the disk prep is this host's, so a joining node whose config disagrees
// must be stopped BEFORE PrepareStorageDevice writes the engine marker -
// after that, storage.engine locks and only an OS reinstall can change it.
func TestPrepareStorageStepRefusesAnEngineMismatch(t *testing.T) {
	fake := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		// k3s is up, so kubectl results can be trusted.
		if exectest.CmdContains(name, args, "is-active", "k3s.service") {
			return exectest.Outcome{}
		}
		// A cluster already running longhorn.
		if exectest.CmdContains(name, args, "get", "csidriver", "driver.longhorn.io") {
			return exectest.Outcome{}
		}
		return exectest.Outcome{Err: exectest.ErrFake}
	}}

	ch := make(chan installsteps.StepMsg, 16)
	exectest.WithFakeRunner(fake, func() {
		prepareStorageStep(config.Config{Storage: config.StorageConfig{Engine: "openebs"}}, ch)
	})

	var done *stepDoneMsg
	for len(ch) > 0 {
		if msg, ok := (<-ch).(stepDoneMsg); ok {
			done = &msg
		}
	}
	if done == nil {
		t.Fatal("prepareStorageStep sent no stepDoneMsg")
	}
	if done.Err == nil {
		t.Fatal("prepareStorageStep err = nil, want a refusal on an engine mismatch")
	}
	for _, want := range []string{"longhorn", "openebs", "Storage engine"} {
		if !strings.Contains(done.Err.Error(), want) {
			t.Errorf("prepareStorageStep err = %q, want it to mention %q", done.Err, want)
		}
	}
}

// TestPrepareStorageStepProceedsWhenNoClusterEngineExists covers the first
// node's own install, where the engine isn't applied until a later step -
// detection must fall through to the configured engine, not block.
func TestPrepareStorageStepProceedsWhenNoClusterEngineExists(t *testing.T) {
	fake := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		return exectest.Outcome{Err: exectest.ErrFake}
	}}
	var detected string
	var ok bool
	exectest.WithFakeRunner(fake, func() {
		detected, ok = storage.DetectClusterEngine(kubectlBinPath)
	})
	if ok {
		t.Fatalf("DetectClusterEngine() = (%q, true), want no engine detected", detected)
	}
	// With nothing detected prepareStorageStep falls through to
	// storage.PrepareStorageDevice, which is covered in internal/storage -
	// driving it here would touch this machine's real disks.
}

// TestPrepareStorageStepIgnoresKubectlUntilK3sIsUp guards the ENOEXEC trap
// internal/status documents: before k3s is installed, /usr/local/bin/k3s is a
// shebang-less placeholder that the kubectl wrapper execs, so every kubectl
// call exits 0. Trusting that would have DetectClusterEngine name an engine
// off a bogus success and refuse a perfectly good first node.
func TestPrepareStorageStepIgnoresKubectlUntilK3sIsUp(t *testing.T) {
	fake := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		if exectest.CmdContains(name, args, "is-active", "k3s.service") {
			return exectest.Outcome{Err: exectest.ErrFake} // k3s not running
		}
		return exectest.Outcome{} // everything else "succeeds", as the placeholder would
	}}

	ch := make(chan installsteps.StepMsg, 16)
	exectest.WithFakeRunner(fake, func() {
		prepareStorageStep(config.Config{Storage: config.StorageConfig{Engine: "openebs"}}, ch)
	})

	for _, call := range fake.Calls {
		if strings.Contains(call, "csidriver") || strings.Contains(call, "get node") {
			t.Errorf("probed the cluster before k3s was active: %q", call)
		}
	}
}

// TestPrepareStorageStepRefusesAnUnlabelledNode: every storage engine's CSI
// node plugin selects on ruddervirt=metal, so a joining node without it gets
// no plugin - and nothing downstream notices, because the engine's pods are
// Ready on every other node and the install reports success.
func TestPrepareStorageStepRefusesAnUnlabelledNode(t *testing.T) {
	fake := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		if exectest.CmdContains(name, args, "is-active", "k3s.service") {
			return exectest.Outcome{}
		}
		// Cluster engine matches this node's config, so only the label is wrong.
		if exectest.CmdContains(name, args, "get", "csidriver", "local.csi.openebs.io") {
			return exectest.Outcome{}
		}
		if exectest.CmdContains(name, args, "get", "node") {
			return exectest.Outcome{Out: []byte("")} // registered, but unlabelled
		}
		return exectest.Outcome{Err: exectest.ErrFake}
	}}

	ch := make(chan installsteps.StepMsg, 16)
	exectest.WithFakeRunner(fake, func() {
		prepareStorageStep(config.Config{Storage: config.StorageConfig{Engine: "openebs"}}, ch)
	})

	var done *stepDoneMsg
	for len(ch) > 0 {
		if msg, ok := (<-ch).(stepDoneMsg); ok {
			done = &msg
		}
	}
	if done == nil || done.Err == nil {
		t.Fatal("prepareStorageStep succeeded, want a refusal for a node missing ruddervirt=metal")
	}
	for _, want := range []string{"ruddervirt=metal", "label node"} {
		if !strings.Contains(done.Err.Error(), want) {
			t.Errorf("prepareStorageStep err = %q, want it to mention %q", done.Err, want)
		}
	}
}

// TestPrepareStorageStepAcceptsALabelledNode is the other side: a correctly
// labelled node whose engine matches must fall through to the real disk prep.
func TestPrepareStorageStepAcceptsALabelledNode(t *testing.T) {
	probed := false
	fake := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		if exectest.CmdContains(name, args, "is-active", "k3s.service") {
			return exectest.Outcome{}
		}
		if exectest.CmdContains(name, args, "get", "csidriver", "local.csi.openebs.io") {
			return exectest.Outcome{}
		}
		if exectest.CmdContains(name, args, "get", "node") {
			probed = true
			return exectest.Outcome{Out: []byte("metal\n")}
		}
		return exectest.Outcome{Err: exectest.ErrFake}
	}}

	ch := make(chan installsteps.StepMsg, 16)
	exectest.WithFakeRunner(fake, func() {
		prepareStorageStep(config.Config{Storage: config.StorageConfig{Engine: "openebs"}}, ch)
	})

	if !probed {
		t.Fatal("never checked the node's label")
	}
	var done *stepDoneMsg
	for len(ch) > 0 {
		if msg, ok := (<-ch).(stepDoneMsg); ok {
			done = &msg
		}
	}
	if done == nil {
		t.Fatal("prepareStorageStep sent no stepDoneMsg")
	}
	// It proceeds into PrepareStorageDevice, which fails against the fake's
	// disk commands - the point is that it got PAST both cluster guards.
	if done.Err != nil && strings.Contains(done.Err.Error(), "ruddervirt=metal") {
		t.Errorf("prepareStorageStep err = %q, want it past the label guard", done.Err)
	}
}
