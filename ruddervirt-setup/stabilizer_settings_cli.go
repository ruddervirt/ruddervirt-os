// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v3"
)

// This is the non-interactive `ruddervirt-setup settings` subcommand,
// dispatched in main.go before the TUI ever starts (an operator runs it
// over SSH, e.g. `ssh admin@host ruddervirt-setup settings --set
// build_max_cpu=16` - see ruddervirt-shell.sh's `-c` branch, which execs a
// given command directly without ever launching the menu).
//
// Deliberately does NOT go through runPrivileged/sudo the way the rest of
// this package's kubectl calls do. The k3s kubeconfig at
// /etc/rancher/k3s/k3s.yaml is root-only, so an unelevated `admin` shell
// simply can't reach the API server without either an exported KUBECONFIG
// pointing at a readable copy or running the whole command as root - and
// that (not a generic connection error) is expected to be the single most
// common failure mode here, so friendlyKubectlError below names it
// explicitly instead of surfacing kubectl's raw error text.
//
// Unlike a settings change from the cloud UI, which goes through
// stabilizer's own Go handler (internal/handler/upgrade.go) before ever
// reaching the cluster, a change from here writes to the HelmChart resource
// DIRECTLY - nothing else validates it. See
// stabilizer_settings_validate.go's parseStabilizerSettingValue for why
// that makes this tool the only validator on this path.

const (
	// defaultStabilizerHelmChartNamespace/Name are install.sh's own
	// defaults for the helm.cattle.io/v1 HelmChart resource that installs
	// stabilizer - --chart-namespace and the release name can change them,
	// so these are only the fallback when the running Deployment's own
	// HELM_CHART_NAMESPACE/HELM_CHART_NAME env vars (which install.sh
	// writes into spec.values.selfUpgrade on every run, for exactly this
	// reason) aren't available.
	defaultStabilizerHelmChartNamespace = "kube-system"
	defaultStabilizerHelmChartName      = "stabilizer"

	// stabilizerAgentDeploymentName/ContainerName are NOT configurable -
	// unlike the HelmChart resource's own identity, the release always
	// lands its agent Deployment at this fixed name in stabilizerNamespace
	// (ruddervirt-system, stabilizer.go).
	stabilizerAgentDeploymentName = "stabilizer"
	stabilizerAgentContainerName  = "stabilizer"

	settingsKubectlTimeout = 15 * time.Second
)

const settingsUsage = `Usage:
  ruddervirt-setup settings
      Report every stabilizer setting's applied and declared value.

  ruddervirt-setup settings --set key=value [--set key2=value2 ...] [-y|--yes]
      Change one or more settings. Prompts for confirmation first (skip
      with -y/--yes) - this restarts the whole stabilizer release.

  Use "unlimited" as a value for any setting that offers one, e.g.
      ruddervirt-setup settings --set build_max_cpu=unlimited
`

// stringSliceFlag collects repeated -set flags into a slice.
type stringSliceFlag []string

func (f *stringSliceFlag) String() string { return strings.Join(*f, ",") }
func (f *stringSliceFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// runSettingsCLI is main.go's entry point for `ruddervirt-setup settings
// ...`. Returns a process exit code.
func runSettingsCLI(args []string) int {
	var setFlags stringSliceFlag
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	fs.Var(&setFlags, "set", "change one setting: key=value (repeatable)")
	var yes bool
	fs.BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	fs.BoolVar(&yes, "y", false, "shorthand for --yes")
	fs.Usage = func() { fmt.Fprint(os.Stderr, settingsUsage) }
	if err := fs.Parse(args); err != nil {
		return 2
	}

	state, err := loadStabilizerSettingsState(settingsKubectl)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", friendlyKubectlError(err))
		return 1
	}

	if len(setFlags) == 0 {
		printStabilizerSettingsReport(os.Stdout, state)
		return 0
	}

	return applyStabilizerSettingsChanges(state, setFlags, yes)
}

// kubectlExecFunc abstracts a single kubectl invocation, returning its raw
// combined output and any error - see loadStabilizerSettingsState's doc
// comment for why this indirection exists.
type kubectlExecFunc func(args ...string) ([]byte, error)

// settingsKubectl runs kubectl WITHOUT sudo - see this file's header
// comment for why. Returns kubectl's raw combined output alongside any
// error, so callers can inspect the output text themselves (e.g.
// isNotFoundOutput) before deciding how to present a failure.
func settingsKubectl(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), settingsKubectlTimeout)
	defer cancel()
	return DefaultRunner.CombinedOutput(ctx, kubectlBinPath, args...)
}

// isNotFoundOutput reports whether out looks like kubectl's own "resource
// not found" error text, as opposed to any other failure.
func isNotFoundOutput(out []byte) bool {
	lower := strings.ToLower(string(out))
	return strings.Contains(lower, "notfound") || strings.Contains(lower, "not found")
}

// friendlyKubectlError rewrites a connection/permission-flavored kubectl
// failure into the specific guidance an operator needs - per the task this
// implements, an unelevated shell failing to read the root-only k3s
// kubeconfig is expected to be the single most common failure here by a
// wide margin, so it's worth naming explicitly rather than surfacing
// kubectl's raw (often cryptic) error text.
func friendlyKubectlError(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "permission denied"),
		strings.Contains(lower, "the connection to the server"),
		strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "unable to load"),
		strings.Contains(lower, "no configuration has been provided"):
		return fmt.Errorf("could not reach the Kubernetes API - try: export KUBECONFIG=/etc/rancher/k3s/k3s.yaml (or run as root)\n(%w)", err)
	}
	return err
}

// k8sDeploymentGetJSON is the minimal subset of `kubectl get deploy -o
// json` this package needs, same hand-rolled-struct approach used
// throughout this codebase (k8sNodeList, k3s.go; k8sSecretGetJSON,
// adopt.go) rather than a client-go/apimachinery dependency.
type k8sDeploymentGetJSON struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Name string `json:"name"`
					Env  []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"env"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

type k8sHelmChartGetJSON struct {
	Spec struct {
		Values map[string]any `json:"values"`
		// ValuesContent/Version/Chart/ChartContent back the guarded
		// chart-version-upgrade flow (stabilizer_upgrade.go) - see
		// loadStabilizerSettingsState's effectiveValues merge below for why
		// ValuesContent can't just be ignored the way it was before.
		ValuesContent string `json:"valuesContent"`
		Version       string `json:"version"`
		Chart         string `json:"chart"`
		ChartContent  string `json:"chartContent"`
	} `json:"spec"`
}

type k8sJobGetJSON struct {
	Status struct {
		Active int `json:"active"`
	} `json:"status"`
}

// stabilizerSettingsState is everything printStabilizerSettingsReport/
// applyStabilizerSettingsChanges need, gathered by loadStabilizerSettingsState
// in exactly three kubectl reads.
type stabilizerSettingsState struct {
	// appliedEnv is every SETTING_*-prefixed (plus HELM_CHART_*) env var
	// name/value pair rendered into the running stabilizer container - the
	// authoritative "what's actually running" state, since helm already
	// collapsed the entire values precedence stack to produce it.
	appliedEnv map[string]string
	// declaredValues is the EFFECTIVE values tree helm-controller would
	// actually resolve: spec.valuesContent (the previous run's
	// user-supplied values, carried forward verbatim by install.sh) merged
	// with spec.values (which wins) on top - mirrors stabilizer's own
	// effectiveValues/mergeInto (internal/handler/upgrade.go) exactly, since
	// comparing against spec.values alone would miss anything only ever
	// written into valuesContent. A setting's path missing from this tree
	// means "no explicit value declared; the chart default applies" - NOT
	// "off"/zero. spec.set is deliberately never merged in - see
	// buildUpgradePatch's doc comment in stabilizer_upgrade.go for why nothing
	// on this side ever reads or writes it either.
	declaredValues map[string]any

	helmChartNamespace string
	helmChartName      string

	// declaredVersion/declaredChart/hasLocalChartContent are the HelmChart
	// resource's own spec.version/spec.chart/spec.chartContent - read here
	// (rather than by a second kubectl round trip) because
	// stabilizer_upgrade.go's guards need all three for exactly the checks
	// stabilizer's own preflight (internal/handler/upgrade.go) applies.
	declaredVersion      string
	declaredChart        string
	hasLocalChartContent bool

	// selfUpgradeEnabled/allowedChart/allowDowngrade mirror the running
	// agent's own SelfUpgradeConfig, read off the same Deployment env this
	// state already parses (SELF_UPGRADE_ENABLED/SELF_UPGRADE_ALLOWED_CHART/
	// SELF_UPGRADE_ALLOW_DOWNGRADE - chart/stabilizer/templates/deployment.yaml)
	// - the guard this box-side tool must apply is exactly the guard the
	// agent itself would apply to the same request arriving over NATS.
	selfUpgradeEnabled bool
	allowedChart       string
	allowDowngrade     bool

	// appliedChartVersion is CHART_VERSION - the chart version actually
	// rendering the running pod, as opposed to declaredVersion (what the
	// HelmChart resource currently asks for): the two differ while an
	// upgrade is in flight or has failed.
	appliedChartVersion string

	// jobActive mirrors `kubectl get job helm-install-<name>
	// -o jsonpath='{.status.active}'` being non-zero - an upgrade or
	// another settings change already in flight. A missing job is the
	// normal idle state, not this.
	jobActive bool

	// aileronDisabled is true when none of the aileron buildLimits/grading
	// SETTING_* env vars are present at all - helm's `with` skips those
	// template blocks entirely when the aileron subchart is disabled, so
	// there is genuinely nothing to report for them, not real zeroes.
	// aileron_ui_enabled is unaffected (always rendered, via a `{{ and
	// .Values.aileron.enabled ... }}` guard rather than `with`).
	aileronDisabled bool
}

// loadStabilizerSettingsState gathers everything needed to read or change
// settings: the agent Deployment's rendered env (applied state, plus the
// HelmChart resource's own actual namespace/name - see this file's header
// comment on why those are read from the Deployment rather than assumed),
// the HelmChart resource's declared spec.values, and whether a release
// operation is already in flight.
//
// exec abstracts HOW each kubectl call actually runs, so this parsing/
// assembly logic is shared between two different exec strategies: the CLI
// subcommand (settingsKubectl, deliberately no sudo - see this file's
// header comment) and the interactive TUI's "Stabilizer Settings" screen
// (stabilizer_settings_tui.go), which instead uses the same interactive-sudo
// runPrivileged path every other TUI-driven kubectl call already does.
func loadStabilizerSettingsState(exec kubectlExecFunc) (*stabilizerSettingsState, error) {
	depOut, err := exec("-n", stabilizerNamespace, "get", "deploy", stabilizerAgentDeploymentName, "-o", "json")
	if err != nil {
		if isNotFoundOutput(depOut) {
			return nil, fmt.Errorf("stabilizer isn't installed on this cluster (no %s Deployment in %s) - run \"Adopt to ruddervirt.com\" (Settings -> Advanced) from the interactive menu first",
				stabilizerAgentDeploymentName, stabilizerNamespace)
		}
		return nil, wrapCmdErr(depOut, err)
	}
	var dep k8sDeploymentGetJSON
	if err := json.Unmarshal(depOut, &dep); err != nil {
		return nil, fmt.Errorf("parsing %s deployment: %w", stabilizerAgentDeploymentName, err)
	}

	appliedEnv := map[string]string{}
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name != stabilizerAgentContainerName {
			continue
		}
		for _, e := range c.Env {
			appliedEnv[e.Name] = e.Value
		}
	}
	if len(appliedEnv) == 0 {
		return nil, fmt.Errorf("container %q not found in the %s Deployment", stabilizerAgentContainerName, stabilizerAgentDeploymentName)
	}

	helmChartNamespace := appliedEnv["HELM_CHART_NAMESPACE"]
	if helmChartNamespace == "" {
		helmChartNamespace = defaultStabilizerHelmChartNamespace
	}
	helmChartName := appliedEnv["HELM_CHART_NAME"]
	if helmChartName == "" {
		helmChartName = defaultStabilizerHelmChartName
	}

	valuesOut, err := exec("-n", helmChartNamespace, "get", "helmchart.helm.cattle.io", helmChartName, "-o", "json")
	if err != nil {
		if isNotFoundOutput(valuesOut) {
			return nil, fmt.Errorf("stabilizer HelmChart resource %s/%s not found", helmChartNamespace, helmChartName)
		}
		return nil, wrapCmdErr(valuesOut, err)
	}
	var cr k8sHelmChartGetJSON
	if err := json.Unmarshal(valuesOut, &cr); err != nil {
		return nil, fmt.Errorf("parsing %s/%s HelmChart resource: %w", helmChartNamespace, helmChartName, err)
	}
	if cr.Spec.Values == nil {
		cr.Spec.Values = map[string]any{}
	}

	// effectiveValues: spec.valuesContent as the base, spec.values merged
	// over it - ports effectiveValues/mergeInto (internal/handler/upgrade.go)
	// exactly, so a value only ever carried in valuesContent isn't invisible
	// to either the settings report or the version-upgrade image-pin check.
	effective := map[string]any{}
	if cr.Spec.ValuesContent != "" {
		var base map[string]any
		if err := yaml.Unmarshal([]byte(cr.Spec.ValuesContent), &base); err != nil {
			return nil, fmt.Errorf("parsing %s/%s HelmChart resource's spec.valuesContent as YAML: %w", helmChartNamespace, helmChartName, err)
		}
		mergeInto(effective, base)
	}
	mergeInto(effective, cr.Spec.Values)

	jobActive := false
	jobOut, jobErr := exec("-n", helmChartNamespace, "get", "job", "helm-install-"+helmChartName, "-o", "json")
	switch {
	case jobErr == nil:
		var job k8sJobGetJSON
		if err := json.Unmarshal(jobOut, &job); err == nil {
			jobActive = job.Status.Active > 0
		}
	case isNotFoundOutput(jobOut):
		// No install/upgrade job exists right now - the normal idle state.
	default:
		return nil, wrapCmdErr(jobOut, jobErr)
	}

	aileronPresent := false
	for _, d := range stabilizerSettingDefs {
		if d.Component != "aileron" || d.Key == "aileron_ui_enabled" {
			continue
		}
		if _, ok := appliedEnv[d.Env]; ok {
			aileronPresent = true
			break
		}
	}

	return &stabilizerSettingsState{
		appliedEnv:           appliedEnv,
		declaredValues:       effective,
		helmChartNamespace:   helmChartNamespace,
		helmChartName:        helmChartName,
		declaredVersion:      cr.Spec.Version,
		declaredChart:        cr.Spec.Chart,
		hasLocalChartContent: cr.Spec.ChartContent != "",
		selfUpgradeEnabled:   appliedEnv["SELF_UPGRADE_ENABLED"] == "true",
		allowedChart:         appliedEnv["SELF_UPGRADE_ALLOWED_CHART"],
		allowDowngrade:       appliedEnv["SELF_UPGRADE_ALLOW_DOWNGRADE"] == "true",
		appliedChartVersion:  appliedEnv["CHART_VERSION"],
		jobActive:            jobActive,
		aileronDisabled:      !aileronPresent,
	}, nil
}

// printStabilizerSettingsReport renders every setting's applied/declared
// state as a table. Every row in stabilizerSettingDefs gets a row here -
// this is what "drive the whole tool from the manifest" means in practice.
func printStabilizerSettingsReport(w io.Writer, state *stabilizerSettingsState) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tAPPLIED\tSTATUS\tSUMMARY")
	for _, d := range stabilizerSettingDefs {
		if d.Component == "aileron" && d.Key != "aileron_ui_enabled" && state.aileronDisabled {
			fmt.Fprintf(tw, "%s\t-\taileron subchart disabled - nothing to report\t%s\n", d.Key, d.Summary)
			continue
		}

		appliedRaw, present := state.appliedEnv[d.Env]
		if !present {
			fmt.Fprintf(tw, "%s\t?\tno data - this stabilizer may be newer than ruddervirt-setup's vendored settings list\t%s\n", d.Key, d.Summary)
			continue
		}
		applied, aerr := parseStabilizerSettingValue(d, appliedRaw)
		if aerr != nil {
			fmt.Fprintf(tw, "%s\t%s\tunparseable applied value\t%s\n", d.Key, appliedRaw, d.Summary)
			continue
		}

		status := "settled (chart default)"
		if declRaw, ok := getByPath(state.declaredValues, d.Path); ok {
			declared, cok := coerceJSONValue(d, declRaw)
			switch {
			case !cok:
				status = "declared value doesn't parse - check the HelmChart resource"
			case stabilizerSettingValuesEqual(d, applied, declared):
				status = "settled"
			default:
				status = fmt.Sprintf("rollout pending -> %s", formatStabilizerSettingValue(d, declared))
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", d.Key, formatStabilizerSettingValue(d, applied), status, d.Summary)
	}
	tw.Flush()

	if state.jobActive {
		fmt.Fprintf(w, "\nNote: a stabilizer release operation is in progress (helm-install-%s job is active) - watch it with:\n  kubectl -n %s get job helm-install-%s -w\n",
			state.helmChartName, state.helmChartNamespace, state.helmChartName)
	}

	if unknown := unknownAppliedSettingEnvVars(state.appliedEnv); len(unknown) > 0 {
		fmt.Fprintf(w, "\nNote: the running stabilizer reports settings this ruddervirt-setup build doesn't know about (%s) - it's likely newer than this tool's vendored settings list. Update ruddervirt-setup to see/change them.\n",
			strings.Join(unknown, ", "))
	}
}

// unknownAppliedSettingEnvVars returns every SETTING_*-prefixed env var name
// in appliedEnv that no entry in stabilizerSettingDefs declares as its Env -
// i.e. the installed stabilizer supports a setting this vendored manifest
// predates. Sorted for stable output.
func unknownAppliedSettingEnvVars(appliedEnv map[string]string) []string {
	known := make(map[string]bool, len(stabilizerSettingDefs))
	for _, d := range stabilizerSettingDefs {
		known[d.Env] = true
	}
	var unknown []string
	for name := range appliedEnv {
		if strings.HasPrefix(name, "SETTING_") && !known[name] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// stabilizerSettingChange is one validated --set request, paired with its
// effective current value (declared if present, else applied - see
// applyStabilizerSettingsChanges) for the skip-redundant-write check and
// the confirmation screen's "before -> after" display.
type stabilizerSettingChange struct {
	def     stabilizerSettingDef
	value   any
	current any // nil if genuinely unknown (couldn't be read/parsed)
}

// effectiveCurrentValue returns what a change request should be compared
// against: the value already DECLARED on the HelmChart resource if one is
// present (even if its rollout hasn't landed yet), else the currently
// APPLIED value. This mirrors stabilizer's own resolveSettings (internal/
// handler/upgrade.go), which compares a request against exactly this
// "effective intent" - so a second identical --set while a rollout is still
// landing is correctly treated as redundant too, not just a steady-state
// repeat.
func effectiveCurrentValue(state *stabilizerSettingsState, d stabilizerSettingDef) any {
	if declRaw, ok := getByPath(state.declaredValues, d.Path); ok {
		if c, ok := coerceJSONValue(d, declRaw); ok {
			return c
		}
	}
	if appliedRaw, ok := state.appliedEnv[d.Env]; ok {
		if a, err := parseStabilizerSettingValue(d, appliedRaw); err == nil {
			return a
		}
	}
	return nil
}

// applyStabilizerSettingsChanges validates every --set request LOCALLY
// (nothing downstream checks this - see this file's header comment),
// skips any that would be a no-op, refuses outright if a release operation
// is already in flight, explains the restart-the-whole-release impact,
// confirms, and finally writes one JSON merge patch covering every changed
// leaf under spec.values.
func applyStabilizerSettingsChanges(state *stabilizerSettingsState, setArgs []string, skipConfirm bool) int {
	var changes []stabilizerSettingChange
	var errs []string

	for _, kv := range setArgs {
		key, raw, ok := strings.Cut(kv, "=")
		if !ok {
			errs = append(errs, fmt.Sprintf("invalid --set %q: expected key=value", kv))
			continue
		}
		def, ok := stabilizerSettingByKey(key)
		if !ok {
			if strings.Contains(strings.ToLower(key), "version") {
				// Chart version changes are deliberately out of scope here -
				// they carry guards (semver validation, downgrade policy,
				// cross-major refusal, stale image-pin detection) this tool
				// must not duplicate or bypass. Point at install.sh instead
				// of just saying "unknown setting".
				errs = append(errs, fmt.Sprintf(
					"%s: chart version changes aren't done here - use install.sh on the box, e.g.:\n"+
						"  ./install.sh --version <v> --zone <zone> --namespace %s --chart-namespace %s",
					key, stabilizerNamespace, state.helmChartNamespace))
				continue
			}
			errs = append(errs, fmt.Sprintf("unknown setting %q", key))
			continue
		}
		if def.Component == "aileron" && def.Key != "aileron_ui_enabled" && state.aileronDisabled {
			errs = append(errs, fmt.Sprintf("%s: the aileron subchart is disabled - enable it before changing build/grading limits", key))
			continue
		}
		value, verr := parseStabilizerSettingValue(def, raw)
		if verr != nil {
			errs = append(errs, verr.Error())
			continue
		}
		changes = append(changes, stabilizerSettingChange{
			def:     def,
			value:   value,
			current: effectiveCurrentValue(state, def),
		})
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "Error:", e)
		}
		return 1
	}

	var toApply []stabilizerSettingChange
	for _, c := range changes {
		if c.current != nil && stabilizerSettingValuesEqual(c.def, c.value, c.current) {
			fmt.Printf("%s is already %s - skipping (no rollout)\n", c.def.Key, formatStabilizerSettingValue(c.def, c.value))
			continue
		}
		toApply = append(toApply, c)
	}
	if len(toApply) == 0 {
		fmt.Println("Nothing to change.")
		return 0
	}

	// Refused here, after the skip-redundant check above - a request that
	// would be a no-op anyway is fine to "apply" even mid-rollout, since it
	// writes nothing. Anything that would actually change spec.values is
	// refused while a release operation is already running, so the two
	// writers (this tool and the cloud UI) never collide.
	if state.jobActive {
		fmt.Fprintf(os.Stderr, "Error: a stabilizer release operation is already in progress (helm-install-%s job is active) - refusing to start another. Wait for it to finish and try again.\n", state.helmChartName)
		return 1
	}

	fmt.Println("This will change:")
	for _, c := range toApply {
		curDisplay := "(not set - chart default)"
		if c.current != nil {
			curDisplay = formatStabilizerSettingValue(c.def, c.current)
		}
		fmt.Printf("  %-32s %s -> %s\n", c.def.Key, curDisplay, formatStabilizerSettingValue(c.def, c.value))
	}
	fmt.Println()
	fmt.Println("Applying this RESTARTS THE WHOLE RELEASE: stabilizer, vncauthproxy, the")
	fmt.Println("aileron operator, and the VNC gateway all restart (roughly 30-90 seconds).")
	fmt.Println("Consoles drop and the zone goes quiet to the cloud UI during that window.")
	fmt.Println("Running VMs are NOT affected. This is not a hot-reload.")
	fmt.Println()

	if !skipConfirm {
		fmt.Print(`Type "yes" to proceed, or anything else to abort: `)
		reply, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(reply), "yes") {
			fmt.Println("Aborted; nothing was changed.")
			return 1
		}
	}

	patch := map[string]any{}
	for _, c := range toApply {
		setByPath(patch, c.def.Path, c.value)
	}
	patchJSON, err := json.Marshal(map[string]any{"spec": map[string]any{"values": patch}})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error building patch:", err)
		return 1
	}

	out, err := settingsKubectl("-n", state.helmChartNamespace, "patch", "helmchart.helm.cattle.io", state.helmChartName,
		"--type=merge", "-p", string(patchJSON))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", friendlyKubectlError(wrapCmdErr(out, err)))
		return 1
	}

	fmt.Println("Applied. Watch the rollout with:")
	fmt.Printf("  kubectl -n %s get job helm-install-%s -w\n", state.helmChartNamespace, state.helmChartName)
	fmt.Println(`Re-run "ruddervirt-setup settings" in about a minute to confirm the applied state caught up.`)
	return 0
}
