// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"encoding/json"
	"fmt"
	"time"

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
// result back into Update - handled silently in the background (see
// app_update.go's stabilizerDetectedMsg/stepDoneMsg cases that fire it),
// never navigating on its own; Settings just re-renders with fresher data
// once it lands.
type stabilizerSettingsLoadedMsg struct {
	state *stabilizerSettingsState
	err   error
}

// loadStabilizerSettingsStateCmd fetches live cluster state off the UI
// thread - fired once stabilizerDetectedMsg confirms stabilizer is
// present, and again after a successful settings change - never
// unconditionally, since the underlying kubectl calls can prompt for sudo.
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

// stabilizerSettingsApplySteps builds the two-step installStep pipeline
// screenStabilizerSettingsApply runs after a confirmed edit: patch, then
// actually WATCH the rollout to completion from inside the TUI. A plain
// "run `kubectl ... -w` yourself to watch it" message (which is what the
// SSH `settings` CLI prints - stabilizer_settings_cli.go - since it exits
// back to a real shell prompt) makes no sense here: the TUI holds the
// terminal in raw mode, so there's no shell for the operator to run that
// command in. This reuses exactly the same installStep/launchStep/
// stepOutputMsg/stepDoneMsg machinery the "Adopt to ruddervirt.com" flow
// (stabilizerSteps, stabilizer.go) already streams progress through, and
// its second step is intentionally the same shape as that flow's own
// waitForStabilizerReadyStep - wait for the helm-install job, then for the
// stabilizer Deployment to become Available again.
//
// Built fresh per confirmed edit (closures capturing helmChartNamespace/
// helmChartName/def/value) rather than a static package var like
// stabilizerSteps, since those vary per invocation and installStep.run's
// signature (func(cfg Config, ch chan<- tea.Msg)) has no other way to carry
// them in.
func stabilizerSettingsApplySteps(helmChartNamespace, helmChartName string, def stabilizerSettingDef, value any) []installStep {
	return []installStep{
		{
			label: fmt.Sprintf("Applying %s", def.Key),
			run: func(cfg Config, ch chan<- tea.Msg) {
				label := fmt.Sprintf("Applying %s", def.Key)
				patch := map[string]any{}
				setByPath(patch, def.Path, value)
				patchJSON, err := json.Marshal(map[string]any{"spec": map[string]any{"values": patch}})
				if err != nil {
					ch <- stepDoneMsg{label: label, err: err}
					return
				}
				ch <- stepOutputMsg(fmt.Sprintf("Patching %s/%s...", helmChartNamespace, helmChartName))
				err = runStreamed(ch, kubectlBinPath, "-n", helmChartNamespace, "patch", "helmchart.helm.cattle.io", helmChartName,
					"--type=merge", "-p", string(patchJSON))
				ch <- stepDoneMsg{label: label, err: err}
			},
		},
		waitForStabilizerRolloutStep(helmChartNamespace, helmChartName),
	}
}

// waitForStabilizerRolloutStep is the second half of both
// stabilizerSettingsApplySteps above and stabilizerVersionApplySteps
// (stabilizer_version_tui.go): watch the patch already committed by step 1
// actually roll out.
//
// k3s's helm-controller does NOT patch an existing helm-install-<name> Job
// in place when spec.values/spec.version changes its pod template - Jobs
// are immutable, so a change to either instead drives a multi-step replace:
// suspend the old job, wait for the job controller to sync that suspension
// AND for its pods to fully terminate, delete it, create a new (suspended)
// job, then resume it only once ITS creation has synced back. Each of those
// handoffs is its own controller reconcile, gated on requeue/backoff rather
// than resolving inline - the whole dance can legitimately take several
// minutes, with a real window where `kubectl get job/helm-install-<name>`
// returns NotFound in between the old job's deletion and the new one's
// creation (confirmed against k3s-io/helm-controller's own reconcile
// source, pkg/controllers/chart/chart.go's reconcileJob).
//
// Because of that, this step is confirmation-only, never the source of
// truth on success: the merge patch immediately before it has ALREADY
// committed durably to the server by the time this runs. So nothing here
// reports a hard failure on a timeout - a wait that runs out just means
// "couldn't confirm in time", not "didn't happen", and is surfaced as an
// informational log line instead of flipping the whole apply to Failed,
// which is exactly the false negative this was written to stop: an
// operator seeing "Failed" here for a change that, checked a few minutes
// later, had actually landed.
func waitForStabilizerRolloutStep(helmChartNamespace, helmChartName string) installStep {
	// 180x5s = 15 minutes - generous on purpose (see the replace dance
	// explained above), not the couple of minutes a naive "the controller
	// should react quickly" assumption would use.
	return waitForStabilizerRolloutStepWithPoll(helmChartNamespace, helmChartName, 180, 5*time.Second)
}

// waitForStabilizerRolloutStepWithPoll is waitForStabilizerRolloutStep with
// the job-appearance poll's attempts/interval pulled out, purely so tests
// can exercise the "job never showed up in time" branch in milliseconds
// instead of the real 15-minute wait.
func waitForStabilizerRolloutStepWithPoll(helmChartNamespace, helmChartName string, pollAttempts int, pollInterval time.Duration) installStep {
	return installStep{
		label: "Waiting for the rollout to complete",
		run: func(cfg Config, ch chan<- tea.Msg) {
			const label = "Waiting for the rollout to complete"
			jobName := "job/helm-install-" + helmChartName

			if err := pollUntil(ch, "Waiting for the stabilizer helm-install job", pollAttempts, pollInterval, func() bool {
				return runPrivileged(kubectlBinPath, "-n", helmChartNamespace, "get", jobName).Run() == nil
			}); err != nil {
				ch <- stepOutputMsg("The change was already applied - the helm-install job just hasn't shown up within the wait window yet (k3s's helm-controller can take a while to replace an in-flight release job). Check Settings again in a few minutes.")
				ch <- stepDoneMsg{label: label}
				return
			}
			if err := runStreamed(ch, kubectlBinPath, "-n", helmChartNamespace, "wait", "--for=condition=complete", jobName, "--timeout=600s"); err != nil {
				ch <- stepOutputMsg("The change was already applied - couldn't confirm the job finished within the wait window. Check Settings again in a few minutes.")
				ch <- stepDoneMsg{label: label}
				return
			}
			ch <- stepOutputMsg("Waiting for stabilizer to become ready again...")
			if err := runStreamed(ch, kubectlBinPath, "-n", stabilizerNamespace, "wait",
				"--for=condition=Available", "deployment.apps/stabilizer", "--timeout=600s"); err != nil {
				ch <- stepOutputMsg("The change was already applied - couldn't confirm stabilizer became ready again within the wait window. Check Settings again in a few minutes.")
			}
			ch <- stepDoneMsg{label: label}
		},
	}
}
