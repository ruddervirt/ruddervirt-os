// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec/exectest"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/stabilizer/settings"
)

// deploymentJSON/helmChartJSON/baseAppliedEnv duplicate
// internal/stabilizer/settings's own cli_test.go fixtures of the same name -
// kept independent rather than exported from production code purely for
// test fixtures, same reasoning as cmdContains/hasField (status_bridge_test.go).

// deploymentJSON builds a fake `kubectl get deploy stabilizer -o json`
// response with the given container env vars.
func deploymentJSON(env map[string]string) string {
	var b strings.Builder
	b.WriteString(`{"spec":{"template":{"spec":{"containers":[{"name":"stabilizer","env":[`)
	first := true
	for k, v := range env {
		if !first {
			b.WriteString(",")
		}
		first = false
		e, _ := json.Marshal(map[string]string{"name": k, "value": v})
		b.Write(e)
	}
	b.WriteString(`]}]}}}}`)
	return b.String()
}

func helmChartJSON(values map[string]any) string {
	out, _ := json.Marshal(map[string]any{"spec": map[string]any{"values": values}})
	return string(out)
}

// baseAppliedEnv is a full set of applied SETTING_* values matching every
// vendored setting's default, plus a fixed HELM_CHART_NAMESPACE/NAME pair -
// the steady-state, fully-settled starting point most tests build on.
func baseAppliedEnv() map[string]string {
	return map[string]string{
		"HELM_CHART_NAMESPACE":                    "kube-system",
		"HELM_CHART_NAME":                         "stabilizer",
		"SETTING_OLLAMA_ENABLED":                  "false",
		"SETTING_AILERON_UI_ENABLED":              "true",
		"SETTING_BUILD_MAX_CPU":                   "8",
		"SETTING_BUILD_MAX_MEMORY":                "16Gi",
		"SETTING_BUILD_MAX_DISK_SIZE":             "50Gi",
		"SETTING_BUILD_MAX_DISK_COUNT":            "3",
		"SETTING_BUILD_MAX_VM_COUNT":              "4",
		"SETTING_GRADING_MAX_CONCURRENT":          "10",
		"SETTING_WATCHDOG_ENABLED":                "true",
		"SETTING_WATCHDOG_VM_TIMEOUT_MINUTES":     "43200",
		"SETTING_WATCHDOG_MAX_VM_RUNTIME_MINUTES": "120",
	}
}

func TestLoadStabilizerSettingsStateCmd(t *testing.T) {
	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		switch {
		case cmdContains(name, args, "get", "deploy", "stabilizer"):
			return exectest.Outcome{Out: []byte(deploymentJSON(baseAppliedEnv()))}
		case cmdContains(name, args, "get", "helmchart.helm.cattle.io", "stabilizer"):
			return exectest.Outcome{Out: []byte(helmChartJSON(map[string]any{}))}
		case cmdContains(name, args, "get", "job", "helm-install-stabilizer"):
			return exectest.Outcome{Out: []byte("not found"), Err: exectest.ErrFake}
		}
		t.Fatalf("unexpected call: %s %v", name, args)
		return exectest.Outcome{}
	}}
	var msg tea.Msg
	exectest.WithFakeRunner(r, func() { msg = loadStabilizerSettingsStateCmd()() })
	got, ok := msg.(stabilizerSettingsLoadedMsg)
	if !ok || got.err != nil || got.state == nil {
		t.Fatalf("loadStabilizerSettingsStateCmd() = %#v, want a populated stabilizerSettingsLoadedMsg", msg)
	}
	if got.state.HelmChartNamespace != "kube-system" {
		t.Errorf("helmChartNamespace = %q, want kube-system", got.state.HelmChartNamespace)
	}

	// tuiKubectlExec must go through the interactive-sudo runPrivileged
	// path (not settingsKubectl's deliberately-unprivileged one) - a plain
	// -n-less "sudo" prefix (vs "sudo -n") confirms which path ran.
	if r.Calls == nil {
		t.Fatal("no calls recorded")
	}
	for _, c := range r.Calls {
		if strings.Contains(c, "sudo -n") {
			t.Errorf("call %q used non-interactive sudo (-n) - the TUI screen must use the interactive path like the rest of the TUI", c)
		}
	}
}

func TestResolveStabilizerSettingChange(t *testing.T) {
	def, _ := settings.StabilizerSettingByKey("build_max_cpu")
	state := &settings.StabilizerSettingsState{
		AppliedEnv:     map[string]string{"SETTING_BUILD_MAX_CPU": "8"},
		DeclaredValues: map[string]any{},
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
		declaredState := &settings.StabilizerSettingsState{
			AppliedEnv: map[string]string{"SETTING_BUILD_MAX_CPU": "8"},
			DeclaredValues: map[string]any{
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
	def, _ := settings.StabilizerSettingByKey("build_max_cpu")

	t.Run("aileron disabled reports not editable", func(t *testing.T) {
		state := &settings.StabilizerSettingsState{AppliedEnv: map[string]string{}, AileronDisabled: true}
		value, status, editable := stabilizerSettingRowDisplay(def, state)
		if editable {
			t.Error("want editable=false when aileron is disabled")
		}
		if value != "-" || !strings.Contains(status, "disabled") {
			t.Errorf("value/status = %q/%q, want a clear disabled indication", value, status)
		}
	})

	t.Run("settled", func(t *testing.T) {
		state := &settings.StabilizerSettingsState{
			AppliedEnv:     map[string]string{"SETTING_BUILD_MAX_CPU": "8"},
			DeclaredValues: map[string]any{},
		}
		value, status, editable := stabilizerSettingRowDisplay(def, state)
		if !editable || value != "8" || status != "settled (chart default)" {
			t.Errorf("got %q/%q/%v, want 8/settled (chart default)/true", value, status, editable)
		}
	})

	t.Run("rollout pending", func(t *testing.T) {
		state := &settings.StabilizerSettingsState{
			AppliedEnv: map[string]string{"SETTING_BUILD_MAX_CPU": "8"},
			DeclaredValues: map[string]any{
				"aileron": map[string]any{"buildLimits": map[string]any{"maxCPU": float64(16)}},
			},
		}
		value, status, editable := stabilizerSettingRowDisplay(def, state)
		if !editable || value != "8" || !strings.Contains(status, "rollout pending") || !strings.Contains(status, "16") {
			t.Errorf("got %q/%q/%v, want 8/rollout pending -> 16/true", value, status, editable)
		}
	})

	t.Run("aileron_ui_enabled stays editable even when other aileron settings are disabled", func(t *testing.T) {
		uiDef, _ := settings.StabilizerSettingByKey("aileron_ui_enabled")
		state := &settings.StabilizerSettingsState{
			AppliedEnv:      map[string]string{"SETTING_AILERON_UI_ENABLED": "false"},
			AileronDisabled: true,
		}
		_, _, editable := stabilizerSettingRowDisplay(uiDef, state)
		if !editable {
			t.Error("aileron_ui_enabled should stay editable regardless of aileronDisabled")
		}
	})
}

func TestStabilizerSettingListValue(t *testing.T) {
	def, _ := settings.StabilizerSettingByKey("build_max_cpu")

	settled := &settings.StabilizerSettingsState{AppliedEnv: map[string]string{"SETTING_BUILD_MAX_CPU": "8"}, DeclaredValues: map[string]any{}}
	if v, _ := stabilizerSettingListValue(def, settled); v != "8" {
		t.Errorf("settled display = %q, want bare \"8\" (no status suffix)", v)
	}

	pending := &settings.StabilizerSettingsState{
		AppliedEnv:     map[string]string{"SETTING_BUILD_MAX_CPU": "8"},
		DeclaredValues: map[string]any{"aileron": map[string]any{"buildLimits": map[string]any{"maxCPU": float64(16)}}},
	}
	if v, _ := stabilizerSettingListValue(def, pending); !strings.Contains(v, "8") || !strings.Contains(v, "rollout pending") {
		t.Errorf("pending display = %q, want it to mention both the applied value and rollout pending", v)
	}
}

func TestStabilizerSettingsApplySteps(t *testing.T) {
	def, _ := settings.StabilizerSettingByKey("build_max_cpu")

	t.Run("has exactly two steps: patch, then wait for rollout", func(t *testing.T) {
		steps := stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)
		if len(steps) != 2 {
			t.Fatalf("got %d steps, want 2", len(steps))
		}
	})

	t.Run("step 1 writes a merge patch under spec.values only", func(t *testing.T) {
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
		steps := stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)
		ch := make(chan installsteps.StepMsg, 100)
		exectest.WithFakeRunner(r, func() { steps[0].Run(config.Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.Err != nil {
			t.Fatalf("step 1 err = %v, want nil", done.Err)
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
		cpu, ok := settings.GetByPath(patch.Spec.Values, "aileron.buildLimits.maxCPU")
		if !ok {
			t.Fatal("patch missing aileron.buildLimits.maxCPU")
		}
		if n, ok := cpu.(float64); !ok || n != 16 {
			t.Errorf("aileron.buildLimits.maxCPU = %v (%T), want the NUMBER 16", cpu, cpu)
		}
	})

	t.Run("step 1 failure is reported, not swallowed", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("boom"), Err: exectest.ErrFake}
		}}
		steps := stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)
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
		steps := stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)
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
		// patches) the existing helm-install-<name> Job on a spec change,
		// which can take a while (see
		// waitForStabilizerRolloutStep's doc comment) - a timeout here used
		// to be reported as a flat "Failed" even though step 1's merge patch
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
		steps := stabilizerSettingsApplySteps("kube-system", "stabilizer", def, 16)
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

	t.Run("step 2 never hard-fails when the helm-install job never shows up in time", func(t *testing.T) {
		// Same regression as above, but for the OTHER half of the replace
		// dance: the job never even appears within the poll window.
		step := waitForStabilizerRolloutStepWithPoll("kube-system", "stabilizer", 2, time.Millisecond)
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("not found"), Err: exectest.ErrFake} // job never appears
		}}
		ch := make(chan installsteps.StepMsg, 100)
		exectest.WithFakeRunner(r, func() { step.Run(config.Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.Err != nil {
			t.Fatalf("err = %v, want nil (informational only, not a hard failure)", done.Err)
		}
	})
}
