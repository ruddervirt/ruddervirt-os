// SPDX-License-Identifier: GPL-3.0-only

package k3s

import (
	"errors"
	"strings"
	"testing"

	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/exec/exectest"
)

// TestWaitForHelmInstallJobSuccess covers the shape shared by
// internal/aileron's ApplyAileron and internal/stabilizer's
// applyStabilizer: poll for the Job to exist, then wait for it to reach
// condition=complete.
func TestWaitForHelmInstallJobSuccess(t *testing.T) {
	const kubectlBin = "/usr/local/bin/kubectl"
	r := &exectest.FakeRunner{}
	ch := make(chan exec.StepMsg, 100)
	var err error
	exectest.WithFakeRunner(r, func() {
		err = WaitForHelmInstallJob(ch, testWrap, kubectlBin, "kube-system", "job/helm-install-aileron", "aileron")
	})
	if err != nil {
		t.Fatalf("WaitForHelmInstallJob err = %v, want nil", err)
	}

	var sawGet, sawWait bool
	for _, c := range r.Calls {
		fields := strings.Fields(c)
		if exectest.CmdContains("", fields, kubectlBin, "-n", "kube-system", "get", "job/helm-install-aileron") {
			sawGet = true
		}
		if exectest.CmdContains("", fields, kubectlBin, "-n", "kube-system", "wait", "--for=condition=complete", "job/helm-install-aileron", "--timeout=600s") {
			sawWait = true
		}
	}
	if !sawGet {
		t.Errorf("expected a `get job/helm-install-aileron` poll among calls %v", r.Calls)
	}
	if !sawWait {
		t.Errorf("expected a `wait --for=condition=complete --timeout=600s` call among calls %v", r.Calls)
	}

	var lines []string
	drain := true
	for drain {
		select {
		case msg := <-ch:
			if s, ok := msg.(testOutputMsg); ok {
				lines = append(lines, string(s))
			}
		default:
			drain = false
		}
	}
	wantLines := []string{
		"Waiting for the aileron helm-install job...",
		"Waiting for the aileron helm-install job to complete...",
	}
	for _, want := range wantLines {
		found := false
		for _, l := range lines {
			if l == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected progress line %q among %v", want, lines)
		}
	}
}

// TestWaitForHelmInstallJobCompletionFailure confirms a job that never
// reaches condition=complete propagates as an error, not masked by the
// componentLabel-formatted progress messages.
func TestWaitForHelmInstallJobCompletionFailure(t *testing.T) {
	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		if exectest.CmdContains(name, args, "wait", "--for=condition=complete") {
			return exectest.Outcome{Err: exectest.ErrFake}
		}
		return exectest.Outcome{}
	}}
	ch := make(chan exec.StepMsg, 100)
	var err error
	exectest.WithFakeRunner(r, func() {
		err = WaitForHelmInstallJob(ch, testWrap, "/usr/local/bin/kubectl", "kube-system", "job/helm-install-stabilizer", "stabilizer")
	})
	if !errors.Is(err, exectest.ErrFake) {
		t.Errorf("WaitForHelmInstallJob err = %v, want %v", err, exectest.ErrFake)
	}
}

// Note: the poll phase's 60 attempts / 5s interval aren't parametrized, so
// a real end-to-end timeout test would take 5 minutes and isn't included
// here, consistent with this module's other PollUntil-based tests.
