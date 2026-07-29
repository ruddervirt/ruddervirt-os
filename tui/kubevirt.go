package main

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"
)

const (
	defaultKubeVirtVersion = "v1.8.2"
	defaultCDIVersion      = "v1.64.0"
)

//go:embed supported-versions.yaml
var supportedVersionsYAML []byte

// supportedVersions is parsed once at startup from the embedded
// supported-versions.yaml - see that file for why this is curated by hand
// rather than fetched live like k3s's version list.
type supportedVersionsFile struct {
	KubeVirt []string `yaml:"kubevirt"`
	CDI      []string `yaml:"cdi"`
}

var supportedVersions supportedVersionsFile

func init() {
	// This file is compiled in, not user input or a network fetch - a
	// parse failure here means the embedded data itself is broken, which
	// is a build-time bug that should fail loudly rather than silently
	// leave the version pickers empty.
	if err := yaml.Unmarshal(supportedVersionsYAML, &supportedVersions); err != nil {
		panic(fmt.Sprintf("tui/supported-versions.yaml failed to parse: %v", err))
	}
}

var semverPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

type parsedSemver struct {
	major, minor, patch int
}

// parseSemver accepts only a strict vMAJOR.MINOR.PATCH tag - anything with
// a -rc/-alpha/-beta suffix, a +build suffix, or any other shape simply
// fails to parse. KubeVirt/CDI's supported-versions list is hand-curated
// (see supported-versions.yaml) so it should never contain anything but
// clean release tags in the first place; this is a separate, simpler
// comparator from k3s.go's parseK3sVersion/compareK3sVersions, which has
// to handle k3s's `+k3sBUILD` suffix and treat -rcN as valid-but-lesser.
func parseSemver(v string) (parsedSemver, bool) {
	m := semverPattern.FindStringSubmatch(v)
	if m == nil {
		return parsedSemver{}, false
	}
	p := parsedSemver{}
	p.major, _ = strconv.Atoi(m[1])
	p.minor, _ = strconv.Atoi(m[2])
	p.patch, _ = strconv.Atoi(m[3])
	return p, true
}

// compareSemver returns >0 if a > b, 0 if equal, <0 if a < b (ok is false
// if either doesn't match the strict vMAJOR.MINOR.PATCH shape).
func compareSemver(a, b string) (int, bool) {
	pa, ok := parseSemver(a)
	if !ok {
		return 0, false
	}
	pb, ok := parseSemver(b)
	if !ok {
		return 0, false
	}
	if pa.major != pb.major {
		return pa.major - pb.major, true
	}
	if pa.minor != pb.minor {
		return pa.minor - pb.minor, true
	}
	return pa.patch - pb.patch, true
}

// supportedVersionsAtLeast returns the entries of all that are >= floor,
// sorted newest-first - the shared filter+sort behind the KubeVirt/CDI
// version settingFields' options funcs, so a downgrade is never offered
// (same reasoning as k3s's version picker: the floor is the currently
// configured value, not a live installed-version check, since a live check
// would silently no-op before the very first install).
func supportedVersionsAtLeast(all []string, floor string) []string {
	var available []string
	for _, v := range all {
		cmp, ok := compareSemver(v, floor)
		if ok && cmp < 0 {
			continue
		}
		available = append(available, v)
	}
	sort.SliceStable(available, func(i, j int) bool {
		cmp, ok := compareSemver(available[i], available[j])
		return ok && cmp > 0
	})
	return available
}

// kubevirtManifestMarkerPath/cdiManifestMarkerPath record which
// KubeVirt/CDI versions were last successfully downloaded to
// /etc/ruddervirt/manifests/ - mirrors storage.go's storageMarkerPath
// pattern, one marker per independently-versioned artifact, so
// downloadKubeVirtCDIManifestsStep can skip a redundant re-download once
// the configured versions already match what's on disk.
const (
	kubevirtManifestMarkerPath = "/var/lib/ruddervirt/kubevirt-manifest.applied"
	cdiManifestMarkerPath      = "/var/lib/ruddervirt/cdi-manifest.applied"
)

// kubevirtCDIManifestFiles lists every file downloadKubeVirtCDIManifestsStep
// writes into manifestDir - shared with the skip-check so an operator
// manually deleting one out from under the marker always forces a
// re-download instead of trusting a stale marker.
var kubevirtCDIManifestFiles = []string{
	"kubevirt-operator.yaml",
	"kubevirt-cr.yaml",
	"cdi-operator.yaml",
	"cdi-cr.yaml",
}

func appliedKubeVirtManifestVersion() (string, error) {
	data, err := os.ReadFile(kubevirtManifestMarkerPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func appliedCDIManifestVersion() (string, error) {
	data, err := os.ReadFile(cdiManifestMarkerPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func markKubeVirtManifestApplied(version string) error {
	return writePrivileged(kubevirtManifestMarkerPath, []byte(version))
}

func markCDIManifestApplied(version string) error {
	return writePrivileged(cdiManifestMarkerPath, []byte(version))
}

// kubevirtCDIManifestsPresent reports whether every file in
// kubevirtCDIManifestFiles still exists under manifestDir.
func kubevirtCDIManifestsPresent(manifestDir string) bool {
	for _, name := range kubevirtCDIManifestFiles {
		if _, err := os.Stat(manifestDir + name); err != nil {
			return false
		}
	}
	return true
}

// resolveKubeVirtVersion/resolveCDIVersion return cfg's configured
// versions, or the defaults if unset - shared by
// downloadKubeVirtCDIManifestsStep and planKubeVirtCDIDownload so the two
// can never disagree on what "desired" means.
func resolveKubeVirtVersion(cfg Config) string {
	v := strings.TrimSpace(cfg.Versions.KubeVirt)
	if v == "" {
		return defaultKubeVirtVersion
	}
	return v
}

func resolveCDIVersion(cfg Config) string {
	v := strings.TrimSpace(cfg.Versions.CDI)
	if v == "" {
		return defaultCDIVersion
	}
	return v
}

// kubevirtCDIManifestsSatisfied is the single check shared by
// downloadKubeVirtCDIManifestsStep's skip-logic and
// planKubeVirtCDIDownload: the applied-version markers must match the
// desired versions, and the manifest files themselves must still be
// present (in case they were deleted out-of-band after the marker was
// written).
func kubevirtCDIManifestsSatisfied(manifestDir, kubevirtVersion, cdiVersion string) bool {
	kv, err := appliedKubeVirtManifestVersion()
	if err != nil || kv != kubevirtVersion {
		return false
	}
	cdi, err := appliedCDIManifestVersion()
	if err != nil || cdi != cdiVersion {
		return false
	}
	return kubevirtCDIManifestsPresent(manifestDir)
}

// planKubeVirtCDIDownload previews downloadKubeVirtCDIManifestsStep.
func planKubeVirtCDIDownload(cfg Config) string {
	const manifestDir = "/etc/ruddervirt/manifests/"
	kubevirtVersion := resolveKubeVirtVersion(cfg)
	cdiVersion := resolveCDIVersion(cfg)
	if kubevirtCDIManifestsSatisfied(manifestDir, kubevirtVersion, cdiVersion) {
		return fmt.Sprintf("skip - KubeVirt %s / CDI %s manifests already present", kubevirtVersion, cdiVersion)
	}
	return fmt.Sprintf("will download KubeVirt %s / CDI %s manifests", kubevirtVersion, cdiVersion)
}

// downloadKubeVirtCDIManifestsStep re-downloads the real upstream
// KubeVirt/CDI manifests for the versions configured in Settings,
// overwriting whatever is already under /etc/ruddervirt/manifests/, unless
// kubevirtCDIManifestsSatisfied reports the desired versions are already
// present - see installK3sStep for the equivalent check on the k3s
// binary. Placed right after "Installing k3s" in installSteps since both
// are pure "fetch the right version of X from the internet" steps that
// need no running cluster.
func downloadKubeVirtCDIManifestsStep(cfg Config, ch chan<- tea.Msg) {
	const label = "Downloading KubeVirt/CDI manifests"
	const manifestDir = "/etc/ruddervirt/manifests/"

	kubevirtVersion := resolveKubeVirtVersion(cfg)
	cdiVersion := resolveCDIVersion(cfg)

	if kubevirtCDIManifestsSatisfied(manifestDir, kubevirtVersion, cdiVersion) {
		ch <- stepOutputMsg(fmt.Sprintf("KubeVirt %s / CDI %s manifests already present", kubevirtVersion, cdiVersion))
		ch <- stepDoneMsg{label: label}
		return
	}

	downloads := []struct{ url, destName string }{
		{
			fmt.Sprintf("https://github.com/kubevirt/kubevirt/releases/download/%s/kubevirt-operator.yaml", kubevirtVersion),
			"kubevirt-operator.yaml",
		},
		{
			fmt.Sprintf("https://github.com/kubevirt/kubevirt/releases/download/%s/kubevirt-cr.yaml", kubevirtVersion),
			"kubevirt-cr.yaml",
		},
		{
			fmt.Sprintf("https://github.com/kubevirt/containerized-data-importer/releases/download/%s/cdi-operator.yaml", cdiVersion),
			"cdi-operator.yaml",
		},
		{
			fmt.Sprintf("https://github.com/kubevirt/containerized-data-importer/releases/download/%s/cdi-cr.yaml", cdiVersion),
			"cdi-cr.yaml",
		},
	}

	for _, d := range downloads {
		ch <- stepOutputMsg(fmt.Sprintf("Downloading %s...", d.destName))
		if err := downloadToPrivilegedPath(d.url, manifestDir+d.destName, 0644); err != nil {
			ch <- stepDoneMsg{label: label, err: err}
			return
		}
	}

	// Only record success once every file above actually landed - a
	// mid-loop failure must never falsely mark a partial download as
	// "applied".
	if err := markKubeVirtManifestApplied(kubevirtVersion); err != nil {
		ch <- stepDoneMsg{label: label, err: err}
		return
	}
	if err := markCDIManifestApplied(cdiVersion); err != nil {
		ch <- stepDoneMsg{label: label, err: err}
		return
	}

	ch <- stepOutputMsg("KubeVirt/CDI manifests downloaded successfully")
	ch <- stepDoneMsg{label: label}
}
