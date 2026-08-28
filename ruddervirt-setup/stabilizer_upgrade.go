// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file ports the version-upgrade guards from stabilizer's own
// internal/handler/upgrade.go (HandleUpgrade/preflight/checkVersionTransition/
// classifyPins/buildUpgradePatch) so that a chart-version change made
// through ruddervirt-setup (stabilizer_version_tui.go) is bound by exactly
// the rules the cloud UI's over-the-wire upgrade RPC enforces - see
// stabilizer_settings_cli.go's header comment on why nothing downstream
// re-checks a write to this HelmChart resource.
//
// Deliberately narrower than the real handler in two ways, both to keep this
// box-side tool conservative rather than reimplementing every knob:
//   - downgrades are never requested from here (allowDowngradeRequest is
//     always false below) - an operator who genuinely needs one coordinates
//     with selfhosted@ruddervirt.com instead of opting in from the box.
//   - a genuine (non-release) image-pin override always refuses the change;
//     there is no clear_image_pins escape hatch in this tool. Only a
//     redundant release pin (the common case: install.sh used to write these
//     on every install) is auto-masked, silently, same as upstream.
//   - settings (spec.values) changes are handled entirely separately by
//     stabilizer_settings_tui.go/stabilizerSettingsApplySteps; this file only
//     ever touches spec.version (plus any masked image pins that version
//     change requires).

// maxStabilizerChartVersionLen/stabilizerChartVersionRE mirror upgrade.go's
// maxVersionLen/semverRE exactly: release CI publishes the chart at
// ${git_tag#v}, so plain X.Y.Z with no leading "v", no prerelease suffix and
// no build metadata is the only valid shape.
const maxStabilizerChartVersionLen = 32

var stabilizerChartVersionRE = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

// validateStabilizerTargetVersion ports validateTargetVersion.
func validateStabilizerTargetVersion(v string) error {
	if len(v) > maxStabilizerChartVersionLen {
		return fmt.Errorf("version is %d characters; chart versions are plain semver and never exceed %d", len(v), maxStabilizerChartVersionLen)
	}
	if stabilizerChartVersionRE.MatchString(v) {
		return nil
	}
	if strings.HasPrefix(v, "v") && stabilizerChartVersionRE.MatchString(strings.TrimPrefix(v, "v")) {
		return fmt.Errorf("chart versions are plain semver - enter %q, not %q", strings.TrimPrefix(v, "v"), v)
	}
	return fmt.Errorf("invalid chart version %q: expected plain semver X.Y.Z with no leading \"v\", no prerelease suffix and no build metadata", v)
}

// stabilizerChartSemver is a parsed plain-semver chart version - distinct
// from kubevirt.go's parsedSemver/parseSemver, which require a "v" prefix
// (k3s/KubeVirt/CDI/Aileron release tags) and are not reusable here: chart
// versions on the HelmChart resource are never "v"-prefixed.
type stabilizerChartSemver [3]int

func parseStabilizerChartVersion(v string) (stabilizerChartSemver, bool) {
	m := stabilizerChartVersionRE.FindStringSubmatch(v)
	if m == nil {
		return stabilizerChartSemver{}, false
	}
	var out stabilizerChartSemver
	for i := range out {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return stabilizerChartSemver{}, false
		}
		out[i] = n
	}
	return out, true
}

func compareStabilizerChartVersion(a, b stabilizerChartSemver) int {
	for i := range a {
		switch {
		case a[i] > b[i]:
			return 1
		case a[i] < b[i]:
			return -1
		}
	}
	return 0
}

// checkStabilizerVersionTransition ports checkVersionTransition: refuses a
// cross-major change unconditionally, and a downgrade unless both the
// request and the cluster's own selfUpgrade.allowDowngrade permit it. An
// unparseable current version (an empty spec.version, or a chart installed
// by some other path) skips both checks - there is nothing principled to
// compare against, and refusing would strand exactly the cluster most in
// need of being moved to a known version.
func checkStabilizerVersionTransition(current, target string, allowDowngradeRequest, allowDowngradeCluster bool) error {
	cur, ok := parseStabilizerChartVersion(current)
	if !ok {
		return nil
	}
	// target was already validated by validateStabilizerTargetVersion, so
	// this can't fail in practice - but treat a parse failure as "nothing to
	// compare" rather than panicking on a zero value.
	tgt, ok := parseStabilizerChartVersion(target)
	if !ok {
		return nil
	}

	if cur[0] != tgt[0] {
		return fmt.Errorf("refusing a cross-major version change (%s -> %s) - this isn't done from here; coordinate with selfhosted@ruddervirt.com", current, target)
	}
	if compareStabilizerChartVersion(tgt, cur) >= 0 {
		return nil
	}
	if !allowDowngradeRequest || !allowDowngradeCluster {
		return fmt.Errorf("refusing a downgrade (%s -> %s) - downgrades aren't offered from here; coordinate with selfhosted@ruddervirt.com if you are sure", current, target)
	}
	return nil
}

// stabilizerPinKind distinguishes a bare image tag value from a fully
// qualified image reference, because "is this a redundant release pin" is
// asked differently of each - ports pinKind.
type stabilizerPinKind int

const (
	stabilizerPinTag stabilizerPinKind = iota
	stabilizerPinImageRef
)

// stabilizerImagePins ports imagePins verbatim - every chart value that
// pins an image, redundant on a released chart (see upstream's own doc
// comment on imagePins for why install.sh used to write them anyway).
var stabilizerImagePins = []struct {
	path []string
	kind stabilizerPinKind
}{
	{[]string{"image", "tag"}, stabilizerPinTag},
	{[]string{"vncAuthProxy", "image", "tag"}, stabilizerPinTag},
	{[]string{"aileron", "image", "tag"}, stabilizerPinTag},
	{[]string{"aileron", "vncGateway", "image", "tag"}, stabilizerPinTag},
	{[]string{"aileron", "vncGateway", "bridgeImage", "tag"}, stabilizerPinTag},
	{[]string{"aileron", "grading", "graderImage"}, stabilizerPinImageRef},
	{[]string{"aileron", "aileronUI", "image", "tag"}, stabilizerPinTag},
}

// stabilizerIsReleasePin reports whether a pinned value is just a
// restatement of the version the chart would have resolved on its own -
// ports isReleasePin.
func stabilizerIsReleasePin(kind stabilizerPinKind, value, currentVersion string) bool {
	if currentVersion == "" {
		return false
	}
	switch kind {
	case stabilizerPinTag:
		return value == currentVersion || value == "v"+currentVersion
	case stabilizerPinImageRef:
		return strings.HasSuffix(value, ":"+currentVersion) || strings.HasSuffix(value, ":v"+currentVersion)
	}
	return false
}

func lookupStabilizerPinPath(m map[string]any, path []string) (any, bool) {
	cur := any(m)
	for _, key := range path {
		asMap, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = asMap[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// classifyStabilizerImagePins ports classifyPins (with clearDevPins always
// false - see this file's header comment): for every image-pinning chart
// value, decides whether it's a redundant release pin (mask it silently) or
// a developer's override (refuse). The distinction matters because masking
// a dev pin silently would move the chart and every other image while
// leaving that one behind - a half-upgrade nobody notices until something
// breaks strangely.
func classifyStabilizerImagePins(effective map[string]any, currentVersion string) ([]string, error) {
	var cleared []string
	for _, pin := range stabilizerImagePins {
		raw, found := lookupStabilizerPinPath(effective, pin.path)
		if !found {
			continue
		}
		value, ok := raw.(string)
		if !ok || value == "" {
			continue
		}
		dotted := strings.Join(pin.path, ".")
		if stabilizerIsReleasePin(pin.kind, value, currentVersion) {
			cleared = append(cleared, dotted)
			continue
		}
		return nil, fmt.Errorf("%s is pinned to %q, which is not this release's tag (%s) - a version bump would move everything else and leave that image behind. Clear it on the box (install.sh), or coordinate with selfhosted@ruddervirt.com",
			dotted, value, currentVersion)
	}
	return cleared, nil
}

// preflightStabilizerVersionUpgrade ports the parts of preflight this
// box-side tool can check without a live NATS round-trip: self-upgrade must
// be enabled on this cluster, no release operation may already be in
// flight, the release must not have been installed from a local
// spec.chartContent, and (when the running agent declares one) the
// release's chart must match its own allowedChart.
func preflightStabilizerVersionUpgrade(state *stabilizerSettingsState) error {
	if !state.selfUpgradeEnabled {
		return fmt.Errorf("self-upgrade is disabled on this cluster (chart value selfUpgrade.enabled) - coordinate with selfhosted@ruddervirt.com")
	}
	if state.jobActive {
		return fmt.Errorf("a stabilizer release operation is already in progress (helm-install-%s job is active) - wait for it to finish and try again", state.helmChartName)
	}
	if state.hasLocalChartContent {
		return fmt.Errorf("this release was installed from a local chart (spec.chartContent) - a version change isn't done from here")
	}
	if state.allowedChart != "" && state.declaredChart != "" && state.declaredChart != state.allowedChart {
		return fmt.Errorf("refusing to modify a release whose chart is %q (expected %q)", state.declaredChart, state.allowedChart)
	}
	return nil
}

// planStabilizerVersionUpgrade validates a requested chart-version change
// end to end (preflight, target-version shape, the version-transition
// guards, image-pin classification) and returns the merge patch to apply -
// mirrors HandleUpgrade up to (not including) the actual cluster write and
// the durable-record-before-patching step, both of which don't apply here:
// the caller performs the write itself via kubectl (stabilizer_version_tui.go),
// same as every other write in this package, and there is no ZoneReporter on
// the box side to record against.
func planStabilizerVersionUpgrade(state *stabilizerSettingsState, targetVersion string) (patch []byte, clearedPins []string, err error) {
	if err := preflightStabilizerVersionUpgrade(state); err != nil {
		return nil, nil, err
	}
	if err := validateStabilizerTargetVersion(targetVersion); err != nil {
		return nil, nil, err
	}
	if err := checkStabilizerVersionTransition(state.declaredVersion, targetVersion, false, state.allowDowngrade); err != nil {
		return nil, nil, err
	}

	cleared, err := classifyStabilizerImagePins(state.declaredValues, state.declaredVersion)
	if err != nil {
		return nil, nil, err
	}
	if state.declaredVersion == targetVersion && len(cleared) == 0 {
		return nil, nil, fmt.Errorf("already at %s with nothing else to change", targetVersion)
	}

	values := map[string]any{}
	for _, dotted := range cleared {
		// Mask with "", never delete - a merge-patch null on spec.values
		// would only uncover the copy sitting in the lower-precedence
		// spec.valuesContent. See buildUpgradePatch's own doc comment
		// (internal/handler/upgrade.go) for the full reasoning.
		setByPath(values, dotted, "")
	}
	spec := map[string]any{"version": targetVersion}
	if len(values) > 0 {
		spec["values"] = values
	}
	patchJSON, err := json.Marshal(map[string]any{"spec": spec})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal version-upgrade patch: %w", err)
	}
	return patchJSON, cleared, nil
}
