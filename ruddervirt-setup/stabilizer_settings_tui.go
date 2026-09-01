// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"encoding/json"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/k3s"
	"ruddervirt-setup/internal/stabilizer"
	"ruddervirt-setup/internal/stabilizer/settings"
)

// This file is the interactive-TUI counterpart to
// internal/stabilizer/settings/cli.go (the `ruddervirt-setup settings` SSH
// subcommand): a "Stabilizer Settings" screen, reached from Settings ->
// Advanced once a node has adopted stabilizer (replacing the "Adopt to
// ruddervirt.com" row there - see screens.SettingsModel.Rows,
// internal/tui/screens/settings.go). Same underlying state and validation
// as the CLI - only how kubectl runs and how results are presented differ.
//
// Unlike the CLI (batch --set key=value flags in one merge patch), this
// screen edits ONE setting at a time, mirroring the existing Settings
// screen's edit-and-save-immediately shape (settings.Editing/Picking,
// screens.SettingsModel).

// tuiKubectlExec is the kubectlExecFunc this screen passes to
// loadStabilizerSettingsState - unlike settingsKubectl (the CLI's
// deliberately-unprivileged exec, see cli.go's header comment), this goes
// through the same interactive-sudo runPrivileged path every other
// TUI-driven kubectl call uses, since the TUI already assumes it can prompt
// for sudo throughout its lifecycle.
func tuiKubectlExec(args ...string) ([]byte, error) {
	return exec.RunPrivileged(kubectlBinPath, args...).CombinedOutput()
}

// stabilizerSettingsLoadedMsg carries loadStabilizerSettingsStateCmd's
// result back into Update - handled silently in the background (see app.go's
// stabilizerDetectedMsg/stepDoneMsg), never navigating on its own; Settings
// just re-renders with fresher data once it lands.
type stabilizerSettingsLoadedMsg struct {
	state *settings.StabilizerSettingsState
	err   error
}

// loadStabilizerSettingsStateCmd fetches live cluster state off the UI
// thread - fired once stabilizer is confirmed present, and again after a
// successful settings change, never unconditionally, since the underlying
// kubectl calls can prompt for sudo.
func loadStabilizerSettingsStateCmd() tea.Cmd {
	return func() tea.Msg {
		state, err := settings.LoadStabilizerSettingsState(tuiKubectlExec)
		return stabilizerSettingsLoadedMsg{state: state, err: err}
	}
}

// resolveStabilizerSettingChange validates raw against def (same rules as
// the CLI's --set) and reports what to compare against
// (EffectiveCurrentValue: declared if present, even mid-rollout, else
// applied). Shared by the picker (bool settings) and free-text edit
// (int/quantity settings) paths, both needing this validate-then-decide-
// if-changed step before showing a confirmation.
func resolveStabilizerSettingChange(state *settings.StabilizerSettingsState, def settings.StabilizerSettingDef, raw string) (value any, current any, err error) {
	value, err = settings.ParseStabilizerSettingValue(def, raw)
	if err != nil {
		return nil, nil, err
	}
	return value, settings.EffectiveCurrentValue(state, def), nil
}

// stabilizerSettingRowDisplay computes what the interactive list screen
// shows for one setting - reuses the CLI's applied/declared comparison
// rules (printStabilizerSettingsReport), returning a (value, status,
// editable) tuple instead of a table row, since the list screen needs
// editable to decide whether Enter does anything.
func stabilizerSettingRowDisplay(d settings.StabilizerSettingDef, state *settings.StabilizerSettingsState) (value, status string, editable bool) {
	if d.Component == "aileron" && d.Key != "aileron_ui_enabled" && state.AileronDisabled {
		return "-", "aileron subchart disabled", false
	}

	appliedRaw, present := state.AppliedEnv[d.Env]
	if !present {
		return "?", "no data (newer stabilizer?)", false
	}
	applied, err := settings.ParseStabilizerSettingValue(d, appliedRaw)
	if err != nil {
		return appliedRaw, "unparseable applied value", false
	}

	display := settings.FormatStabilizerSettingValue(d, applied)
	status = "settled"
	if declRaw, ok := settings.GetByPath(state.DeclaredValues, d.Path); ok {
		if declared, ok := settings.CoerceJSONValue(d, declRaw); ok {
			if !settings.StabilizerSettingValuesEqual(d, applied, declared) {
				status = "rollout pending -> " + settings.FormatStabilizerSettingValue(d, declared)
			}
		} else {
			status = "declared value doesn't parse"
		}
	} else {
		status = "settled (chart default)"
	}
	return display, status, true
}

// stabilizerSettingListValue collapses stabilizerSettingRowDisplay into one
// display string for the list screen's single Value column: the common case
// ("settled"/"settled (chart default)") stays bare, anything worth flagging
// is appended in parens.
func stabilizerSettingListValue(d settings.StabilizerSettingDef, state *settings.StabilizerSettingsState) (display string, editable bool) {
	value, status, editable := stabilizerSettingRowDisplay(d, state)
	switch status {
	case "settled", "settled (chart default)":
		return value, editable
	default:
		return fmt.Sprintf("%s (%s)", value, status), editable
	}
}

// stabilizerSettingsApplySteps builds the two-step installsteps.Step pipeline
// screenStabilizerSettingsApply runs after a confirmed edit: patch, then
// actually WATCH the rollout to completion from inside the TUI. A plain "run
// `kubectl ... -w` yourself" message (what the SSH `settings` CLI prints,
// since it exits to a real shell) makes no sense here - the TUI holds the
// terminal in raw mode, so there's no shell to run that in. Reuses the same
// installsteps.Step/stepOutputMsg/stepDoneMsg machinery the "Adopt to
// ruddervirt.com" flow (stabilizer.AdoptSteps) streams progress through; its
// second step mirrors that flow's waitForStabilizerReadyStep - wait for the
// helm-install job, then for the Deployment to become Available again.
//
// Built fresh per confirmed edit (closures capturing helmChartNamespace/
// helmChartName/def/value) rather than a static package var, since
// installsteps.Step.Run's signature has no other way to carry them in.
func stabilizerSettingsApplySteps(helmChartNamespace, helmChartName string, def settings.StabilizerSettingDef, value any) []installsteps.Step {
	return []installsteps.Step{
		{
			Label: fmt.Sprintf("Applying %s", def.Key),
			Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
				label := fmt.Sprintf("Applying %s", def.Key)
				patch := map[string]any{}
				settings.SetByPath(patch, def.Path, value)
				patchJSON, err := json.Marshal(map[string]any{"spec": map[string]any{"values": patch}})
				if err != nil {
					ch <- stepDoneMsg{Label: label, Err: err}
					return
				}
				ch <- stepOutputMsg(fmt.Sprintf("Patching %s/%s...", helmChartNamespace, helmChartName))
				err = exec.RunStreamed(ch, wrapStepOutput, kubectlBinPath, "-n", helmChartNamespace, "patch", "helmchart.helm.cattle.io", helmChartName,
					"--type=merge", "-p", string(patchJSON))
				ch <- stepDoneMsg{Label: label, Err: err}
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
// in place when spec.values/spec.version changes its pod template - Jobs are
// immutable, so it instead drives a multi-step replace (suspend old job,
// wait for suspension sync and pod termination, delete, create new suspended
// job, resume once its creation syncs back). Each handoff is its own
// reconcile gated on requeue/backoff, so the whole dance can take several
// minutes, with a real window where `kubectl get job/helm-install-<name>`
// returns NotFound between the old job's deletion and the new one's
// creation (confirmed against k3s-io/helm-controller's reconcileJob).
//
// Because of that, this step is confirmation-only, never the source of
// truth on success - the merge patch before it already committed durably.
// So a timeout here never reports hard failure, just "couldn't confirm in
// time" as an informational log line rather than flipping to Failed - this
// avoids a false negative where an operator sees "Failed" for a change that,
// checked minutes later, had actually landed.
func waitForStabilizerRolloutStep(helmChartNamespace, helmChartName string) installsteps.Step {
	// 180x5s = 15 minutes - generous on purpose (see the replace dance above).
	return waitForStabilizerRolloutStepWithPoll(helmChartNamespace, helmChartName, 180, 5*time.Second)
}

// waitForStabilizerRolloutStepWithPoll is waitForStabilizerRolloutStep with
// the job-appearance poll's attempts/interval pulled out, purely so tests
// can exercise the "job never showed up in time" branch in milliseconds
// instead of the real 15-minute wait.
func waitForStabilizerRolloutStepWithPoll(helmChartNamespace, helmChartName string, pollAttempts int, pollInterval time.Duration) installsteps.Step {
	return installsteps.Step{
		Label: "Waiting for the rollout to complete",
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			const label = "Waiting for the rollout to complete"
			jobName := "job/helm-install-" + helmChartName

			if err := k3s.PollUntil(ch, wrapStepOutput, "Waiting for the stabilizer helm-install job", pollAttempts, pollInterval, func() bool {
				return exec.RunPrivileged(kubectlBinPath, "-n", helmChartNamespace, "get", jobName).Run() == nil
			}); err != nil {
				ch <- stepOutputMsg("The change was already applied - the helm-install job just hasn't shown up within the wait window yet (k3s's helm-controller can take a while to replace an in-flight release job). Check Settings again in a few minutes.")
				ch <- stepDoneMsg{Label: label}
				return
			}
			if err := exec.RunStreamed(ch, wrapStepOutput, kubectlBinPath, "-n", helmChartNamespace, "wait", "--for=condition=complete", jobName, "--timeout=600s"); err != nil {
				ch <- stepOutputMsg("The change was already applied - couldn't confirm the job finished within the wait window. Check Settings again in a few minutes.")
				ch <- stepDoneMsg{Label: label}
				return
			}
			ch <- stepOutputMsg("Waiting for stabilizer to become ready again...")
			if err := exec.RunStreamed(ch, wrapStepOutput, kubectlBinPath, "-n", stabilizer.StabilizerNamespace, "wait",
				"--for=condition=Available", "deployment.apps/stabilizer", "--timeout=600s"); err != nil {
				ch <- stepOutputMsg("The change was already applied - couldn't confirm stabilizer became ready again within the wait window. Check Settings again in a few minutes.")
			}
			ch <- stepDoneMsg{Label: label}
		},
	}
}
