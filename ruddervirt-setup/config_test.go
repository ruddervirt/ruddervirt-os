// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
	cfg := defaultConfig()
	if cfg.Network.Addressing != "dhcp" {
		t.Errorf("defaultConfig().Network.Addressing = %q, want %q", cfg.Network.Addressing, "dhcp")
	}
	if cfg.Storage.Engine != "openebs" {
		t.Errorf("defaultConfig().Storage.Engine = %q, want %q", cfg.Storage.Engine, "openebs")
	}
	if cfg.Versions.K3s != defaultK3sVersion {
		t.Errorf("defaultConfig().Versions.K3s = %q, want %q", cfg.Versions.K3s, defaultK3sVersion)
	}
	if !cfg.System.AutoUpdate {
		t.Error("defaultConfig().System.AutoUpdate = false, want true")
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("missing file returns defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatalf("loadConfig(%q) err = %v, want nil", path, err)
		}
		if !reflect.DeepEqual(cfg, defaultConfig()) {
			t.Errorf("loadConfig(%q) = %+v, want defaults %+v", path, cfg, defaultConfig())
		}
	})

	t.Run("existing file overlays onto defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ruddervirt-setup.yaml")
		yaml := "network:\n  interface_name: eth0\n  addressing: static\nstorage:\n  engine: longhorn\n"
		if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatalf("loadConfig(%q) err = %v, want nil", path, err)
		}
		if cfg.Network.InterfaceName != "eth0" || cfg.Network.Addressing != "static" {
			t.Errorf("loadConfig(%q) network = %+v, want interface eth0/static", path, cfg.Network)
		}
		if cfg.Storage.Engine != "longhorn" {
			t.Errorf("loadConfig(%q) Storage.Engine = %q, want %q", path, cfg.Storage.Engine, "longhorn")
		}
		// Fields absent from the file fall back to the defaults, not zero values.
		if cfg.Versions.K3s != defaultK3sVersion {
			t.Errorf("loadConfig(%q) Versions.K3s = %q, want default %q", path, cfg.Versions.K3s, defaultK3sVersion)
		}
	})

	t.Run("invalid yaml returns an error and fresh defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.yaml")
		if err := os.WriteFile(path, []byte("not: [valid: yaml"), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		cfg, err := loadConfig(path)
		if err == nil {
			t.Fatalf("loadConfig(%q) err = nil, want an error", path)
		}
		if !reflect.DeepEqual(cfg, defaultConfig()) {
			t.Errorf("loadConfig(%q) on error = %+v, want defaults %+v", path, cfg, defaultConfig())
		}
	})
}
