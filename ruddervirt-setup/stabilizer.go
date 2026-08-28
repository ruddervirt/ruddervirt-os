// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"
)

const (
	// stabilizerNamespace is stabilizer-helmchart.yaml's targetNamespace -
	// same namespace applyAileron (aileron.go) installs Aileron into.
	stabilizerNamespace = "ruddervirt-system"
	// stabilizerHelmReleaseName matches stabilizerHelmChartName (aileron.go)
	// - the HelmChart CR applyStabilizer below creates is named this, which
	// is also the release name adoptAileronStep re-stamps ownership to.
	stabilizerHelmReleaseName = "stabilizer"
	// standaloneAileronReleaseName is the release adoptAileronStep looks
	// for/retires - exactly the release name ruddervirt-setup's own
	// applyAileron (aileron.go) creates on every fresh install.
	standaloneAileronReleaseName = "aileron"
	natsAuthSecretName           = "stabilizer-nats-auth"
	nebulaSecretName             = "stabilizer-nebula"

	// defaultStabilizerNatsURL is the shared NATS bus every zone connects
	// to - fixed, not operator-editable: ruddervirt provides this value for
	// every adoption, so there's nothing for the wizard to ask about.
	defaultStabilizerNatsURL = "nats://172.16.100.40:4222,nats://172.16.100.41:4222,nats://172.16.100.42:4222"
)

// applyStabilizer renders manifests/stabilizer-helmchart.yaml's
// placeholders and applies the resulting HelmChart object - same
// read-template/substitute/write-tempfile/apply/poll-job shape as
// applyAileron (aileron.go). k3s's own always-on helm-controller then does
// the actual chart install as a Job inside the cluster; no helm binary is
// ever invoked here.
func applyStabilizer(ch chan<- tea.Msg, kubectlBin, version, zone, natsURL string) error {
	const templatePath = "/etc/ruddervirt/manifests/stabilizer-helmchart.yaml"
	const jobNamespace = "kube-system"
	const jobName = "job/helm-install-stabilizer"

	chartVersion := strings.TrimPrefix(version, "v")

	if err := writeManifestFile(ch, "stabilizer-helmchart.yaml"); err != nil {
		return err
	}
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return err
	}
	rendered := strings.ReplaceAll(string(data), "__STABILIZER_VERSION__", chartVersion)
	rendered = strings.ReplaceAll(rendered, "__STABILIZER_ZONE__", zone)
	rendered = strings.ReplaceAll(rendered, "__NATS_URL__", natsURL)

	tmp, err := os.CreateTemp("", "stabilizer-helmchart-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(rendered); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := runStreamed(ch, kubectlBin, "apply", "-f", tmpPath); err != nil {
		return err
	}

	if err := pollUntil(ch, "Waiting for the stabilizer helm-install job", 60, 5*time.Second, func() bool {
		return runPrivileged(kubectlBin, "-n", jobNamespace, "get", jobName).Run() == nil
	}); err != nil {
		return err
	}
	ch <- stepOutputMsg("Waiting for the stabilizer helm-install job to complete...")
	return runStreamed(ch, kubectlBin, "-n", jobNamespace, "wait", "--for=condition=complete", jobName, "--timeout=600s")
}

// standaloneAileronReleasePresent reports whether a live Helm release named
// standaloneAileronReleaseName exists in stabilizerNamespace - used for the
// confirm screen's plan summary. Explicitly distinct from
// stabilizerChartPresent (aileron.go): that checks for a "stabilizer"
// HelmChart CR; this checks for an "aileron" Helm release Secret - the
// thing adoptAileronStep (adopt.go) actually adopts.
func standaloneAileronReleasePresent(kubectlBin string) bool {
	_, ok, err := findDeployedHelmReleaseSecret(kubectlBin, stabilizerNamespace, standaloneAileronReleaseName)
	return err == nil && ok
}

// nebulaFetchTimeout bounds resolveNebulaConfig's URL branch - long enough
// for a slow link to a private CA host, short enough that a typo'd/
// unreachable URL doesn't hang the TUI.
const nebulaFetchTimeout = 15 * time.Second

// resolveNebulaConfig accepts either a local filesystem path or an
// http(s):// URL and returns its raw content - a local read or an HTTP GET
// with a bounded timeout. A non-2xx status or any network/read error is a
// hard validation failure, never a silent empty/garbage secret - the
// Nebula mesh identity must be minted externally on a separate CA and
// can't be generated here, so a failure to fetch it must stop the flow
// rather than proceed with nothing.
func resolveNebulaConfig(pathOrURL string) (string, error) {
	pathOrURL = strings.TrimSpace(pathOrURL)
	if strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://") {
		client := &http.Client{Timeout: nebulaFetchTimeout}
		resp, err := client.Get(pathOrURL)
		if err != nil {
			return "", fmt.Errorf("fetching %s: %w", pathOrURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("fetching %s: unexpected status %s", pathOrURL, resp.Status)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("reading response from %s: %w", pathOrURL, err)
		}
		return string(body), nil
	}
	data, err := os.ReadFile(pathOrURL)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", pathOrURL, err)
	}
	return string(data), nil
}

// nebulaPKI is the shape validateNebulaConfig checks a plain Nebula config
// for - the "ca"/"cert"/"key" leaves under a top-level "pki:" map, per the
// stabilizer-nebula Secret's documented shape (a single self-contained
// Nebula config with inline PEM material).
type nebulaPKI struct {
	CA   string `yaml:"ca"`
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

// k8sSecretManifestShape is the minimal shape extractNebulaConfig checks
// content against to decide whether it's a whole Kubernetes Secret manifest
// (what ruddervirt actually hands out, per its own provisioning process)
// rather than a bare Nebula config file.
type k8sSecretManifestShape struct {
	Kind       string            `yaml:"kind"`
	Data       map[string]string `yaml:"data"`
	StringData map[string]string `yaml:"stringData"`
}

// extractNebulaConfig accepts either shape an operator might be handed and
// returns the plain Nebula config text (the thing with a top-level "pki:"
// key) either way:
//
//   - A whole `kind: Secret` manifest - what ruddervirt's own provisioning
//     actually produces (e.g. `kubectl create secret generic
//     stabilizer-nebula --from-file=config.yml=<tenant>.yml -o yaml
//     --dry-run=client`): the real config lives base64-encoded under
//     data["config.yml"] (or plain under stringData["config.yml"]), not at
//     the document's top level - validating the outer Secret envelope for a
//     top-level "pki:" key would always fail, since that key is one layer
//     down and, for the data form, still base64-encoded.
//   - A bare Nebula config (a plain "pki: ..." document) - passed through
//     unchanged, for anything provisioned a simpler way.
//
// If content parses as a Secret but has no "config.yml" entry, falls back
// to whichever single data/stringData entry exists (some other key name),
// and only errors if that's still ambiguous or entirely absent.
func extractNebulaConfig(content string) (string, error) {
	var secret k8sSecretManifestShape
	if err := yaml.Unmarshal([]byte(content), &secret); err != nil || secret.Kind != "Secret" {
		// Not a Secret manifest (or not even valid YAML) - treat as a bare
		// Nebula config; validateNebulaConfig below is what actually
		// rejects genuinely malformed content.
		return content, nil
	}

	if raw, ok := secret.StringData["config.yml"]; ok {
		return raw, nil
	}
	if b64, ok := secret.Data["config.yml"]; ok {
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return "", fmt.Errorf("Secret's data[\"config.yml\"] isn't valid base64: %w", err)
		}
		return string(decoded), nil
	}

	// No "config.yml" key by that exact name - fall back to a single
	// unambiguous entry under whichever of data/stringData is present.
	switch {
	case len(secret.StringData) == 1:
		for _, v := range secret.StringData {
			return v, nil
		}
	case len(secret.Data) == 1:
		for _, b64 := range secret.Data {
			decoded, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				return "", fmt.Errorf("Secret's data entry isn't valid base64: %w", err)
			}
			return string(decoded), nil
		}
	}
	return "", fmt.Errorf("Secret manifest has no config.yml entry in data/stringData")
}

// validateNebulaConfig parses content (already unwrapped by
// extractNebulaConfig, if it needed to be) as YAML and confirms it has a
// top-level "pki" map containing non-empty "ca", "cert", and "key" values.
// Deliberately shallow - it only confirms the shape looks right, not that
// the PKI material itself is valid PEM; that's stabilizer's own job to fail
// fast on at pod startup.
func validateNebulaConfig(content string) error {
	var parsed struct {
		PKI nebulaPKI `yaml:"pki"`
	}
	if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
		return fmt.Errorf("not valid YAML: %w", err)
	}
	var missing []string
	if parsed.PKI.CA == "" {
		missing = append(missing, "pki.ca")
	}
	if parsed.PKI.Cert == "" {
		missing = append(missing, "pki.cert")
	}
	if parsed.PKI.Key == "" {
		missing = append(missing, "pki.key")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required field(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// nebulaConfigResolvedMsg carries resolveNebulaConfigCmd's result back into
// Update - fetching/reading and validating the Nebula config happens off
// the UI thread since resolveNebulaConfig can block on a network round trip.
type nebulaConfigResolvedMsg struct {
	content string
	err     error
}

// resolveNebulaConfigCmd runs resolveNebulaConfig, unwraps a whole Secret
// manifest down to its plain Nebula config if that's what was fetched (see
// extractNebulaConfig), then validateNebulaConfig on the result - as a
// single tea.Cmd. screenStabilizerNebula fires this on Enter and reacts to
// nebulaConfigResolvedMsg once it lands.
func resolveNebulaConfigCmd(pathOrURL string) tea.Cmd {
	return func() tea.Msg {
		raw, err := resolveNebulaConfig(pathOrURL)
		if err != nil {
			return nebulaConfigResolvedMsg{err: err}
		}
		content, err := extractNebulaConfig(raw)
		if err != nil {
			return nebulaConfigResolvedMsg{err: fmt.Errorf("%s: %w", pathOrURL, err)}
		}
		if err := validateNebulaConfig(content); err != nil {
			return nebulaConfigResolvedMsg{err: fmt.Errorf("%s doesn't look like a valid nebula config: %w", pathOrURL, err)}
		}
		return nebulaConfigResolvedMsg{content: content}
	}
}

// natsAuthSecretManifest/nebulaSecretManifest build the two Secret
// manifests this flow applies, via secretManifestYAML (secrets.go) - thin,
// obviously-correct wrappers so the only place secret *values* actually
// flow through is that one generic path.
func natsAuthSecretManifest(user, password string) string {
	return secretManifestYAML(natsAuthSecretName, stabilizerNamespace, map[string][]byte{
		"user":     []byte(user),
		"password": []byte(password),
	})
}

func nebulaSecretManifest(configYAML string) string {
	return secretManifestYAML(nebulaSecretName, stabilizerNamespace, map[string][]byte{
		"config.yml": []byte(configYAML),
	})
}

// pendingStabilizer* carry the confirmed wizard's secret values into
// stabilizerSteps' run funcs, which (like every installStep) only accept
// (cfg Config, ch chan<- tea.Msg) - set immediately before launchStep is
// called for this flow, mirroring pendingUpdate* (update.go). Unlike
// pendingUpdate* (not secret), these are explicitly zeroed once the
// pipeline finishes, success or failure, so plaintext credential material
// doesn't sit in package globals for the rest of this process's lifetime
// any longer than the run that needed it.
var (
	pendingStabilizerNatsUser     string
	pendingStabilizerNatsPassword string
	pendingStabilizerNebulaConfig string
)

// clearPendingStabilizerSecrets zeroes every pendingStabilizer* var.
func clearPendingStabilizerSecrets() {
	pendingStabilizerNatsUser = ""
	pendingStabilizerNatsPassword = ""
	pendingStabilizerNebulaConfig = ""
}

// stabilizerSteps is the "Adopt to ruddervirt.com" flow's own step
// list - deliberately NOT appended to the global installSteps
// (install_steps.go): this is opt-in, offered from Settings, not part of
// every fresh install/Apply.
var stabilizerSteps = []installStep{
	{
		label: "Creating stabilizer-nats-auth secret",
		run: func(cfg Config, ch chan<- tea.Msg) {
			const label = "Creating stabilizer-nats-auth secret"
			err := applySecretManifest(ch, kubectlBinPath, natsAuthSecretManifest(pendingStabilizerNatsUser, pendingStabilizerNatsPassword))
			ch <- stepDoneMsg{label: label, err: err}
		},
	},
	{
		label: "Creating stabilizer-nebula secret",
		run: func(cfg Config, ch chan<- tea.Msg) {
			const label = "Creating stabilizer-nebula secret"
			err := applySecretManifest(ch, kubectlBinPath, nebulaSecretManifest(pendingStabilizerNebulaConfig))
			ch <- stepDoneMsg{label: label, err: err}
		},
	},
	{
		label: "Adopting standalone aileron release",
		run:   adoptAileronStep,
	},
	{
		label: "Applying stabilizer",
		run: func(cfg Config, ch chan<- tea.Msg) {
			const label = "Applying stabilizer"
			version := strings.TrimSpace(cfg.Stabilizer.Version)
			if version == "" {
				version = defaultStabilizerVersion
			}
			ch <- stepOutputMsg("Applying stabilizer...")
			err := applyStabilizer(ch, kubectlBinPath, version, cfg.Stabilizer.Zone, cfg.Stabilizer.NatsURL)
			ch <- stepDoneMsg{label: label, err: err}
		},
	},
	{
		label: "Waiting for stabilizer to become ready",
		run: func(cfg Config, ch chan<- tea.Msg) {
			const label = "Waiting for stabilizer to become ready"
			err := runStreamed(ch, kubectlBinPath, "-n", stabilizerNamespace, "wait",
				"--for=condition=Available", "deployment.apps/stabilizer", "--timeout=600s")
			ch <- stepDoneMsg{label: label, err: err}
		},
	},
}

// aileronReadyCheckMsg carries checkAileronReadyCmd's result back into
// Update - gates entry into the "Adopt to ruddervirt.com" wizard.
type aileronReadyCheckMsg struct {
	ready bool
}

// checkAileronReadyCmd reports (read-only, off the UI thread) whether
// Aileron is actually installed and running - reuses aileronReady
// (status.go), the same live "deployment.apps/aileron Available" check the
// home screen's own Services row already relies on, so this stays correct
// whether Aileron is currently standalone or already stabilizer-managed.
// Adopting against an Aileron that was never installed, or is only
// partially rolled out/crash-looping, would re-stamp ownership of
// resources that aren't actually healthy - so this blocks the wizard
// before it ever asks for secrets, not just before the final write (see
// adoptAileronStep's own re-check, adopt.go, for the defense-in-depth
// half of this).
//
// k3sServiceActive() is checked FIRST, not just aileronReady() alone -
// before k3s has actually been installed, kubectl (which execs through
// /usr/local/bin/k3s) silently reports success doing nothing at all (see
// k3sServiceActive's own doc comment for exactly why), which would
// otherwise make this check falsely report "ready" on a node k3s was never
// installed on.
func checkAileronReadyCmd(kubectlBin string) tea.Cmd {
	return func() tea.Msg {
		return aileronReadyCheckMsg{ready: k3sServiceActive() && aileronReady(kubectlBin)}
	}
}

// stabilizerPlanMsg carries computeStabilizerPlanCmd's result back into
// Update - feeds screenStabilizerConfirm's plan summary.
type stabilizerPlanMsg struct {
	willAdopt bool
}

// computeStabilizerPlanCmd checks (read-only) whether a standalone aileron
// release is present, off the UI thread, so screenStabilizerPlanning's
// "Computing plan..." message can actually show while it runs.
func computeStabilizerPlanCmd(kubectlBin string) tea.Cmd {
	return func() tea.Msg {
		return stabilizerPlanMsg{willAdopt: standaloneAileronReleasePresent(kubectlBin)}
	}
}

// stabilizerNonEmptyField validates the wizard's plain-text fields (zone,
// NATS password) - zone is a chart-required value with no sane default
// (deploymentZone in the stabilizer chart's values.yaml), so an empty value
// here would only surface much later as a cryptic Helm template-render
// failure inside the install Job.
func stabilizerNonEmptyField(name, val string) (string, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	return val, nil
}
