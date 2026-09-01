// SPDX-License-Identifier: GPL-3.0-only

// Package versions holds the generic semver-ish comparison infrastructure
// shared well beyond KubeVirt/CDI (originally kubevirt.go): parsing/
// comparing strict vMAJOR.MINOR.PATCH tags, and the hand-curated
// supported-versions.yaml allowlist. KubeVirt/CDI-specific logic (manifest
// download, CRD-presence validation) stays in internal/kubevirt.
package versions

import (
	_ "embed"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed supported-versions.yaml
var supportedVersionsYAML []byte

// SupportedVersions is parsed once at startup from the embedded
// supported-versions.yaml - see that file for why this is curated by hand
// rather than fetched live like k3s's version list.
type supportedVersionsFile struct {
	KubeVirt []string `yaml:"kubevirt"`
	CDI      []string `yaml:"cdi"`
	KubeOVN  []string `yaml:"kubeovn"`
	Multus   []string `yaml:"multus"`
}

var SupportedVersions supportedVersionsFile

func init() {
	// Compiled in, not user input - a parse failure means a build-time
	// bug, so fail loudly rather than leave the version pickers empty.
	if err := yaml.Unmarshal(supportedVersionsYAML, &SupportedVersions); err != nil {
		panic(fmt.Sprintf("ruddervirt-setup/internal/versions/supported-versions.yaml failed to parse: %v", err))
	}
}

var semverPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

type parsedSemver struct {
	major, minor, patch int
}

// ParseSemver accepts a strict vMAJOR.MINOR.PATCH tag, optionally padded
// with leading/trailing whitespace - anything with a -rc/-alpha/-beta
// suffix, a +build suffix, or any other shape fails to parse. This is a
// separate, simpler comparator from internal/k3s's ParseK3sVersion/
// CompareK3sVersions, which has to handle k3s's `+k3sBUILD` suffix and
// treat -rcN as valid-but-lesser (see this package's doc comment).
func ParseSemver(v string) (parsedSemver, bool) {
	m := semverPattern.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return parsedSemver{}, false
	}
	p := parsedSemver{}
	p.major, _ = strconv.Atoi(m[1])
	p.minor, _ = strconv.Atoi(m[2])
	p.patch, _ = strconv.Atoi(m[3])
	return p, true
}

// CompareSemver returns >0 if a > b, 0 if equal, <0 if a < b (ok is false
// if either doesn't match the strict vMAJOR.MINOR.PATCH shape).
func CompareSemver(a, b string) (int, bool) {
	pa, ok := ParseSemver(a)
	if !ok {
		return 0, false
	}
	pb, ok := ParseSemver(b)
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

// SupportedVersionsAtLeast returns the entries of all that are >= floor,
// sorted newest-first, so a downgrade is never offered. floor is the
// currently configured value rather than a live installed-version check,
// since that would silently no-op before the very first install.
func SupportedVersionsAtLeast(all []string, floor string) []string {
	var available []string
	for _, v := range all {
		cmp, ok := CompareSemver(v, floor)
		if ok && cmp < 0 {
			continue
		}
		available = append(available, v)
	}
	sort.SliceStable(available, func(i, j int) bool {
		cmp, ok := CompareSemver(available[i], available[j])
		return ok && cmp > 0
	})
	return available
}
