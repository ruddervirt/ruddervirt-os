// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLoadStabilizerSettingsStateCmd(t *testing.T) {
	r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
		switch {
		case cmdContains(name, args, "get", "deploy", "stabilizer"):
			return commandOutcome{out: []byte(deploymentJSON(baseAppliedEnv()))}
		case cmdContains(name, args, "get", "helmchart.helm.cattle.io", "stabilizer"):
			return commandOutcome{out: []byte(helmChartJSON(map[string]any{}))}
		case cmdContains(name, args, "get", "job", "helm-install-stabilizer"):
			return commandOutcome{out: []byte("not found"), err: errFake}
		}
		t.Fatalf("unexpected call: %s %v", name, args)
		return commandOutcome{}
	}}
	var msg tea.Msg
	withFakeRunner(r, func() { msg = loadStabilizerSettingsStateCmd()() })
	got, ok := msg.(stabilizerSettingsLoadedMsg)
	if !ok || got.err != nil || got.state == nil {
		t.Fatalf("loadStabilizerSettingsStateCmd() = %#v, want a populated stabilizerSettingsLoadedMsg", msg)
	}
	if got.state.helmChartNamespace != "kube-system" {
		t.Errorf("helmChartNamespace = %q, want kube-system", got.state.helmChartNamespace)
	}

	// tuiKubectlExec must go through the interactive-sudo runPrivileged
	// path (not settingsKubectl's deliberately-unprivileged one) - the
	// fakeRunner sees the sudo-wrapped command either way, but a plain
	// -n-less "sudo" prefix (vs "sudo -n") confirms which path ran.
	if r.calls == nil {
		t.Fatal("no calls recorded")
	}
	for _, c := range r.calls {
		if strings.Contains(c, "sudo -n") {
			t.Errorf("call %q used non-interactive sudo (-n) - the TUI screen must use the interactive path like the rest of the TUI", c)
		}
	}
}

func TestResolveStabilizerSettingChange(t *testing.T) {
	def, _ := stabilizerSettingByKey("build_max_cpu")
	state := &stabilizerSettingsState{
		appliedEnv:     map[string]string{"SETTING_BUILD_MAX_CPU": "8"},
		declaredValues: map[string]any{},
	}

	t.Run("valid change", func(t *testing.T) {
		value, current, err := resolveStabilizerSettingChange(state, def, "16")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if value != 16 {
			t.Errorf("value = %v, want 16", value)
		}
		if current != 8 {
			t.Errorf("current = %v, want 8 (applied)", current)
		}
	})

	t.Run("invalid value", func(t *testing.T) {
		if _, _, err := resolveStabilizerSettingChange(state, def, "not-a-number"); err == nil {
			t.Error("want an error for an invalid value")
		}
	})

	t.Run("declared value takes precedence over applied", func(t *testing.T) {
		declaredState := &stabilizerSettingsState{
			appliedEnv: map[string]string{"SETTING_BUILD_MAX_CPU": "8"},
			declaredValues: map[string]any{
				"aileron": map[string]any{"buildLimits": map[string]any{"maxCPU": float64(20)}},
			},
		}
		_, current, err := resolveStabilizerSettingChange(declaredState, def, "20")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if current != 20 {
			t.Errorf("current = %v, want 20 (declared, even though applied is still 8)", current)
		}
	})
}

func TestStabilizerSettingRowDisplay(t *testing.T) {
	def, _ := stabilizerSettingByKey("build_max_cpu")

	t.Run("aileron disabled reports not editable", func(t *testing.T) {
		state := &stabilizerSettingsState{appliedEnv: map[string]string{}, aileronDisabled: true}
		value, status, editable := stabilizerSettingRowDisplay(def, state)
		if editable {
			t.Error("want editable=false when aileron is disabled")
		}
		if value != "-" || !strings.Contains(status, "disabled") {
			t.Errorf("value/status = %q/%q, want a clear disabled indication", value, status)
		}
	})

	t.Run("settled", func(t *testing.T) {
		state := &stabilizerSettingsState{
			appliedEnv:     map[string]string{"SETTING_BUILD_MAX_CPU": "8"},
			declaredValues: map[string]any{},
		}
		value, status, editable := stabilizerSettingRowDisplay(def, state)
		if !editable || value != "8" || status != "settled (chart default)" {
			t.Errorf("got %q/%q/%v, want 8/settled (chart default)/true", value, status, editable)
		}
	})

	t.Run("rollout pending", func(t *testing.T) {
		state := &stabilizerSettingsState{
			appliedEnv: map[string]string{"SETTING_BUILD_MAX_CPU": "8"},
			declaredValues: map[string]any{
				"aileron": map[string]any{"buildLimits": map[string]any{"maxCPU": float64(16)}},
			},
		}
		value, status, editable := stabilizerSettingRowDisplay(def, state)
		if !editable || value != "8" || !strings.Contains(status, "rollout pending") || !strings.Contains(status, "16") {
			t.Errorf("got %q/%q/%v, want 8/rollout pending -> 16/true", value, status, editable)
		}
	})

	t.Run("aileron_ui_enabled stays editable even when other aileron settings are disabled", func(t *testing.T) {
		uiDef, _ := stabilizerSettingByKey("aileron_ui_enabled")
		state := &stabilizerSettingsState{
			appliedEnv:      map[string]string{"SETTING_AILERON_UI_ENABLED": "false"},
			aileronDisabled: true,
		}
		_, _, editable := stabilizerSettingRowDisplay(uiDef, state)
		if !editable {
			t.Error("aileron_ui_enabled should stay editable regardless of aileronDisabled")
		}
	})
}

func TestStabilizerSettingListValue(t *testing.T) {
	def, _ := stabilizerSettingByKey("build_max_cpu")

	settled := &stabilizerSettingsState{appliedEnv: map[string]string{"SETTING_BUILD_MAX_CPU": "8"}, declaredValues: map[string]any{}}
	if v, _ := stabilizerSettingListValue(def, settled); v != "8" {
		t.Errorf("settled display = %q, want bare \"8\" (no status suffix)", v)
	}

	pending := &stabilizerSettingsState{
		appliedEnv:     map[string]string{"SETTING_BUILD_MAX_CPU": "8"},
		declaredValues: map[string]any{"aileron": map[string]any{"buildLimits": map[string]any{"maxCPU": float64(16)}}},
	}
	if v, _ := stabilizerSettingListValue(def, pending); !strings.Contains(v, "8") || !strings.Contains(v, "rollout pending") {
		t.Errorf("pending display = %q, want it to mention both the applied value and rollout pending", v)
	}
}

func TestStabilizerSettingsApplySteps(t *testing.T) {
	def, _ := stabilizerSettingByKey("build_max_cpu")

	t.Run("has exactly two steps: patch, then wait for rollout", func(t *testing.T) {
		steps := stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)
		if len(steps) != 2 {
			t.Fatalf("got %d steps, want 2", len(steps))
		}
	})

	t.Run("step 1 writes a merge patch under spec.values only", func(t *testing.T) {
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
		steps := stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)
		ch := make(chan tea.Msg, 100)
		withFakeRunner(r, func() { steps[0].run(Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.err != nil {
			t.Fatalf("step 1 err = %v, want nil", done.err)
		}
		if patchArg == "" {
			t.Fatal("no patch body captured")
		}
		var patch struct {
			Spec struct {
				Values map[string]any `json:"values"`
			} `json:"spec"`
		}
		if err := json.Unmarshal([]byte(patchArg), &patch); err != nil {
			t.Fatalf("patch body isn't valid JSON: %v\n%s", err, patchArg)
		}
		cpu, ok := getByPath(patch.Spec.Values, "aileron.buildLimits.maxCPU")
		if !ok {
			t.Fatal("patch missing aileron.buildLimits.maxCPU")
		}
		if n, ok := cpu.(float64); !ok || n != 16 {
			t.Errorf("aileron.buildLimits.maxCPU = %v (%T), want the NUMBER 16", cpu, cpu)
		}
	})

	t.Run("step 1 failure is reported, not swallowed", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte("boom"), err: errFake}
		}}
		steps := stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)
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
		steps := stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)
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

	t.Run("step 2 never hard-fails on a job-completion-wait timeout - the patch already landed", func(t *testing.T) {
		// Regression test: k3s's helm-controller replaces (rather than
		// patches) the existing helm-install-<name> Job on a spec change,
		// which can legitimately take a while (see
		// waitForStabilizerRolloutStep's doc comment) - a timeout here used
		// to be reported as a flat "Failed", even though the merge patch in
		// step 1 had already committed. It must now be reported as done
		// (with an informational log line), not failed.
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
		steps := stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)
		ch := make(chan tea.Msg, 100)
		withFakeRunner(r, func() { steps[1].run(Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.err != nil {
			t.Fatalf("step 2 err = %v, want nil (informational only, not a hard failure)", done.err)
		}
		if sawDeployWait {
			t.Error("must not wait on the deployment after the job-completion wait itself timed out")
		}
	})

	t.Run("step 2 never hard-fails when the helm-install job never shows up in time", func(t *testing.T) {
		// Same regression as above, but for the OTHER half of the replace
		// dance: the job never even appears within the poll window.
		step := waitForStabilizerRolloutStepWithPoll("kube-system", "stabilizer", 2, time.Millisecond)
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte("not found"), err: errFake} // job never appears
		}}
		ch := make(chan tea.Msg, 100)
		withFakeRunner(r, func() { step.run(Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.err != nil {
			t.Fatalf("err = %v, want nil (informational only, not a hard failure)", done.err)
		}
	})
}
