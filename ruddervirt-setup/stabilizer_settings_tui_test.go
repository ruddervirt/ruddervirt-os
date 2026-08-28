// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"encoding/json"
	"strings"
	"testing"

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

func TestApplyStabilizerSettingPatchCmd(t *testing.T) {
	def, _ := stabilizerSettingByKey("build_max_cpu")

	t.Run("success writes a merge patch under spec.values only", func(t *testing.T) {
		var patchArg string
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "patch", "helmchart.helm.cattle.io", "stabilizer") {
				for i, a := range args {
					if a == "-p" && i+1 < len(args) {
						patchArg = args[i+1]
					}
				}
				return commandOutcome{}
			}
			return commandOutcome{}
		}}
		var msg tea.Msg
		withFakeRunner(r, func() { msg = applyStabilizerSettingPatchCmd("kube-system", "stabilizer", def, 16)() })
		got, ok := msg.(stabilizerSettingsApplyResultMsg)
		if !ok || got.err != nil {
			t.Fatalf("applyStabilizerSettingPatchCmd() = %#v, want a nil-error result", msg)
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

	t.Run("failure is reported, not swallowed", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte("boom"), err: errFake}
		}}
		var msg tea.Msg
		withFakeRunner(r, func() { msg = applyStabilizerSettingPatchCmd("kube-system", "stabilizer", def, 16)() })
		got, ok := msg.(stabilizerSettingsApplyResultMsg)
		if !ok || got.err == nil {
			t.Fatalf("applyStabilizerSettingPatchCmd() = %#v, want a non-nil error", msg)
		}
	})
}
