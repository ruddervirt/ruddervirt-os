// SPDX-License-Identifier: GPL-3.0-only

package settings

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ruddervirt-setup/internal/exec/exectest"
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

// helmChartJSONFull is helmChartJSON plus the version/chart/valuesContent
// fields the guarded version-upgrade flow (stabilizer_upgrade.go) and the
// effectiveValues merge (LoadStabilizerSettingsState) also need - kept
// separate since most existing tests only care about spec.values.
func helmChartJSONFull(version, chart, valuesContent string, values map[string]any) string {
	out, _ := json.Marshal(map[string]any{"spec": map[string]any{
		"values":        values,
		"valuesContent": valuesContent,
		"version":       version,
		"chart":         chart,
	}})
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
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case exectest.CmdContains(name, args, "get", "deploy", "stabilizer"):
				return exectest.Outcome{Out: []byte(deploymentJSON(baseAppliedEnv()))}
			case exectest.CmdContains(name, args, "get", "helmchart.helm.cattle.io", "stabilizer"):
				return exectest.Outcome{Out: []byte(helmChartJSON(map[string]any{}))}
			case exectest.CmdContains(name, args, "get", "job", "helm-install-stabilizer"):
				return exectest.Outcome{Out: []byte(`Error from server (NotFound): jobs.batch "helm-install-stabilizer" not found`), Err: exectest.ErrFake}
			}
			t.Fatalf("unexpected call: %s %v", name, args)
			return exectest.Outcome{}
		}}
		var state *StabilizerSettingsState
		var err error
		exectest.WithFakeRunner(r, func() { state, err = LoadStabilizerSettingsState(settingsKubectl) })
		if err != nil {
			t.Fatalf("LoadStabilizerSettingsState err = %v, want nil", err)
		}
		if state.HelmChartNamespace != "kube-system" || state.HelmChartName != "stabilizer" {
			t.Errorf("HelmChart coordinates = %s/%s, want kube-system/stabilizer", state.HelmChartNamespace, state.HelmChartName)
		}
		if state.JobActive {
			t.Error("jobActive = true, want false (job not found = idle)")
		}
		if state.AileronDisabled {
			t.Error("aileronDisabled = true, want false (aileron SETTING_* vars present)")
		}
	})

	t.Run("custom HelmChart coordinates from the Deployment's own env", func(t *testing.T) {
		env := baseAppliedEnv()
		env["HELM_CHART_NAMESPACE"] = "custom-ns"
		env["HELM_CHART_NAME"] = "my-release"
		var sawCustomGet bool
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case exectest.CmdContains(name, args, "get", "deploy", "stabilizer"):
				return exectest.Outcome{Out: []byte(deploymentJSON(env))}
			case exectest.CmdContains(name, args, "get", "helmchart.helm.cattle.io", "my-release"):
				sawCustomGet = true
				if !exectest.HasField(strings.Join(args, " "), "custom-ns") {
					t.Errorf("HelmChart get didn't target custom-ns: %v", args)
				}
				return exectest.Outcome{Out: []byte(helmChartJSON(map[string]any{}))}
			case exectest.CmdContains(name, args, "get", "job", "helm-install-my-release"):
				return exectest.Outcome{Out: []byte("not found"), Err: exectest.ErrFake}
			}
			t.Fatalf("unexpected call: %s %v", name, args)
			return exectest.Outcome{}
		}}
		exectest.WithFakeRunner(r, func() { _, _ = LoadStabilizerSettingsState(settingsKubectl) })
		if !sawCustomGet {
			t.Error("never fetched the HelmChart resource at the Deployment-reported custom coordinates")
		}
	})

	t.Run("missing Deployment gives an install-first hint, not a raw kubectl error", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte(`Error from server (NotFound): deployments.apps "stabilizer" not found`), Err: exectest.ErrFake}
		}}
		var err error
		exectest.WithFakeRunner(r, func() { _, err = LoadStabilizerSettingsState(settingsKubectl) })
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
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case exectest.CmdContains(name, args, "get", "deploy"):
				return exectest.Outcome{Out: []byte(deploymentJSON(env))}
			case exectest.CmdContains(name, args, "get", "helmchart"):
				return exectest.Outcome{Out: []byte(helmChartJSON(map[string]any{}))}
			case exectest.CmdContains(name, args, "get", "job"):
				return exectest.Outcome{Out: []byte("not found"), Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		var state *StabilizerSettingsState
		exectest.WithFakeRunner(r, func() { state, _ = LoadStabilizerSettingsState(settingsKubectl) })
		if !state.AileronDisabled {
			t.Error("aileronDisabled = false, want true (no aileron buildLimits/grading env vars present)")
		}
	})

	t.Run("active job is detected", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case exectest.CmdContains(name, args, "get", "deploy"):
				return exectest.Outcome{Out: []byte(deploymentJSON(baseAppliedEnv()))}
			case exectest.CmdContains(name, args, "get", "helmchart"):
				return exectest.Outcome{Out: []byte(helmChartJSON(map[string]any{}))}
			case exectest.CmdContains(name, args, "get", "job"):
				return exectest.Outcome{Out: []byte(`{"status":{"active":1}}`)}
			}
			return exectest.Outcome{}
		}}
		var state *StabilizerSettingsState
		exectest.WithFakeRunner(r, func() { state, _ = LoadStabilizerSettingsState(settingsKubectl) })
		if !state.JobActive {
			t.Error("jobActive = false, want true")
		}
	})

	t.Run("effective declaredValues merges valuesContent (base) with values (override), and version/chart/self-upgrade env are captured", func(t *testing.T) {
		env := baseAppliedEnv()
		env["SELF_UPGRADE_ENABLED"] = "true"
		env["SELF_UPGRADE_ALLOWED_CHART"] = "oci://ghcr.io/ruddervirt/charts/stabilizer"
		env["SELF_UPGRADE_ALLOW_DOWNGRADE"] = "false"
		env["CHART_VERSION"] = "1.2.3"
		valuesContent := "aileron:\n  image:\n    tag: \"1.2.3\"\n  buildLimits:\n    maxCPU: 8\n"
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case exectest.CmdContains(name, args, "get", "deploy"):
				return exectest.Outcome{Out: []byte(deploymentJSON(env))}
			case exectest.CmdContains(name, args, "get", "helmchart"):
				return exectest.Outcome{Out: []byte(helmChartJSONFull("1.2.4", "oci://ghcr.io/ruddervirt/charts/stabilizer", valuesContent,
					map[string]any{"aileron": map[string]any{"buildLimits": map[string]any{"maxCPU": float64(16)}}}))}
			case exectest.CmdContains(name, args, "get", "job"):
				return exectest.Outcome{Out: []byte("not found"), Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		var state *StabilizerSettingsState
		var err error
		exectest.WithFakeRunner(r, func() { state, err = LoadStabilizerSettingsState(settingsKubectl) })
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		// values (maxCPU: 16) must win over valuesContent (maxCPU: 8) - the
		// override side of the merge.
		cpu, ok := GetByPath(state.DeclaredValues, "aileron.buildLimits.maxCPU")
		if !ok || cpu != float64(16) {
			t.Errorf("aileron.buildLimits.maxCPU = %v (ok=%v), want 16 (values overrides valuesContent)", cpu, ok)
		}
		// image.tag only lives in valuesContent - must still be visible in
		// the merged view (this was the exact gap the merge fixes).
		tag, ok := GetByPath(state.DeclaredValues, "aileron.image.tag")
		if !ok || tag != "1.2.3" {
			t.Errorf("aileron.image.tag = %v (ok=%v), want \"1.2.3\" from valuesContent alone", tag, ok)
		}
		if state.DeclaredVersion != "1.2.4" {
			t.Errorf("declaredVersion = %q, want 1.2.4", state.DeclaredVersion)
		}
		if state.DeclaredChart != "oci://ghcr.io/ruddervirt/charts/stabilizer" {
			t.Errorf("declaredChart = %q, want the OCI chart ref", state.DeclaredChart)
		}
		if state.HasLocalChartContent {
			t.Error("hasLocalChartContent = true, want false (no spec.chartContent set)")
		}
		if !state.SelfUpgradeEnabled {
			t.Error("selfUpgradeEnabled = false, want true (SELF_UPGRADE_ENABLED=true)")
		}
		if state.AllowedChart != "oci://ghcr.io/ruddervirt/charts/stabilizer" {
			t.Errorf("allowedChart = %q, want the OCI chart ref", state.AllowedChart)
		}
		if state.AllowDowngrade {
			t.Error("allowDowngrade = true, want false")
		}
		if state.AppliedChartVersion != "1.2.3" {
			t.Errorf("appliedChartVersion = %q, want 1.2.3 (from CHART_VERSION)", state.AppliedChartVersion)
		}
	})

	t.Run("malformed valuesContent YAML is a hard error, not silently ignored", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case exectest.CmdContains(name, args, "get", "deploy"):
				return exectest.Outcome{Out: []byte(deploymentJSON(baseAppliedEnv()))}
			case exectest.CmdContains(name, args, "get", "helmchart"):
				return exectest.Outcome{Out: []byte(helmChartJSONFull("1.0.0", "", "not: valid: yaml: [", map[string]any{}))}
			case exectest.CmdContains(name, args, "get", "job"):
				return exectest.Outcome{Out: []byte("not found"), Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		var err error
		exectest.WithFakeRunner(r, func() { _, err = LoadStabilizerSettingsState(settingsKubectl) })
		if err == nil {
			t.Fatal("err = nil, want an error for malformed spec.valuesContent YAML")
		}
	})
}

func TestPrintStabilizerSettingsReport(t *testing.T) {
	env := baseAppliedEnv()
	state := &StabilizerSettingsState{
		AppliedEnv: env,
		DeclaredValues: map[string]any{
			"aileron": map[string]any{
				"buildLimits": map[string]any{
					"maxCPU": float64(16), // declared differs from applied (8) - rollout pending
				},
			},
		},
		HelmChartNamespace: "kube-system",
		HelmChartName:      "stabilizer",
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
	state := &StabilizerSettingsState{
		AppliedEnv: map[string]string{
			"SETTING_AILERON_UI_ENABLED": "false",
			"SETTING_WATCHDOG_ENABLED":   "true",
		},
		DeclaredValues:  map[string]any{},
		AileronDisabled: true,
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
	newState := func() *StabilizerSettingsState {
		return &StabilizerSettingsState{
			AppliedEnv:         baseAppliedEnv(),
			DeclaredValues:     map[string]any{},
			HelmChartNamespace: "kube-system",
			HelmChartName:      "stabilizer",
		}
	}

	t.Run("unknown key and out-of-bounds value are rejected before any kubectl write", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "patch") {
				t.Fatal("must not patch when validation fails")
			}
			return exectest.Outcome{}
		}}
		var code int
		exectest.WithFakeRunner(r, func() {
			code = applyStabilizerSettingsChanges(newState(), []string{"nope=1", "build_max_cpu=99999"}, true)
		})
		if code == 0 {
			t.Error("want a non-zero exit code")
		}
	})

	t.Run("redundant write is skipped with no patch", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "patch") {
				t.Fatal("must not patch a value that's already applied and declared")
			}
			return exectest.Outcome{}
		}}
		var code int
		exectest.WithFakeRunner(r, func() {
			code = applyStabilizerSettingsChanges(newState(), []string{"build_max_cpu=8"}, true)
		})
		if code != 0 {
			t.Errorf("code = %d, want 0 for a no-op", code)
		}
	})

	t.Run("refuses to write while a release operation is in flight", func(t *testing.T) {
		state := newState()
		state.JobActive = true
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "patch") {
				t.Fatal("must not patch while a release operation is in progress")
			}
			return exectest.Outcome{}
		}}
		var code int
		exectest.WithFakeRunner(r, func() {
			code = applyStabilizerSettingsChanges(state, []string{"build_max_cpu=16"}, true)
		})
		if code == 0 {
			t.Error("want a non-zero exit code when refusing")
		}
	})

	t.Run("aileron settings refused when the subchart is disabled", func(t *testing.T) {
		state := newState()
		state.AileronDisabled = true
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "patch") {
				t.Fatal("must not patch an aileron setting while aileron is disabled")
			}
			return exectest.Outcome{}
		}}
		var code int
		exectest.WithFakeRunner(r, func() {
			code = applyStabilizerSettingsChanges(state, []string{"build_max_cpu=16"}, true)
		})
		if code == 0 {
			t.Error("want a non-zero exit code")
		}
	})

	t.Run("happy path patches spec.values with correct JSON types, one merge patch", func(t *testing.T) {
		var patchArg string
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "patch", "helmchart.helm.cattle.io", "stabilizer") {
				for i, a := range args {
					if a == "-p" && i+1 < len(args) {
						patchArg = args[i+1]
					}
				}
				return exectest.Outcome{Out: []byte("helmchart.helm.cattle.io/stabilizer patched")}
			}
			return exectest.Outcome{}
		}}
		var code int
		exectest.WithFakeRunner(r, func() {
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
		cpu, ok := GetByPath(patch.Spec.Values, "aileron.buildLimits.maxCPU")
		if !ok {
			t.Fatal("patch missing aileron.buildLimits.maxCPU")
		}
		if n, ok := cpu.(float64); !ok || n != 16 {
			t.Errorf("aileron.buildLimits.maxCPU = %v (%T), want the NUMBER 16", cpu, cpu)
		}
		wd, ok := GetByPath(patch.Spec.Values, "watchdog.enabled")
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
	state := &StabilizerSettingsState{AppliedEnv: env, DeclaredValues: map[string]any{}}
	var buf bytes.Buffer
	printStabilizerSettingsReport(&buf, state)
	out := buf.String()
	if !strings.Contains(out, "SETTING_FROM_THE_FUTURE") {
		t.Errorf("report should warn about the unrecognized SETTING_* var:\n%s", out)
	}
}

func TestApplyStabilizerSettingsChanges_VersionGuidance(t *testing.T) {
	state := &StabilizerSettingsState{
		AppliedEnv:         baseAppliedEnv(),
		DeclaredValues:     map[string]any{},
		HelmChartNamespace: "kube-system",
		HelmChartName:      "stabilizer",
	}
	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		if exectest.CmdContains(name, args, "patch") {
			t.Fatal("must not patch spec.version - version changes are out of scope")
		}
		return exectest.Outcome{}
	}}
	var code int
	exectest.WithFakeRunner(r, func() {
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
		{"unrelated error", exectest.ErrFake, false},
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
