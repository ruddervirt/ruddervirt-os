// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"encoding/json"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// This file is the interactive-TUI counterpart to stabilizer_settings_cli.go
// (the `ruddervirt-setup settings` SSH subcommand): a "Stabilizer Settings"
// screen, reached from Settings -> Advanced once a node has adopted
// stabilizer (replacing the "Adopt to ruddervirt.com" row there - see
// settingsRows, view.go). Same underlying state (stabilizerSettingsState)
// and validation (parseStabilizerSettingValue et al., all in
// stabilizer_settings_validate.go) as the CLI - only how kubectl actually
// runs, and how the result is presented, differ.
//
// Unlike the CLI (which accepts a batch of --set key=value flags and
// applies them all in one merge patch), this screen edits ONE setting at a
// time, mirroring the existing Settings screen's own edit-and-save-
// immediately shape (settingsEditing/settingsPicking in app_update.go) -
// simpler to build correctly, and consistent with how every other setting
// in this app already works.

// tuiKubectlExec is the kubectlExecFunc this screen passes to
// loadStabilizerSettingsState - unlike settingsKubectl (the CLI's
// deliberately-unprivileged exec, see stabilizer_settings_cli.go's header
// comment), this goes through the same interactive-sudo runPrivileged path
// every other TUI-driven kubectl call already uses (e.g. adoptAileronStep,
// applyStabilizer) - the TUI already assumes it can prompt for sudo
// throughout its lifecycle, so there's no reason for this one screen to be
// the exception.
func tuiKubectlExec(args ...string) ([]byte, error) {
	return runPrivileged(kubectlBinPath, args...).CombinedOutput()
}

// stabilizerSettingsLoadedMsg carries loadStabilizerSettingsStateCmd's
// result back into Update.
type stabilizerSettingsLoadedMsg struct {
	state *stabilizerSettingsState
	err   error
}

// loadStabilizerSettingsStateCmd fetches live cluster state off the UI
// thread, so screenStabilizerSettingsLoading's message can actually show
// while the (possibly sudo-prompting) kubectl calls run.
func loadStabilizerSettingsStateCmd() tea.Cmd {
	return func() tea.Msg {
		state, err := loadStabilizerSettingsState(tuiKubectlExec)
		return stabilizerSettingsLoadedMsg{state: state, err: err}
	}
}

// resolveStabilizerSettingChange validates raw against def (accepting the
// "unlimited" keyword, bounds, quantity syntax - same rules as the CLI's
// --set) and reports what it should be compared against
// (effectiveCurrentValue - declared if present, even mid-rollout, else
// applied). Shared between the list screen's picker (bool settings) and
// free-text edit (int/quantity settings) paths, since both need exactly
// this validate-then-decide-if-its-actually-a-change step before ever
// showing a confirmation.
func resolveStabilizerSettingChange(state *stabilizerSettingsState, def stabilizerSettingDef, raw string) (value any, current any, err error) {
	value, err = parseStabilizerSettingValue(def, raw)
	if err != nil {
		return nil, nil, err
	}
	return value, effectiveCurrentValue(state, def), nil
}

// stabilizerSettingRowDisplay computes what the interactive list screen
// shows for one setting - reuses exactly the same applied/declared
// comparison rules printStabilizerSettingsReport (the CLI) does, just
// returning a (value, status, editable) tuple instead of writing a table
// row, since the list screen needs to know editable to decide whether
// Enter should do anything.
func stabilizerSettingRowDisplay(d stabilizerSettingDef, state *stabilizerSettingsState) (value, status string, editable bool) {
	if d.Component == "aileron" && d.Key != "aileron_ui_enabled" && state.aileronDisabled {
		return "-", "aileron subchart disabled", false
	}

	appliedRaw, present := state.appliedEnv[d.Env]
	if !present {
		return "?", "no data (newer stabilizer?)", false
	}
	applied, err := parseStabilizerSettingValue(d, appliedRaw)
	if err != nil {
		return appliedRaw, "unparseable applied value", false
	}

	display := formatStabilizerSettingValue(d, applied)
	status = "settled"
	if declRaw, ok := getByPath(state.declaredValues, d.Path); ok {
		if declared, ok := coerceJSONValue(d, declRaw); ok {
			if !stabilizerSettingValuesEqual(d, applied, declared) {
				status = "rollout pending -> " + formatStabilizerSettingValue(d, declared)
			}
		} else {
			status = "declared value doesn't parse"
		}
	} else {
		status = "settled (chart default)"
	}
	return display, status, true
}

// stabilizerSettingListValue is stabilizerSettingRowDisplay collapsed into
// one display string for the list screen's single Value column - the
// common case ("settled"/"settled (chart default)") stays a bare value, and
// anything worth flagging (rollout pending, aileron disabled, no data,
// unparseable) is appended in parens instead of needing its own column.
func stabilizerSettingListValue(d stabilizerSettingDef, state *stabilizerSettingsState) (display string, editable bool) {
	value, status, editable := stabilizerSettingRowDisplay(d, state)
	switch status {
	case "settled", "settled (chart default)":
		return value, editable
	default:
		return fmt.Sprintf("%s (%s)", value, status), editable
	}
}

// stabilizerSettingsApplyResultMsg carries applyStabilizerSettingPatchCmd's
// result back into Update.
type stabilizerSettingsApplyResultMsg struct {
	err error
}

// applyStabilizerSettingPatchCmd writes exactly one setting's leaf under
// spec.values via a JSON merge patch - same shape/rules as the CLI's own
// patch (buildNestedPatch-equivalent via setByPath, real JSON types, never
// spec.set, never any field but spec.values) - just one leaf at a time
// since this screen edits one setting per confirm, and via runPrivileged
// (interactive sudo) rather than the CLI's unprivileged settingsKubectl.
func applyStabilizerSettingPatchCmd(helmChartNamespace, helmChartName string, def stabilizerSettingDef, value any) tea.Cmd {
	return func() tea.Msg {
		patch := map[string]any{}
		setByPath(patch, def.Path, value)
		patchJSON, err := json.Marshal(map[string]any{"spec": map[string]any{"values": patch}})
		if err != nil {
			return stabilizerSettingsApplyResultMsg{err: err}
		}
		out, err := runPrivileged(kubectlBinPath, "-n", helmChartNamespace, "patch", "helmchart.helm.cattle.io", helmChartName,
			"--type=merge", "-p", string(patchJSON)).CombinedOutput()
		if err != nil {
			return stabilizerSettingsApplyResultMsg{err: wrapCmdErr(out, err)}
		}
		return stabilizerSettingsApplyResultMsg{}
	}
}
