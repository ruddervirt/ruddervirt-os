package main

import "testing"

func TestParseInstalledK3sVersionOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"normal two-line output", "k3s version v1.34.5+k3s1 (abcdef12)\ngo version go1.23.4\n", "v1.34.5+k3s1", true},
		{"unparseable version token", "k3s version garbage\n", "", false},
		{"empty output", "", "", false},
		{"no k3s version line", "go version go1.23.4\n", "", false},
	}
	for _, c := range cases {
		got, ok := parseInstalledK3sVersionOutput(c.in)
		if ok != c.ok {
			t.Errorf("%s: parseInstalledK3sVersionOutput(%q) ok = %v, want %v", c.name, c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("%s: parseInstalledK3sVersionOutput(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestResolveK3sVersion(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"unset falls back to default", Config{}, defaultK3sVersion},
		{"whitespace-only falls back to default", Config{Versions: VersionsConfig{K3s: "   "}}, defaultK3sVersion},
		{"explicit value is trimmed and passed through", Config{Versions: VersionsConfig{K3s: "  v1.30.0+k3s1  "}}, "v1.30.0+k3s1"},
	}
	for _, c := range cases {
		if got := resolveK3sVersion(c.cfg); got != c.want {
			t.Errorf("%s: resolveK3sVersion() = %q, want %q", c.name, got, c.want)
		}
	}
}
