// SPDX-License-Identifier: GPL-3.0-only

package bootrecover

import (
	"os"
	"strings"
	"testing"
)

// fakeFiles captures what a run of WriteAssets asked the (faked) privileged
// write/chmod primitives to do.
type fakeFiles struct {
	writes []string // paths written, in order
	data   map[string]string
	chmods map[string]os.FileMode
	err    error // injected failure for the write primitive
}

func (f *fakeFiles) write(path string, data []byte) error {
	if f.err != nil {
		return f.err
	}
	if f.data == nil {
		f.data = map[string]string{}
	}
	f.writes = append(f.writes, path)
	f.data[path] = string(data)
	return nil
}

func (f *fakeFiles) chmod(path string, mode os.FileMode) error {
	if f.chmods == nil {
		f.chmods = map[string]os.FileMode{}
	}
	f.chmods[path] = mode
	return nil
}

func TestWriteAssetsWritesEveryAssetWithCorrectModes(t *testing.T) {
	f := &fakeFiles{}
	if err := WriteAssets(f.write, f.chmod); err != nil {
		t.Fatalf("WriteAssets: %v", err)
	}

	want := map[string]os.FileMode{
		"/usr/local/bin/ruddervirt-boot-recover":                    0o755, // executable script
		"/etc/systemd/system/ruddervirt-boot-recover.service":       0o644,
		"/etc/kubelet.config":                                       0o644,
		"/etc/systemd/system.conf.d/10-ruddervirt-watchdog.conf":    0o644,
		"/etc/modules-load.d/ruddervirt-watchdog.conf":              0o644,
		"/etc/systemd/system/ruddervirt-boot-recover.timer":         0o644,
		"/etc/systemd/system/ruddervirt-boot-recover-sweep.service": 0o644,
	}
	gotWrites := map[string]bool{}
	for _, p := range f.writes {
		gotWrites[p] = true
	}
	for path, mode := range want {
		if !gotWrites[path] {
			t.Errorf("asset %s was never written", path)
		}
		if m, ok := f.chmods[path]; ok && m != mode {
			t.Errorf("asset %s chmodded %o, want %o", path, m, mode)
		}
	}
	// Only the script is non-default mode; everything else is the default
	// 0644 the write primitive already produces and must not be chmodded.
	if len(f.chmods) != 1 {
		t.Errorf("chmod called on %d files (want 1, the script only)", len(f.chmods))
	}

	// The embedded script is the real artifact, not an empty placeholder.
	if !strings.Contains(f.data["/usr/local/bin/ruddervirt-boot-recover"], "reap_corpses") {
		t.Error("embedded recovery script looks wrong (missing reap_corpses)")
	}
	// Cadence guard: the reaper must sweep repeatedly through the CNI
	// convergence window, not once at boot - corpse bursts can be born at any
	// point in that window, not just in the first seconds. The reap call
	// inside wait_for_cni's loop is that guarantee; assert it survived.
	if !strings.Contains(f.data["/usr/local/bin/ruddervirt-boot-recover"], "while :; do\n    sweep_all") {
		t.Error("reaper is not looped inside wait_for_cni (corpse-burst regression risk)")
	}
	// Failed-phase cleanup guard: controller-owned pods that died with
	// recorded exit codes (a reboot wave) must be swept too, except Job-owned
	// ones (backoffLimit accounting resets on deletion).
	script, ok := f.data["/usr/local/bin/ruddervirt-boot-recover"]
	if !ok {
		t.Fatal("script not written")
	}
	if !strings.Contains(script, "sweep_failed()") {
		t.Error("script missing the failed-phase sweep")
	}
	// Finalization-residue guard: Succeeded pods kubelet finalized on
	// re-adoption (reboot waves) must be swept too, same non-Job rule.
	if !strings.Contains(script, "sweep_completed()") {
		t.Error("script missing the completed-phase sweep")
	}
	if !strings.Contains(script, "-'!job-name'") && !strings.Contains(script, "-l '!job-name'") {
		t.Error("failed-phase sweep must exclude Job-owned pods")
	}
	// The unit must point at the exact ExecStart the script is written to.
	if !strings.Contains(strings.TrimSpace(f.data["/etc/systemd/system/ruddervirt-boot-recover.service"]), "ExecStart=/usr/local/bin/ruddervirt-boot-recover") {
		t.Error("unit ExecStart does not match script path")
	}
	if !strings.Contains(f.data["/etc/kubelet.config"], "shutdownGracePeriod: 120s") {
		t.Error("kubelet config missing shutdownGracePeriod 120s")
	}

	// Restart-sweep coverage: the timer must exist, fire on a schedule, and
	// invoke the script in --reap-only mode (the oneshot alone can't cover
	// k3s/containerd restarts that happen after it converges).
	timer, ok := f.data["/etc/systemd/system/ruddervirt-boot-recover.timer"]
	if !ok {
		t.Fatal("timer unit not written")
	}
	if !strings.Contains(timer, "OnUnitActiveSec=5min") {
		t.Error("timer missing repeating schedule (OnUnitActiveSec)")
	}
	sweep, ok := f.data["/etc/systemd/system/ruddervirt-boot-recover-sweep.service"]
	if !ok {
		t.Fatal("sweep service not written")
	}
	if !strings.Contains(sweep, "--reap-only") {
		t.Error("sweep service does not invoke --reap-only mode")
	}
	if script, ok := f.data["/usr/local/bin/ruddervirt-boot-recover"]; ok {
		if !strings.Contains(script, "--reap-only") {
			t.Error("script missing --reap-only dispatch")
		}
	}
}

func TestWriteAssetsPropagatesWriteFailure(t *testing.T) {
	f := &fakeFiles{err: os.ErrPermission}
	err := WriteAssets(f.write, f.chmod)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "writing /usr/local/bin/ruddervirt-boot-recover") {
		t.Fatalf("error does not name the failing asset: %v", err)
	}
}

func TestRecoveryScriptIsValidBashHeader(t *testing.T) {
	head := string(recoveryScript[:64])
	if !strings.HasPrefix(head, "#!/bin/bash") && !strings.HasPrefix(head, "#!/usr/bin/env bash") {
		t.Errorf("recovery script missing shebang: %q", head)
	}
}
