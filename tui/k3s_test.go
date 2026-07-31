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

func TestParseK3sVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want parsedK3sVersion
		ok   bool
	}{
		{"final release", "v1.34.5+k3s1", parsedK3sVersion{major: 1, minor: 34, patch: 5, build: 1}, true},
		{"release candidate", "v1.34.5-rc1+k3s1", parsedK3sVersion{major: 1, minor: 34, patch: 5, hasRC: true, rc: 1, build: 1}, true},
		{"unparseable", "garbage", parsedK3sVersion{}, false},
		{"missing +k3sN suffix", "v1.34.5", parsedK3sVersion{}, false},
	}
	for _, c := range cases {
		got, ok := parseK3sVersion(c.in)
		if ok != c.ok {
			t.Errorf("%s: parseK3sVersion(%q) ok = %v, want %v", c.name, c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("%s: parseK3sVersion(%q) = %+v, want %+v", c.name, c.in, got, c.want)
		}
	}
}

func TestCompareK3sVersions(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int // sign only
		ok   bool
	}{
		{"equal", "v1.34.5+k3s1", "v1.34.5+k3s1", 0, true},
		{"a newer minor", "v1.35.0+k3s1", "v1.34.5+k3s1", 1, true},
		{"a older patch", "v1.34.4+k3s1", "v1.34.5+k3s1", -1, true},
		{"rc sorts below final of same version", "v1.34.5-rc1+k3s1", "v1.34.5+k3s1", -1, true},
		{"final sorts above rc of same version", "v1.34.5+k3s1", "v1.34.5-rc1+k3s1", 1, true},
		{"higher build sorts above", "v1.34.5+k3s2", "v1.34.5+k3s1", 1, true},
		{"unparseable a", "garbage", "v1.34.5+k3s1", 0, false},
		{"unparseable b", "v1.34.5+k3s1", "garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := compareK3sVersions(c.a, c.b)
		if ok != c.ok {
			t.Errorf("%s: compareK3sVersions(%q, %q) ok = %v, want %v", c.name, c.a, c.b, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		sign := 0
		if got > 0 {
			sign = 1
		} else if got < 0 {
			sign = -1
		}
		if sign != c.want {
			t.Errorf("%s: compareK3sVersions(%q, %q) = %d (sign %d), want sign %d", c.name, c.a, c.b, got, sign, c.want)
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
