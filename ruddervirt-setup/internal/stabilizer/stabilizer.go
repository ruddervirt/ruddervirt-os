// SPDX-License-Identifier: GPL-3.0-only

// Package stabilizer holds the "Adopt to ruddervirt.com" wizard's business
// logic: applying the stabilizer HelmChart, resolving/validating a Nebula
// mesh config, the one-shot Helm-ownership-restamping port of
// adopt-aileron.sh (adopt.go), and the guarded chart-version-upgrade logic
// (upgrade.go). Package main's stabilizer_bridge.go adapts this package's
// tea.Cmd-free wrappers onto AdoptSteps/SetPendingSecrets/etc.
package stabilizer

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/k3s"
	"ruddervirt-setup/internal/manifests"
	"ruddervirt-setup/internal/secrets"
	"ruddervirt-setup/internal/versions"
)

// kubectlBinPath mirrors package main's own copy of this literal
// (status_bridge.go) - duplicated here rather than imported, same
// "each package defines its own kubectlBin constant" convention already
// used by internal/aileron, internal/k3s, internal/status,
// internal/stabilizer/settings.
const kubectlBinPath = "/usr/local/bin/kubectl"

// wrapStepOutput adapts a log line to installsteps.StepOutputMsg - mirrors
// package main's exec_bridge.go wrapStepOutput, kept as a local copy since
// AdoptSteps/applyStabilizer/adopt.go build their own pipeline internally.
func wrapStepOutput(l string) installsteps.StepMsg { return installsteps.StepOutputMsg(l) }

const (
	// StabilizerNamespace is stabilizer-helmchart.yaml's targetNamespace -
	// same namespace applyAileron (internal/aileron) installs Aileron into.
	StabilizerNamespace = "ruddervirt-system"
	// stabilizerHelmReleaseName matches stabilizerHelmChartName
	// (internal/aileron) - the HelmChart CR applyStabilizer below creates is
	// named this, which is also the release name adoptAileronStep
	// re-stamps ownership to.
	stabilizerHelmReleaseName = "stabilizer"
	// standaloneAileronReleaseName is the release adoptAileronStep looks
	// for/retires - exactly the release name ruddervirt-setup's own
	// applyAileron (internal/aileron) creates on every fresh install.
	standaloneAileronReleaseName = "aileron"
	// NatsAuthSecretName/NebulaSecretName are the two Secrets
	// natsAuthSecretManifest/nebulaSecretManifest below build - exported
	// since package main's view.go references them directly in the confirm
	// screen's summary text.
	NatsAuthSecretName = "stabilizer-nats-auth"
	NebulaSecretName   = "stabilizer-nebula"

	// DefaultStabilizerNatsURL is the shared NATS bus every zone connects
	// to - fixed, not operator-editable: ruddervirt provides this value for
	// every adoption, so there's nothing for the wizard to ask about.
	DefaultStabilizerNatsURL = "nats://172.16.100.40:4222,nats://172.16.100.41:4222,nats://172.16.100.42:4222"
)

// applyStabilizer renders manifests/stabilizer-helmchart.yaml's
// placeholders, applies the resulting HelmChart object, and waits for the
// controller's asynchronously-created install Job to complete - via
// internal/manifests.RenderAndApply and internal/k3s.WaitForHelmInstallJob,
// the same shape as internal/aileron's ApplyAileron. k3s's helm-controller
// does the actual chart install as a Job in-cluster; no helm binary is
// invoked here.
func applyStabilizer(ch chan<- installsteps.StepMsg, kubectlBin, version, zone, natsURL string) error {
	const jobNamespace = "kube-system"
	const jobName = "job/helm-install-stabilizer"

	chartVersion := strings.TrimPrefix(version, "v")

	if err := manifests.RenderAndApply(ch, wrapStepOutput, config.WritePrivileged, kubectlBin, "stabilizer-helmchart.yaml", "stabilizer-helmchart", []manifests.Placeholder{
		{Token: "__STABILIZER_VERSION__", Value: chartVersion},
		{Token: "__STABILIZER_ZONE__", Value: zone},
		{Token: "__NATS_URL__", Value: natsURL},
	}); err != nil {
		return err
	}

	return k3s.WaitForHelmInstallJob(ch, wrapStepOutput, kubectlBin, jobNamespace, jobName, "stabilizer")
}

// StandaloneAileronReleasePresent reports whether a live Helm release named
// standaloneAileronReleaseName exists in StabilizerNamespace - used for the
// confirm screen's plan summary. Distinct from aileron.StabilizerChartPresent
// (checks for a "stabilizer" HelmChart CR): this checks for an "aileron"
// Helm release Secret, the thing adoptAileronStep (adopt.go) adopts.
func StandaloneAileronReleasePresent(kubectlBin string) bool {
	_, ok, err := findDeployedHelmReleaseSecret(kubectlBin, StabilizerNamespace, standaloneAileronReleaseName)
	return err == nil && ok
}

// nebulaFetchTimeout bounds ResolveNebulaConfig's URL branch - long enough
// for a slow link to a private CA host, short enough that a typo'd/
// unreachable URL doesn't hang the TUI.
const nebulaFetchTimeout = 15 * time.Second

// ResolveNebulaConfig accepts either a local filesystem path or an
// http(s):// URL and returns its raw content - a local read or a bounded
// HTTP GET. A non-2xx status or any network/read error is a hard
// validation failure, never a silent empty/garbage secret: the Nebula mesh
// identity must be minted externally and can't be generated here, so a
// fetch failure must stop the flow rather than proceed with nothing.
func ResolveNebulaConfig(pathOrURL string) (string, error) {
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

// nebulaPKI is the shape ValidateNebulaConfig checks a plain Nebula config
// for - the "ca"/"cert"/"key" leaves under a top-level "pki:" map, per the
// stabilizer-nebula Secret's documented shape (a single self-contained
// Nebula config with inline PEM material).
type nebulaPKI struct {
	CA   string `yaml:"ca"`
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

// k8sSecretManifestShape is the minimal shape ExtractNebulaConfig checks
// content against to decide whether it's a whole Kubernetes Secret manifest
// (what ruddervirt actually hands out, per its own provisioning process)
// rather than a bare Nebula config file.
type k8sSecretManifestShape struct {
	Kind       string            `yaml:"kind"`
	Data       map[string]string `yaml:"data"`
	StringData map[string]string `yaml:"stringData"`
}

// ExtractNebulaConfig accepts either shape an operator might be handed and
// returns the plain Nebula config text (the thing with a top-level "pki:"
// key) either way:
//
//   - A whole `kind: Secret` manifest - what ruddervirt's own provisioning
//     produces (e.g. `kubectl create secret generic stabilizer-nebula
//     --from-file=config.yml=<tenant>.yml -o yaml --dry-run=client`): the
//     real config lives base64-encoded under data["config.yml"] (or plain
//     under stringData["config.yml"]), one layer below the Secret's own
//     top level.
//   - A bare Nebula config (a plain "pki: ..." document), passed through
//     unchanged.
//
// If it parses as a Secret with no "config.yml" entry, falls back to a
// single unambiguous data/stringData entry, erroring only if that's still
// ambiguous or absent.
func ExtractNebulaConfig(content string) (string, error) {
	var secret k8sSecretManifestShape
	if err := yaml.Unmarshal([]byte(content), &secret); err != nil || secret.Kind != "Secret" {
		// Not a Secret manifest (or not valid YAML) - treat as a bare Nebula
		// config; ValidateNebulaConfig rejects genuinely malformed content.
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

// ValidateNebulaConfig parses content (already unwrapped by
// ExtractNebulaConfig, if needed) as YAML and confirms it has a top-level
// "pki" map with non-empty "ca", "cert", "key" values. Deliberately
// shallow - confirms the shape only, not that the PKI material is valid
// PEM; that's stabilizer's own job at pod startup.
func ValidateNebulaConfig(content string) error {
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

// natsAuthSecretManifest/nebulaSecretManifest build the two Secret
// manifests this flow applies, via secrets.SecretManifestYAML
// (internal/secrets) - thin, obviously-correct wrappers so the only place
// secret *values* actually flow through is that one generic path.
func natsAuthSecretManifest(user, password string) string {
	return secrets.SecretManifestYAML(NatsAuthSecretName, StabilizerNamespace, map[string][]byte{
		"user":     []byte(user),
		"password": []byte(password),
	})
}

func nebulaSecretManifest(configYAML string) string {
	return secrets.SecretManifestYAML(NebulaSecretName, StabilizerNamespace, map[string][]byte{
		"config.yml": []byte(configYAML),
	})
}

// pendingStabilizer* carry the confirmed wizard's secret values into
// AdoptSteps' Run funcs, whose signature (cfg config.Config, ch chan<-
// installsteps.StepMsg) has no room for them. Set via SetPendingSecrets
// right before LaunchStep (package main's app.go), mirroring
// internal/selfupdate's pendingUpdate*. Unlike pendingUpdate* (not secret),
// these are explicitly zeroed once the pipeline finishes, success or
// failure, so plaintext credential material doesn't outlive the run that
// needed it. Deliberately unexported - never promote to shared state or
// thread through config.Config, which would persist them to disk.
var (
	pendingStabilizerNatsUser     string
	pendingStabilizerNatsPassword string
	pendingStabilizerNebulaConfig string
)

// SetPendingSecrets records the wizard-confirmed secret values AdoptSteps'
// Run funcs apply - called once, right before launching AdoptSteps, by
// package main's app.go. Mirrors internal/selfupdate.SetPending's shape.
func SetPendingSecrets(natsUser, natsPassword, nebulaConfig string) {
	pendingStabilizerNatsUser = natsUser
	pendingStabilizerNatsPassword = natsPassword
	pendingStabilizerNebulaConfig = nebulaConfig
}

// ClearPendingSecrets zeroes every pendingStabilizer* var.
func ClearPendingSecrets() {
	pendingStabilizerNatsUser = ""
	pendingStabilizerNatsPassword = ""
	pendingStabilizerNebulaConfig = ""
}

// AdoptSteps is the "Adopt to ruddervirt.com" flow's own step list -
// deliberately not appended to package main's global installSteps: this is
// opt-in, offered from Settings, not part of every fresh install/Apply.
var AdoptSteps = []installsteps.Step{
	{
		Label: "Creating stabilizer-nats-auth secret",
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			const label = "Creating stabilizer-nats-auth secret"
			err := secrets.ApplySecretManifest(ch, wrapStepOutput, kubectlBinPath, natsAuthSecretManifest(pendingStabilizerNatsUser, pendingStabilizerNatsPassword))
			ch <- installsteps.StepDoneMsg{Label: label, Err: err}
		},
	},
	{
		Label: "Creating stabilizer-nebula secret",
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			const label = "Creating stabilizer-nebula secret"
			err := secrets.ApplySecretManifest(ch, wrapStepOutput, kubectlBinPath, nebulaSecretManifest(pendingStabilizerNebulaConfig))
			ch <- installsteps.StepDoneMsg{Label: label, Err: err}
		},
	},
	{
		Label: "Adopting standalone aileron release",
		Run:   adoptAileronStep,
	},
	{
		Label: "Applying stabilizer",
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			const label = "Applying stabilizer"
			version := strings.TrimSpace(cfg.Stabilizer.Version)
			if version == "" {
				version = versions.DefaultStabilizerVersion
			}
			ch <- installsteps.StepOutputMsg("Applying stabilizer...")
			err := applyStabilizer(ch, kubectlBinPath, version, cfg.Stabilizer.Zone, cfg.Stabilizer.NatsURL)
			ch <- installsteps.StepDoneMsg{Label: label, Err: err}
		},
	},
	{
		Label: "Waiting for stabilizer to become ready",
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			const label = "Waiting for stabilizer to become ready"
			err := exec.RunStreamed(ch, wrapStepOutput, kubectlBinPath, "-n", StabilizerNamespace, "wait",
				"--for=condition=Available", "deployment.apps/stabilizer", "--timeout=600s")
			ch <- installsteps.StepDoneMsg{Label: label, Err: err}
		},
	},
}

// NonEmptyField validates the wizard's plain-text fields (zone, NATS
// password) - zone is chart-required with no sane default (deploymentZone
// in the stabilizer chart's values.yaml), so an empty value here would only
// surface later as a cryptic Helm template-render failure.
func NonEmptyField(name, val string) (string, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	return val, nil
}
