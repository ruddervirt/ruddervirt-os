// SPDX-License-Identifier: GPL-3.0-only

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	versionspkg "ruddervirt-setup/internal/versions"
)

func TestParseRequiredIP(t *testing.T) {
	cases := []struct {
		name    string
		val     string
		want    string
		wantErr bool
	}{
		{"valid IPv4", "10.0.0.1", "10.0.0.1", false},
		{"empty", "", "", true},
		{"not an IP", "not-an-ip", "", true},
	}
	for _, c := range cases {
		got, err := parseRequiredIP("field", c.val)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: parseRequiredIP(%q) err = %v, wantErr %v", c.name, c.val, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("%s: parseRequiredIP(%q) = %q, want %q", c.name, c.val, got, c.want)
		}
	}
}

func TestParseCIDRField(t *testing.T) {
	cases := []struct {
		name    string
		val     string
		wantErr bool
	}{
		{"valid CIDR", "10.42.0.0/16", false},
		{"missing prefix", "10.42.0.0", true},
		{"garbage", "not-a-cidr", true},
	}
	for _, c := range cases {
		_, err := parseCIDRField("field", c.val)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: parseCIDRField(%q) err = %v, wantErr %v", c.name, c.val, err, c.wantErr)
		}
	}
}

func TestParsePrefixLen(t *testing.T) {
	cases := []struct {
		name    string
		val     string
		want    int
		wantErr bool
	}{
		{"valid", "24", 24, false},
		{"minimum", "1", 1, false},
		{"maximum", "32", 32, false},
		{"zero", "0", 0, true},
		{"too large", "33", 0, true},
		{"not a number", "abc", 0, true},
	}
	for _, c := range cases {
		got, err := parsePrefixLen(c.val)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: parsePrefixLen(%q) err = %v, wantErr %v", c.name, c.val, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("%s: parsePrefixLen(%q) = %d, want %d", c.name, c.val, got, c.want)
		}
	}
}

func TestParseDNSList(t *testing.T) {
	cases := []struct {
		name    string
		val     string
		want    []string
		wantErr bool
	}{
		{"single", "1.1.1.1", []string{"1.1.1.1"}, false},
		{"multiple with spaces", "1.1.1.1, 8.8.8.8", []string{"1.1.1.1", "8.8.8.8"}, false},
		{"empty entries skipped", "1.1.1.1,,8.8.8.8", []string{"1.1.1.1", "8.8.8.8"}, false},
		{"empty string", "", nil, false},
		{"invalid IP", "1.1.1.1,not-an-ip", nil, true},
	}
	for _, c := range cases {
		got, err := parseDNSList(c.val)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: parseDNSList(%q) err = %v, wantErr %v", c.name, c.val, err, c.wantErr)
			continue
		}
		if !c.wantErr && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: parseDNSList(%q) = %v, want %v", c.name, c.val, got, c.want)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Network.Addressing != "dhcp" {
		t.Errorf("DefaultConfig().Network.Addressing = %q, want %q", cfg.Network.Addressing, "dhcp")
	}
	if cfg.Storage.Engine != "openebs" {
		t.Errorf("DefaultConfig().Storage.Engine = %q, want %q", cfg.Storage.Engine, "openebs")
	}
	if cfg.Versions.K3s != versionspkg.DefaultK3sVersion {
		t.Errorf("DefaultConfig().Versions.K3s = %q, want %q", cfg.Versions.K3s, versionspkg.DefaultK3sVersion)
	}
	if cfg.Versions.KubeOVN != versionspkg.DefaultKubeOVNVersion {
		t.Errorf("DefaultConfig().Versions.KubeOVN = %q, want %q", cfg.Versions.KubeOVN, versionspkg.DefaultKubeOVNVersion)
	}
	if cfg.Versions.Multus != versionspkg.DefaultMultusVersion {
		t.Errorf("DefaultConfig().Versions.Multus = %q, want %q", cfg.Versions.Multus, versionspkg.DefaultMultusVersion)
	}
	if !cfg.System.AutoUpdate {
		t.Error("DefaultConfig().System.AutoUpdate = false, want true")
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("missing file returns defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig(%q) err = %v, want nil", path, err)
		}
		if !reflect.DeepEqual(cfg, DefaultConfig()) {
			t.Errorf("LoadConfig(%q) = %+v, want defaults %+v", path, cfg, DefaultConfig())
		}
	})

	t.Run("existing file overlays onto defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ruddervirt-setup.yaml")
		yaml := "network:\n  interface_name: eth0\n  addressing: static\nstorage:\n  engine: longhorn\n"
		if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig(%q) err = %v, want nil", path, err)
		}
		if cfg.Network.InterfaceName != "eth0" || cfg.Network.Addressing != "static" {
			t.Errorf("LoadConfig(%q) network = %+v, want interface eth0/static", path, cfg.Network)
		}
		if cfg.Storage.Engine != "longhorn" {
			t.Errorf("LoadConfig(%q) Storage.Engine = %q, want %q", path, cfg.Storage.Engine, "longhorn")
		}
		// Fields absent from the file fall back to defaults, not zero values -
		// protects existing installs from an empty Versions.KubeOVN/Multus
		// (an invalid chart version) after upgrading to a build that added them.
		if cfg.Versions.K3s != versionspkg.DefaultK3sVersion {
			t.Errorf("LoadConfig(%q) Versions.K3s = %q, want default %q", path, cfg.Versions.K3s, versionspkg.DefaultK3sVersion)
		}
		if cfg.Versions.KubeOVN != versionspkg.DefaultKubeOVNVersion {
			t.Errorf("LoadConfig(%q) Versions.KubeOVN = %q, want default %q", path, cfg.Versions.KubeOVN, versionspkg.DefaultKubeOVNVersion)
		}
		if cfg.Versions.Multus != versionspkg.DefaultMultusVersion {
			t.Errorf("LoadConfig(%q) Versions.Multus = %q, want default %q", path, cfg.Versions.Multus, versionspkg.DefaultMultusVersion)
		}
	})

	t.Run("invalid yaml returns an error and fresh defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.yaml")
		if err := os.WriteFile(path, []byte("not: [valid: yaml"), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		cfg, err := LoadConfig(path)
		if err == nil {
			t.Fatalf("LoadConfig(%q) err = nil, want an error", path)
		}
		if !reflect.DeepEqual(cfg, DefaultConfig()) {
			t.Errorf("LoadConfig(%q) on error = %+v, want defaults %+v", path, cfg, DefaultConfig())
		}
	})
}

// TestClusterScopedFieldsStayEditableOnAJoiningNode: every component applied
// by prepareK3sStep/ApplyKubeOvnStep is one cluster-wide object, so upgrading
// it is legitimate from any control-plane node. What used to make that unsafe
// was each node reading "the current version" out of its own
// ruddervirt-setup.yaml; now that the running version is read back off the
// cluster (VersionCache.ClusterVersions), these fields must NOT lock on a
// joining node. The two per-node fields must stay editable too.
func TestClusterScopedFieldsStayEditableOnAJoiningNode(t *testing.T) {
	editable := map[string]bool{
		"versions.kubeovn":          true,
		"versions.multus":           true,
		"versions.kubevirt":         true,
		"versions.cdi":              true,
		"versions.aileron":          true,
		"system.aileron_ui_enabled": true,
		"versions.k3s":              true,
		"system.auto_update":        true,
	}

	joining := VersionCache{ClusterJoinTarget: "https://10.0.0.5:6443"}
	cfg := DefaultConfig()

	seen := map[string]bool{}
	for _, f := range SettingFields {
		if !editable[f.Key] {
			continue
		}
		seen[f.Key] = true
		if f.Locked == nil {
			continue
		}
		if locked, reason := f.Locked(&cfg, joining); locked {
			t.Errorf("%s: Locked() = true (%q) on a joining node, want it editable - a cluster-wide component can be upgraded from any control-plane node", f.Key, reason)
		}
	}
	for key := range editable {
		if !seen[key] {
			t.Errorf("%s is no longer a SettingField - this test's list is stale", key)
		}
	}
}

// TestClusterScopedVersionFieldsMatchSettingFields guards the hand-written
// list against a new cluster-scoped version field being added without being
// reconciled against the cluster - the exact omission that would reintroduce
// the stale-config downgrade.
func TestClusterScopedVersionFieldsMatchSettingFields(t *testing.T) {
	byKey := map[string]SettingField{}
	for _, f := range SettingFields {
		byKey[f.Key] = f
	}
	for _, key := range ClusterScopedVersionFields {
		f, ok := byKey[key]
		if !ok {
			t.Errorf("ClusterScopedVersionFields names %q, which is not a SettingField", key)
			continue
		}
		if f.Get == nil || f.Set == nil {
			t.Errorf("%s: needs both Get and Set for ReconcileClusterVersions to work", key)
		}
	}
	if len(ClusterScopedVersionFields) != 5 {
		t.Errorf("ClusterScopedVersionFields has %d entries, want 5 (kubeovn, multus, kubevirt, cdi, aileron)", len(ClusterScopedVersionFields))
	}
}

// TestEffectiveVersionPrefersTheNewerOfConfigAndCluster: the Update screen's
// value column and its upgrade arrow both read through this, so it has to
// resolve both directions - a stale node config loses to the cluster, and an
// operator's freshly picked (not yet applied) version wins over it.
func TestEffectiveVersionPrefersTheNewerOfConfigAndCluster(t *testing.T) {
	var kubevirt SettingField
	for _, f := range SettingFields {
		if f.Key == "versions.kubevirt" {
			kubevirt = f
		}
	}
	if kubevirt.Get == nil {
		t.Fatal("versions.kubevirt is no longer a SettingField")
	}

	cases := []struct {
		name       string
		configured string
		cluster    string
		want       string
	}{
		{"cluster ahead of a stale node config", "v1.9.0", "v1.9.1", "v1.9.1"},
		{"operator picked an upgrade not yet applied", "v1.10.0", "v1.9.1", "v1.10.0"},
		{"agreement", "v1.9.1", "v1.9.1", "v1.9.1"},
		{"cluster unknown falls back to config", "v1.9.1", "", "v1.9.1"},
		{"unparseable cluster value never wins", "v1.9.1", "vnot-semver", "v1.9.1"},
	}
	for _, c := range cases {
		cfg := DefaultConfig()
		cfg.Versions.KubeVirt = c.configured
		versions := VersionCache{ClusterVersions: map[string]string{"versions.kubevirt": c.cluster}}
		if got := EffectiveVersion(kubevirt, &cfg, versions); got != c.want {
			t.Errorf("%s: EffectiveVersion(configured=%q, cluster=%q) = %q, want %q", c.name, c.configured, c.cluster, got, c.want)
		}
	}

	// A field with no cluster-reported version is untouched - the map is
	// keyed by SettingField.Key, and per-node fields are never in it.
	cfg := DefaultConfig()
	cfg.Versions.KubeVirt = "v1.9.1"
	if got := EffectiveVersion(kubevirt, &cfg, VersionCache{}); got != "v1.9.1" {
		t.Errorf("EffectiveVersion with an empty cache = %q, want the configured %q", got, "v1.9.1")
	}
}

// TestEffectiveVersionFloorsThePickerAtTheClusterVersion: the pickers for
// cluster-scoped fields must offer no option older than what the cluster
// runs, or the Update screen would present a downgrade as an upgrade on a
// node whose config predates someone else's upgrade.
func TestEffectiveVersionFloorsThePickerAtTheClusterVersion(t *testing.T) {
	var kubeovn SettingField
	for _, f := range SettingFields {
		if f.Key == "versions.kubeovn" {
			kubeovn = f
		}
	}
	if kubeovn.Options == nil {
		t.Fatal("versions.kubeovn has no Options func")
	}

	cfg := DefaultConfig()
	all := kubeovn.Options(&cfg, VersionCache{})
	if len(all) == 0 {
		t.Skip("no supported kube-ovn versions at or above the default - nothing to floor")
	}
	newest := all[0] // SupportedVersionsAtLeast sorts newest-first.

	// With the cluster already on the newest supported version, the only
	// option left is that version itself: no downgrade is offerable.
	versions := VersionCache{ClusterVersions: map[string]string{"versions.kubeovn": newest}}
	floored := kubeovn.Options(&cfg, versions)
	for _, o := range floored {
		if cmp, ok := versionspkg.CompareSemver(o, newest); ok && cmp < 0 {
			t.Errorf("versions.kubeovn offers %q while the cluster runs %q - that is a downgrade", o, newest)
		}
	}
}

// TestReconcileClusterVersionsIsUpwardOnly is the safety property the whole
// read-from-the-cluster design rests on: a cluster probe can land at any
// moment, including right after an operator picked an upgrade, so it must
// only ever raise a stale value and never lower a picked one.
func TestReconcileClusterVersionsIsUpwardOnly(t *testing.T) {
	t.Run("raises a stale node config to what the cluster runs", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Versions.KubeVirt = "v1.9.0"
		cfg.Versions.CDI = "v1.65.0"
		versions := VersionCache{ClusterVersions: map[string]string{
			"versions.kubevirt": "v1.9.1",
			"versions.cdi":      "v1.65.0",
		}}
		changed := ReconcileClusterVersions(&cfg, versions)
		if len(changed) != 1 || changed[0] != "versions.kubevirt" {
			t.Fatalf("ReconcileClusterVersions changed %v, want only versions.kubevirt", changed)
		}
		if cfg.Versions.KubeVirt != "v1.9.1" {
			t.Errorf("cfg.Versions.KubeVirt = %q, want the cluster's %q", cfg.Versions.KubeVirt, "v1.9.1")
		}
		if cfg.Versions.CDI != "v1.65.0" {
			t.Errorf("cfg.Versions.CDI = %q, want it untouched", cfg.Versions.CDI)
		}
	})

	t.Run("never lowers a version the operator just picked", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Versions.KubeVirt = "v1.10.0"
		versions := VersionCache{ClusterVersions: map[string]string{"versions.kubevirt": "v1.9.1"}}
		if changed := ReconcileClusterVersions(&cfg, versions); len(changed) != 0 {
			t.Errorf("ReconcileClusterVersions changed %v, want nothing - the pick is newer than the cluster", changed)
		}
		if cfg.Versions.KubeVirt != "v1.10.0" {
			t.Errorf("cfg.Versions.KubeVirt = %q, want the operator's pick %q to survive", cfg.Versions.KubeVirt, "v1.10.0")
		}
	})

	t.Run("an empty cache is a no-op", func(t *testing.T) {
		cfg := DefaultConfig()
		before := cfg
		if changed := ReconcileClusterVersions(&cfg, VersionCache{}); len(changed) != 0 {
			t.Errorf("ReconcileClusterVersions changed %v on an unreachable cluster, want nothing", changed)
		}
		if !reflect.DeepEqual(cfg, before) {
			t.Error("ReconcileClusterVersions mutated cfg with no cluster information")
		}
	})
}
