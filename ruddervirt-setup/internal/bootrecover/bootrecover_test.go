// SPDX-License-Identifier: GPL-3.0-only

package bootrecover

import (
	"os"
	"os/exec"
	"path/filepath"
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

// TestRecoveryScriptRunsUnderSetU executes the embedded script under `bash -u`
// with a stub kubectl (API up, no corpses, configurable CNI readiness) and
// asserts both convergence paths. Regression coverage for two field bugs:
//
//   - `local name` without `=` leaves the variable UNSET in function scope, so
//     `set -u` aborts the script the first time it is read ("unbound
//     variable"); done_workloads must be seeded with "".
//   - convergence was checked via word count (`set -- $done_workloads`), but
//     each workload id is "kind selector" (two words), so $#==N never held
//     and the wait always burned the full CNI_DEADLINE before failing.
func TestRecoveryScriptRunsUnderSetU(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(stub, []byte(`#!/bin/bash
case " $* " in
  *" --raw=/readyz "*) exit 0 ;;
  *" wait --for=condition=Ready "*) exit "${READY_RC:-0}" ;;
  *" get pods "*) exit 0 ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	// No-op sleep so the deadline-bounded convergence polls don't stall the
	// test (the deadline check still terminates the loop).
	if err := os.WriteFile(filepath.Join(dir, "sleep"), []byte(`#!/bin/bash
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(dir, "ruddervirt-boot-recover.sh")
	body := string(recoveryScript)
	for old, new := range map[string]string{
		"KUBECTL=/usr/local/bin/kubectl": "KUBECTL=" + stub,
		"API_DEADLINE=300":               "API_DEADLINE=1",
		"CNI_DEADLINE=300":               "CNI_DEADLINE=1",
	} {
		if !strings.Contains(body, old) {
			t.Fatalf("cannot stub %q in embedded script", old)
		}
		body = strings.ReplaceAll(body, old, new)
	}
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(readyRC string) (string, int) {
		t.Helper()
		cmd := exec.Command(bash, "-u", script)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "READY_RC="+readyRC)
		out, runErr := cmd.CombinedOutput()
		code := 0
		if exit, ok := runErr.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if runErr != nil {
			t.Fatalf("running script: %v", runErr)
		}
		return string(out), code
	}

	if out, code := run("0"); code != 0 || !strings.Contains(out, "cluster converged") {
		t.Errorf("converged path: exit=%d output=%q\n", code, out)
	}
	if out, code := run("1"); code > 1 || !strings.Contains(out, "giving up") {
		t.Errorf("never-converges path: exit=%d output=%q\n", code, out)
	}
}
