// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"testing"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/versions"
)

func TestResolveKubeVirtAndCDIVersion(t *testing.T) {
	cases := []struct {
		name         string
		cfg          config.Config
		wantKubeVirt string
		wantCDI      string
	}{
		{"unset falls back to defaults", config.Config{}, versions.DefaultKubeVirtVersion, versions.DefaultCDIVersion},
		{
			"explicit values are trimmed and passed through",
			config.Config{Versions: config.VersionsConfig{KubeVirt: "  v1.9.0  ", CDI: "  v1.65.0  "}},
			"v1.9.0", "v1.65.0",
		},
	}
	for _, c := range cases {
		if got := resolveKubeVirtVersion(c.cfg); got != c.wantKubeVirt {
			t.Errorf("%s: resolveKubeVirtVersion() = %q, want %q", c.name, got, c.wantKubeVirt)
		}
		if got := resolveCDIVersion(c.cfg); got != c.wantCDI {
			t.Errorf("%s: resolveCDIVersion() = %q, want %q", c.name, got, c.wantCDI)
		}
	}
}
