// SPDX-License-Identifier: GPL-3.0-only

package main

import "testing"

func TestParseShadowHash(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			"default admin entry",
			"admin:$6$ruddervirt0$hVyBGZxUjDrV7kW.cEWoJxBLIWzCDxPDRjE3NgDEmeAMoNEpXoJa2fR6PGEKu5dnI78I22zK5z1cC.IEDOnAZ/:19700:0:99999:7:::\n",
			"$6$ruddervirt0$hVyBGZxUjDrV7kW.cEWoJxBLIWzCDxPDRjE3NgDEmeAMoNEpXoJa2fR6PGEKu5dnI78I22zK5z1cC.IEDOnAZ/",
			true,
		},
		{"changed password", "admin:$6$othersalt$abcdef:19700:0:99999:7:::", "$6$othersalt$abcdef", true},
		{"locked account", "admin:!:19700:0:99999:7:::", "!", true},
		{"empty hash field", "admin::19700:0:99999:7:::", "", false},
		{"no colon", "admin", "", false},
		{"empty line", "", "", false},
	}
	for _, c := range cases {
		got, err := parseShadowHash(c.in)
		if (err == nil) != c.ok {
			t.Errorf("%s: parseShadowHash(%q) err = %v, want ok = %v", c.name, c.in, err, c.ok)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("%s: parseShadowHash(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
