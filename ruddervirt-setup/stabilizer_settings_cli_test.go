// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

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

func TestLoadStabilizerSettingsState(t *testing.T) {
	t.Run("happy path with default HelmChart coordinates", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			switch {
			case cmdContains(name, args, "get", "deploy", "stabilizer"):
				return commandOutcome{out: []byte(deploymentJSON(baseAppliedEnv()))}
			case cmdContains(name, args, "get", "helmchart.helm.cattle.io", "stabilizer"):
				return commandOutcome{out: []byte(helmChartJSON(map[string]any{}))}
			case cmdContains(name, args, "get", "job", "helm-install-stabilizer"):
				return commandOutcome{out: []byte(`Error from server (NotFound): jobs.batch "helm-install-stabilizer" not found`), err: errFake}
			}
			t.Fatalf("unexpected call: %s %v", name, args)
			return commandOutcome{}
		}}
		var state *stabilizerSettingsState
		var err error
		withFakeRunner(r, func() { state, err = loadStabilizerSettingsState(settingsKubectl) })
		if err != nil {
			t.Fatalf("loadStabilizerSettingsState err = %v, want nil", err)
		}
		if state.helmChartNamespace != "kube-system" || state.helmChartName != "stabilizer" {
			t.Errorf("HelmChart coordinates = %s/%s, want kube-system/stabilizer", state.helmChartNamespace, state.helmChartName)
		}
		if state.jobActive {
			t.Error("jobActive = true, want false (job not found = idle)")
		}
		if state.aileronDisabled {
			t.Error("aileronDisabled = true, want false (aileron SETTING_* vars present)")
		}
	})

	t.Run("custom HelmChart coordinates from the Deployment's own env", func(t *testing.T) {
		env := baseAppliedEnv()
		env["HELM_CHART_NAMESPACE"] = "custom-ns"
		env["HELM_CHART_NAME"] = "my-release"
		var sawCustomGet bool
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			switch {
			case cmdContains(name, args, "get", "deploy", "stabilizer"):
				return commandOutcome{out: []byte(deploymentJSON(env))}
			case cmdContains(name, args, "get", "helmchart.helm.cattle.io", "my-release"):
				sawCustomGet = true
				if !hasField(strings.Join(args, " "), "custom-ns") {
					t.Errorf("HelmChart get didn't target custom-ns: %v", args)
				}
				return commandOutcome{out: []byte(helmChartJSON(map[string]any{}))}
			case cmdContains(name, args, "get", "job", "helm-install-my-release"):
				return commandOutcome{out: []byte("not found"), err: errFake}
			}
			t.Fatalf("unexpected call: %s %v", name, args)
			return commandOutcome{}
		}}
		withFakeRunner(r, func() { _, _ = loadStabilizerSettingsState(settingsKubectl) })
		if !sawCustomGet {
			t.Error("never fetched the HelmChart resource at the Deployment-reported custom coordinates")
		}
	})

	t.Run("missing Deployment gives an install-first hint, not a raw kubectl error", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte(`Error from server (NotFound): deployments.apps "stabilizer" not found`), err: errFake}
		}}
		var err error
		withFakeRunner(r, func() { _, err = loadStabilizerSettingsState(settingsKubectl) })
		if err == nil || !strings.Contains(err.Error(), "Adopt to ruddervirt.com") {
			t.Errorf("err = %v, want a hint to run the adopt flow", err)
		}
	})

	t.Run("aileron subchart disabled is detected when no aileron SETTING_* vars are present", func(t *testing.T) {
		env := map[string]string{
			"SETTING_OLLAMA_ENABLED":     "false",
			"SETTING_AILERON_UI_ENABLED": "false", // always rendered even when aileron is off
			"SETTING_WATCHDOG_ENABLED":   "true",
		}
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			switch {
			case cmdContains(name, args, "get", "deploy"):
				return commandOutcome{out: []byte(deploymentJSON(env))}
			case cmdContains(name, args, "get", "helmchart"):
				return commandOutcome{out: []byte(helmChartJSON(map[string]any{}))}
			case cmdContains(name, args, "get", "job"):
				return commandOutcome{out: []byte("not found"), err: errFake}
			}
			return commandOutcome{}
		}}
		var state *stabilizerSettingsState
		withFakeRunner(r, func() { state, _ = loadStabilizerSettingsState(settingsKubectl) })
		if !state.aileronDisabled {
			t.Error("aileronDisabled = false, want true (no aileron buildLimits/grading env vars present)")
		}
	})

	t.Run("active job is detected", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			switch {
			case cmdContains(name, args, "get", "deploy"):
				return commandOutcome{out: []byte(deploymentJSON(baseAppliedEnv()))}
			case cmdContains(name, args, "get", "helmchart"):
				return commandOutcome{out: []byte(helmChartJSON(map[string]any{}))}
			case cmdContains(name, args, "get", "job"):
				return commandOutcome{out: []byte(`{"status":{"active":1}}`)}
			}
			return commandOutcome{}
		}}
		var state *stabilizerSettingsState
		withFakeRunner(r, func() { state, _ = loadStabilizerSettingsState(settingsKubectl) })
		if !state.jobActive {
			t.Error("jobActive = false, want true")
		}
	})
}

func TestPrintStabilizerSettingsReport(t *testing.T) {
	env := baseAppliedEnv()
	state := &stabilizerSettingsState{
		appliedEnv: env,
		declaredValues: map[string]any{
			"aileron": map[string]any{
				"buildLimits": map[string]any{
					"maxCPU": float64(16), // declared differs from applied (8) - rollout pending
				},
			},
		},
		helmChartNamespace: "kube-system",
		helmChartName:      "stabilizer",
	}
	var buf bytes.Buffer
	printStabilizerSettingsReport(&buf, state)
	out := buf.String()

	if !strings.Contains(out, "build_max_cpu") || !strings.Contains(out, "rollout pending") {
		t.Errorf("report missing rollout-pending row for build_max_cpu:\n%s", out)
	}
	if !strings.Contains(out, "watchdog_enabled") || !strings.Contains(out, "settled") {
		t.Errorf("report missing settled row for watchdog_enabled:\n%s", out)
	}
}

func TestPrintStabilizerSettingsReport_AileronDisabled(t *testing.T) {
	state := &stabilizerSettingsState{
		appliedEnv: map[string]string{
			"SETTING_AILERON_UI_ENABLED": "false",
			"SETTING_WATCHDOG_ENABLED":   "true",
		},
		declaredValues:  map[string]any{},
		aileronDisabled: true,
	}
	var buf bytes.Buffer
	printStabilizerSettingsReport(&buf, state)
	out := buf.String()
	if !strings.Contains(out, "build_max_cpu") || !strings.Contains(out, "aileron subchart disabled") {
		t.Errorf("report should say aileron is disabled for build_max_cpu, not show a fake zero:\n%s", out)
	}
	if strings.Contains(out, "aileron_ui_enabled\t-") {
		t.Errorf("aileron_ui_enabled must still report its real value even when other aileron settings are disabled:\n%s", out)
	}
}

func TestApplyStabilizerSettingsChanges(t *testing.T) {
	newState := func() *stabilizerSettingsState {
		return &stabilizerSettingsState{
			appliedEnv:         baseAppliedEnv(),
			declaredValues:     map[string]any{},
			helmChartNamespace: "kube-system",
			helmChartName:      "stabilizer",
		}
	}

	t.Run("unknown key and out-of-bounds value are rejected before any kubectl write", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "patch") {
				t.Fatal("must not patch when validation fails")
			}
			return commandOutcome{}
		}}
		var code int
		withFakeRunner(r, func() {
			code = applyStabilizerSettingsChanges(newState(), []string{"nope=1", "build_max_cpu=99999"}, true)
		})
		if code == 0 {
			t.Error("want a non-zero exit code")
		}
	})

	t.Run("redundant write is skipped with no patch", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "patch") {
				t.Fatal("must not patch a value that's already applied and declared")
			}
			return commandOutcome{}
		}}
		var code int
		withFakeRunner(r, func() {
			code = applyStabilizerSettingsChanges(newState(), []string{"build_max_cpu=8"}, true)
		})
		if code != 0 {
			t.Errorf("code = %d, want 0 for a no-op", code)
		}
	})

	t.Run("refuses to write while a release operation is in flight", func(t *testing.T) {
		state := newState()
		state.jobActive = true
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "patch") {
				t.Fatal("must not patch while a release operation is in progress")
			}
			return commandOutcome{}
		}}
		var code int
		withFakeRunner(r, func() {
			code = applyStabilizerSettingsChanges(state, []string{"build_max_cpu=16"}, true)
		})
		if code == 0 {
			t.Error("want a non-zero exit code when refusing")
		}
	})

	t.Run("aileron settings refused when the subchart is disabled", func(t *testing.T) {
		state := newState()
		state.aileronDisabled = true
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "patch") {
				t.Fatal("must not patch an aileron setting while aileron is disabled")
			}
			return commandOutcome{}
		}}
		var code int
		withFakeRunner(r, func() {
			code = applyStabilizerSettingsChanges(state, []string{"build_max_cpu=16"}, true)
		})
		if code == 0 {
			t.Error("want a non-zero exit code")
		}
	})

	t.Run("happy path patches spec.values with correct JSON types, one merge patch", func(t *testing.T) {
		var patchArg string
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "patch", "helmchart.helm.cattle.io", "stabilizer") {
				for i, a := range args {
					if a == "-p" && i+1 < len(args) {
						patchArg = args[i+1]
					}
				}
				return commandOutcome{out: []byte("helmchart.helm.cattle.io/stabilizer patched")}
			}
			return commandOutcome{}
		}}
		var code int
		withFakeRunner(r, func() {
			code = applyStabilizerSettingsChanges(newState(), []string{"build_max_cpu=16", "watchdog_enabled=false"}, true)
		})
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if patchArg == "" {
			t.Fatal("no -p patch body was captured - patch was never issued")
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
		wd, ok := getByPath(patch.Spec.Values, "watchdog.enabled")
		if !ok {
			t.Fatal("patch missing watchdog.enabled")
		}
		if b, ok := wd.(bool); !ok || b != false {
			t.Errorf("watchdog.enabled = %v (%T), want the BOOLEAN false", wd, wd)
		}
		// Never rewrite spec.values wholesale - only spec.values itself and
		// nothing else (chart/version/failurePolicy/metadata) should appear
		// anywhere in the patch.
		if strings.Contains(patchArg, "failurePolicy") || strings.Contains(patchArg, "\"version\"") || strings.Contains(patchArg, "\"chart\"") {
			t.Errorf("patch touches fields it must never touch: %s", patchArg)
		}
	})
}

func TestUnknownAppliedSettingEnvVars(t *testing.T) {
	env := baseAppliedEnv()
	env["SETTING_NEW_THING_ADDED_LATER"] = "true"
	got := unknownAppliedSettingEnvVars(env)
	if len(got) != 1 || got[0] != "SETTING_NEW_THING_ADDED_LATER" {
		t.Errorf("unknownAppliedSettingEnvVars = %v, want [SETTING_NEW_THING_ADDED_LATER]", got)
	}

	if got := unknownAppliedSettingEnvVars(baseAppliedEnv()); len(got) != 0 {
		t.Errorf("unknownAppliedSettingEnvVars(fully known env) = %v, want none", got)
	}
}

func TestPrintStabilizerSettingsReport_WarnsOnUnknownSettingEnvVar(t *testing.T) {
	env := baseAppliedEnv()
	env["SETTING_FROM_THE_FUTURE"] = "42"
	state := &stabilizerSettingsState{appliedEnv: env, declaredValues: map[string]any{}}
	var buf bytes.Buffer
	printStabilizerSettingsReport(&buf, state)
	out := buf.String()
	if !strings.Contains(out, "SETTING_FROM_THE_FUTURE") {
		t.Errorf("report should warn about the unrecognized SETTING_* var:\n%s", out)
	}
}

func TestApplyStabilizerSettingsChanges_VersionGuidance(t *testing.T) {
	state := &stabilizerSettingsState{
		appliedEnv:         baseAppliedEnv(),
		declaredValues:     map[string]any{},
		helmChartNamespace: "kube-system",
		helmChartName:      "stabilizer",
	}
	r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
		if cmdContains(name, args, "patch") {
			t.Fatal("must not patch spec.version - version changes are out of scope")
		}
		return commandOutcome{}
	}}
	var code int
	withFakeRunner(r, func() {
		code = applyStabilizerSettingsChanges(state, []string{"version=v0.2.0"}, true)
	})
	if code == 0 {
		t.Error("want a non-zero exit code")
	}
}

func TestFriendlyKubectlError(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantHit bool
	}{
		{"permission denied", errPermissionDenied, true},
		{"connection refused", errConnectionRefused, true},
		{"unrelated error", errFake, false},
	}
	for _, c := range cases {
		got := friendlyKubectlError(c.err)
		hit := strings.Contains(got.Error(), "KUBECONFIG")
		if hit != c.wantHit {
			t.Errorf("%s: friendlyKubectlError hit=%v, want %v (%v)", c.name, hit, c.wantHit, got)
		}
	}
}

var (
	errPermissionDenied  = errWithMessage("open /etc/rancher/k3s/k3s.yaml: permission denied")
	errConnectionRefused = errWithMessage("The connection to the server localhost:8080 was refused")
)

type stringError string

func (e stringError) Error() string { return string(e) }

func errWithMessage(s string) error { return stringError(s) }
