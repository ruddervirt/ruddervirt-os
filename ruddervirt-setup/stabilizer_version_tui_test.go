// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "patch", "helmchart.helm.cattle.io", "stabilizer") {
				for i, a := range args {
					if a == "-p" && i+1 < len(args) {
						patchArg = args[i+1]
					}
				}
			}
			return commandOutcome{}
		}}
		steps := stabilizerVersionApplySteps("kube-system", "stabilizer", patch, "1.3.0")
		ch := make(chan tea.Msg, 100)
		withFakeRunner(r, func() { steps[0].run(Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.err != nil {
			t.Fatalf("step 1 err = %v, want nil", done.err)
		}
		if patchArg != string(patch) {
			t.Errorf("patch body = %q, want exactly %q", patchArg, patch)
		}
	})

	t.Run("step 1 failure is reported, not swallowed", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte("boom"), err: errFake}
		}}
		steps := stabilizerVersionApplySteps("kube-system", "stabilizer", patch, "1.3.0")
		ch := make(chan tea.Msg, 100)
		withFakeRunner(r, func() { steps[0].run(Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.err == nil {
			t.Fatal("step 1 err = nil, want non-nil")
		}
	})

	t.Run("step 2 waits for the job then for stabilizer to become Available again", func(t *testing.T) {
		var sawJobWait, sawDeployWait bool
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			switch {
			case cmdContains(name, args, "get", "job/helm-install-stabilizer"):
				return commandOutcome{}
			case cmdContains(name, args, "wait", "--for=condition=complete", "job/helm-install-stabilizer"):
				sawJobWait = true
				return commandOutcome{}
			case cmdContains(name, args, "wait", "--for=condition=Available", "deployment.apps/stabilizer"):
				sawDeployWait = true
				return commandOutcome{}
			}
			return commandOutcome{}
		}}
		steps := stabilizerVersionApplySteps("kube-system", "stabilizer", patch, "1.3.0")
		ch := make(chan tea.Msg, 100)
		withFakeRunner(r, func() { steps[1].run(Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.err != nil {
			t.Fatalf("step 2 err = %v, want nil", done.err)
		}
		if !sawJobWait || !sawDeployWait {
			t.Errorf("step 2 didn't wait for both the job and the deployment: job=%v deploy=%v", sawJobWait, sawDeployWait)
		}
	})

	t.Run("step 2 propagates a job failure without waiting on the deployment", func(t *testing.T) {
		var sawDeployWait bool
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			switch {
			case cmdContains(name, args, "get", "job/helm-install-stabilizer"):
				return commandOutcome{}
			case cmdContains(name, args, "wait", "--for=condition=complete", "job/helm-install-stabilizer"):
				return commandOutcome{out: []byte("job failed"), err: errFake}
			case cmdContains(name, args, "wait", "--for=condition=Available", "deployment.apps/stabilizer"):
				sawDeployWait = true
			}
			return commandOutcome{}
		}}
		steps := stabilizerVersionApplySteps("kube-system", "stabilizer", patch, "1.3.0")
		ch := make(chan tea.Msg, 100)
		withFakeRunner(r, func() { steps[1].run(Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.err == nil {
			t.Fatal("step 2 err = nil, want non-nil")
		}
		if sawDeployWait {
			t.Error("must not wait on the deployment after the job itself failed")
		}
	})
}
