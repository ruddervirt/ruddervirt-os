// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"testing"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
	versionspkg "ruddervirt-setup/internal/versions"
)

// drainStrings collects every stepOutputMsg sent to ch without blocking the
// caller - callers still hold ch open, so this only reads what's already
// buffered. Used by adopt_test.go and other package-main tests that drive
// installsteps.Step-shaped functions directly.
func drainStrings(ch chan installsteps.StepMsg) []string {
	var out []string
	for {
		select {
		case msg := <-ch:
			if s, ok := msg.(stepOutputMsg); ok {
				out = append(out, string(s))
			}
		default:
			return out
		}
	}
}

func TestResolveK3sVersion(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"unset falls back to default", config.Config{}, versionspkg.DefaultK3sVersion},
		{"whitespace-only falls back to default", config.Config{Versions: config.VersionsConfig{K3s: "   "}}, versionspkg.DefaultK3sVersion},
		{"explicit value is trimmed and passed through", config.Config{Versions: config.VersionsConfig{K3s: "  v1.30.0+k3s1  "}}, "v1.30.0+k3s1"},
	}
	for _, c := range cases {
		if got := resolveK3sVersion(c.cfg); got != c.want {
			t.Errorf("%s: resolveK3sVersion() = %q, want %q", c.name, got, c.want)
		}
	}
}
