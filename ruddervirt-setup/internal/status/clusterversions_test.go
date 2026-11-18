// SPDX-License-Identifier: GPL-3.0-only

package status

import "testing"

// TestNormalizeVersionTag: the HelmChart components store their tag in two
// different shapes - kube-ovn/multus get the configured value verbatim
// ("v1.15.32"), while Aileron's is stripped to bare semver because Helm
// chart versions must be ("0.0.55"). Both have to come back comparable with
// a configured version, or a cluster running exactly the configured version
// would read as a difference and trigger a pointless re-apply.
func TestNormalizeVersionTag(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"already prefixed", "v1.15.32", "v1.15.32"},
		{"bare semver gets its v back", "0.0.55", "v0.0.55"},
		{"trailing newline from jsonpath", "v1.9.1\n", "v1.9.1"},
		{"surrounding whitespace", "  1.65.0  ", "v1.65.0"},
		{"empty stays empty - unknown, not a version", "", ""},
		{"whitespace only stays empty", "   \n", ""},
	}
	for _, c := range cases {
		if got := normalizeVersionTag(c.raw); got != c.want {
			t.Errorf("%s: normalizeVersionTag(%q) = %q, want %q", c.name, c.raw, got, c.want)
		}
	}
}

// TestFetchClusterComponentVersionsIsUnreachableWithoutK3s: the ENOEXEC trap
// K3sServiceActive exists for means every kubectl call can falsely exit 0
// before k3s is installed, so the probe must refuse to report Reachable on a
// host with no k3s service rather than publishing five empty versions as
// fact.
func TestFetchClusterComponentVersionsIsUnreachableWithoutK3s(t *testing.T) {
	if K3sServiceActive() {
		t.Skip("k3s is running on this host - the no-k3s branch isn't reachable")
	}
	got := FetchClusterComponentVersions("/usr/local/bin/kubectl")
	if got.Reachable {
		t.Error("FetchClusterComponentVersions().Reachable = true with k3s.service inactive")
	}
	if got != (ClusterComponentVersions{}) {
		t.Errorf("FetchClusterComponentVersions() = %+v, want the zero value", got)
	}
}
