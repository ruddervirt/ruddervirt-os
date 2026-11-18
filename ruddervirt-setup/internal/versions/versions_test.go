// SPDX-License-Identifier: GPL-3.0-only

package versions

import "testing"

func TestParseSemver(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want parsedSemver
		ok   bool
	}{
		{"clean release", "v1.2.3", parsedSemver{major: 1, minor: 2, patch: 3}, true},
		{"rc suffix rejected", "v1.2.3-rc1", parsedSemver{}, false},
		{"build suffix rejected", "v1.2.3+build1", parsedSemver{}, false},
		{"missing v prefix rejected", "1.2.3", parsedSemver{}, false},
		{"garbage", "not-a-version", parsedSemver{}, false},
		{"zero patch", "v0.0.1", parsedSemver{major: 0, minor: 0, patch: 1}, true},
		{"padded with whitespace", "  v2.10.0  ", parsedSemver{major: 2, minor: 10, patch: 0}, true},
		{"dev build literal rejected", "dev", parsedSemver{}, false},
		{"empty string rejected", "", parsedSemver{}, false},
		{"missing patch rejected", "v1.2", parsedSemver{}, false},
	}
	for _, c := range cases {
		got, ok := ParseSemver(c.in)
		if ok != c.ok {
			t.Errorf("%s: ParseSemver(%q) ok = %v, want %v", c.name, c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("%s: ParseSemver(%q) = %+v, want %+v", c.name, c.in, got, c.want)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int // sign only
		ok   bool
	}{
		{"equal", "v1.2.3", "v1.2.3", 0, true},
		{"a newer major", "v2.0.0", "v1.9.9", 1, true},
		{"a older minor", "v1.1.0", "v1.2.0", -1, true},
		{"a older patch", "v1.2.2", "v1.2.3", -1, true},
		{"unparseable a", "garbage", "v1.2.3", 0, false},
		{"unparseable b", "v1.2.3", "garbage", 0, false},
		{"equal patch bump", "v1.2.4", "v1.2.3", 1, true},
		{"a newer minor", "v1.3.0", "v1.2.9", 1, true},
		{"dev build literal, a", "dev", "v1.2.3", 0, false},
		{"dev build literal, b", "v1.2.3", "dev", 0, false},
		{"empty a", "", "v1.2.3", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareSemver(c.a, c.b)
		if ok != c.ok {
			t.Errorf("%s: CompareSemver(%q, %q) ok = %v, want %v", c.name, c.a, c.b, ok, c.ok)
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
			t.Errorf("%s: CompareSemver(%q, %q) = %d (sign %d), want sign %d", c.name, c.a, c.b, got, sign, c.want)
		}
	}
}

// TestNewerVersion covers the single reconciliation rule the multi-node
// upgrade path rests on: a node's configured version and the version read
// back off the live cluster are merged by taking the newer, so a stale
// config is repaired upward and an operator's pick is never undone.
func TestNewerVersion(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want string
	}{
		{"b newer", "v1.9.0", "v1.9.1", "v1.9.1"},
		{"a newer", "v1.10.0", "v1.9.1", "v1.10.0"},
		{"equal", "v1.9.1", "v1.9.1", "v1.9.1"},
		{"b unknown", "v1.9.1", "", "v1.9.1"},
		{"a unknown", "", "v1.9.1", "v1.9.1"},
		{"both unknown", "", "", ""},
		{"b unparseable keeps a", "v1.9.1", "not-a-version", "v1.9.1"},
		{"a unparseable keeps a", "1.9-rc1", "v1.9.1", "1.9-rc1"},
		{"minor beats patch", "v1.9.9", "v1.10.0", "v1.10.0"},
		{"major beats minor", "v1.99.0", "v2.0.0", "v2.0.0"},
	}
	for _, c := range cases {
		if got := NewerVersion(c.a, c.b); got != c.want {
			t.Errorf("%s: NewerVersion(%q, %q) = %q, want %q", c.name, c.a, c.b, got, c.want)
		}
	}
}
