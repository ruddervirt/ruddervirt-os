// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"testing"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec/exectest"
	"ruddervirt-setup/internal/installsteps"
)

func TestStabilizerVersionApplySteps(t *testing.T) {
	patch := []byte(`{"spec":{"version":"1.3.0"}}`)

	t.Run("has exactly two steps: patch, then wait for rollout", func(t *testing.T) {
		steps := stabilizerVersionApplySteps("kube-system", "stabilizer", patch, "1.3.0")
		if len(steps) != 2 {
			t.Fatalf("got %d steps, want 2", len(steps))
		}
	})

	t.Run("step 1 patches with exactly the given patch body", func(t *testing.T) {
		var patchArg string
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if cmdContains(name, args, "patch", "helmchart.helm.cattle.io", "stabilizer") {
				for i, a := range args {
					if a == "-p" && i+1 < len(args) {
						patchArg = args[i+1]
					}
				}
			}
			return exectest.Outcome{}
		}}
		steps := stabilizerVersionApplySteps("kube-system", "stabilizer", patch, "1.3.0")
		ch := make(chan installsteps.StepMsg, 100)
		exectest.WithFakeRunner(r, func() { steps[0].Run(config.Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.Err != nil {
			t.Fatalf("step 1 err = %v, want nil", done.Err)
		}
		if patchArg != string(patch) {
			t.Errorf("patch body = %q, want exactly %q", patchArg, patch)
		}
	})

	t.Run("step 1 failure is reported, not swallowed", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("boom"), Err: exectest.ErrFake}
		}}
		steps := stabilizerVersionApplySteps("kube-system", "stabilizer", patch, "1.3.0")
		ch := make(chan installsteps.StepMsg, 100)
		exectest.WithFakeRunner(r, func() { steps[0].Run(config.Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.Err == nil {
			t.Fatal("step 1 err = nil, want non-nil")
		}
	})

	t.Run("step 2 waits for the job then for stabilizer to become Available again", func(t *testing.T) {
		var sawJobWait, sawDeployWait bool
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case cmdContains(name, args, "get", "job/helm-install-stabilizer"):
				return exectest.Outcome{}
			case cmdContains(name, args, "wait", "--for=condition=complete", "job/helm-install-stabilizer"):
				sawJobWait = true
				return exectest.Outcome{}
			case cmdContains(name, args, "wait", "--for=condition=Available", "deployment.apps/stabilizer"):
				sawDeployWait = true
				return exectest.Outcome{}
			}
			return exectest.Outcome{}
		}}
		steps := stabilizerVersionApplySteps("kube-system", "stabilizer", patch, "1.3.0")
		ch := make(chan installsteps.StepMsg, 100)
		exectest.WithFakeRunner(r, func() { steps[1].Run(config.Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.Err != nil {
			t.Fatalf("step 2 err = %v, want nil", done.Err)
		}
		if !sawJobWait || !sawDeployWait {
			t.Errorf("step 2 didn't wait for both the job and the deployment: job=%v deploy=%v", sawJobWait, sawDeployWait)
		}
	})

	t.Run("step 2 never hard-fails on a job-completion-wait timeout - the patch already landed", func(t *testing.T) {
		// Regression test: k3s's helm-controller replaces (rather than
		// patches) the existing helm-install-<name> Job on a spec.version
		// change, which can take a while (see waitForStabilizerRolloutStep's
		// doc comment, stabilizer_settings_tui.go) - a timeout here used to
		// be reported as a flat "Failed" even though step 1's merge patch
		// had already committed. Must now be reported as done, not failed.
		var sawDeployWait bool
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case cmdContains(name, args, "get", "job/helm-install-stabilizer"):
				return exectest.Outcome{}
			case cmdContains(name, args, "wait", "--for=condition=complete", "job/helm-install-stabilizer"):
				return exectest.Outcome{Out: []byte("job failed"), Err: exectest.ErrFake}
			case cmdContains(name, args, "wait", "--for=condition=Available", "deployment.apps/stabilizer"):
				sawDeployWait = true
			}
			return exectest.Outcome{}
		}}
		steps := stabilizerVersionApplySteps("kube-system", "stabilizer", patch, "1.3.0")
		ch := make(chan installsteps.StepMsg, 100)
		exectest.WithFakeRunner(r, func() { steps[1].Run(config.Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.Err != nil {
			t.Fatalf("step 2 err = %v, want nil (informational only, not a hard failure)", done.Err)
		}
		if sawDeployWait {
			t.Error("must not wait on the deployment after the job-completion wait itself timed out")
		}
	})
}
