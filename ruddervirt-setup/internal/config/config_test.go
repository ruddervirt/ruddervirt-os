// SPDX-License-Identifier: GPL-3.0-only

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// TestClusterScopedFieldsLockOnAJoiningNode: every component applied by
// prepareK3sStep/ApplyKubeOvnStep is cluster-wide, and a joining node skips
// those steps - so offering an upgrade there would persist a version this
// node will never apply, and render it as though it had. k3s is the
// exception: its binary is per-node.
func TestClusterScopedFieldsLockOnAJoiningNode(t *testing.T) {
	clusterScoped := map[string]bool{
		"versions.kubeovn":          true,
		"versions.multus":           true,
		"versions.kubevirt":         true,
		"versions.cdi":              true,
		"versions.aileron":          true,
		"system.aileron_ui_enabled": true,
	}
	nodeScoped := map[string]bool{
		"versions.k3s":       true,
		"system.auto_update": true,
	}

	joining := VersionCache{ClusterJoinTarget: "https://10.0.0.5:6443"}
	standalone := VersionCache{}
	cfg := DefaultConfig()

	seen := map[string]bool{}
	for _, f := range SettingFields {
		locked := false
		reason := ""
		if f.Locked != nil {
			locked, reason = f.Locked(&cfg, joining)
		}
		switch {
		case clusterScoped[f.Key]:
			seen[f.Key] = true
			if !locked {
				t.Errorf("%s: Locked() = false on a joining node, want it locked as cluster-wide", f.Key)
			}
			if !strings.Contains(reason, "first node") {
				t.Errorf("%s: Locked() reason = %q, want it to point at the cluster's first node", f.Key, reason)
			}
		case nodeScoped[f.Key]:
			seen[f.Key] = true
			if locked {
				t.Errorf("%s: Locked() = true (%q) on a joining node, want a per-node field to stay editable", f.Key, reason)
			}
		}

		// Nothing may lock on a node that bootstrapped its own cluster, which
		// is every install that predates join support. storage.engine is
		// excluded: it locks on its own on-disk marker, not on cfg.
		if f.Locked != nil && f.Key != "storage.engine" {
			if locked, reason := f.Locked(&cfg, standalone); locked {
				t.Errorf("%s: Locked() = true (%q) on a standalone node, want it editable", f.Key, reason)
			}
		}
	}

	for key := range clusterScoped {
		if !seen[key] {
			t.Errorf("%s is no longer a SettingField - this test's cluster-scoped list is stale", key)
		}
	}
	for key := range nodeScoped {
		if !seen[key] {
			t.Errorf("%s is no longer a SettingField - this test's node-scoped list is stale", key)
		}
	}
}
