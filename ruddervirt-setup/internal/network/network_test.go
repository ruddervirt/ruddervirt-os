// SPDX-License-Identifier: GPL-3.0-only

package network

import (
	"strings"
	"testing"

	"ruddervirt-setup/internal/exec/exectest"
)

// TestResolveNetworkForInstall covers branches that don't touch the real
// OS - InterfaceName pre-set skips DetectDefaultInterface, keeping these
// cases hermetic. The auto-detect (InterfaceName=="") branch depends on
// /proc/net/route and isn't covered here.
func TestResolveNetworkForInstall(t *testing.T) {
	cases := []struct {
		name    string
		cfg     NetworkConfig
		wantErr bool
	}{
		{
			name: "dhcp with interface set is fine",
			cfg: NetworkConfig{
				InterfaceName: "eth0",
				Addressing:    "dhcp",
			},
			wantErr: false,
		},
		{
			name: "static with all fields set is fine",
			cfg: NetworkConfig{
				InterfaceName: "eth0",
				Addressing:    "static",
				StaticIP:      "10.0.0.5",
				Prefix:        24,
				Gateway:       "10.0.0.1",
			},
			wantErr: false,
		},
		{
			name: "static missing IP is an error",
			cfg: NetworkConfig{
				InterfaceName: "eth0",
				Addressing:    "static",
				Prefix:        24,
				Gateway:       "10.0.0.1",
			},
			wantErr: true,
		},
		{
			name: "static missing prefix is an error",
			cfg: NetworkConfig{
				InterfaceName: "eth0",
				Addressing:    "static",
				StaticIP:      "10.0.0.5",
				Gateway:       "10.0.0.1",
			},
			wantErr: true,
		},
		{
			name: "static missing gateway is an error",
			cfg: NetworkConfig{
				InterfaceName: "eth0",
				Addressing:    "static",
				StaticIP:      "10.0.0.5",
				Prefix:        24,
			},
			wantErr: true,
		},
	}
	for _, c := range cases {
		cfg := c.cfg
		err := ResolveNetworkForInstall(&cfg)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: ResolveNetworkForInstall() err = %v, wantErr %v", c.name, err, c.wantErr)
		}
	}
}

func TestApplyNetworkConfig(t *testing.T) {
	t.Run("no interface selected errors without touching the runner", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			t.Errorf("unexpected command: %s %v", name, args)
			return exectest.Outcome{}
		}}
		var err error
		exectest.WithFakeRunner(r, func() { err = ApplyNetworkConfig(NetworkConfig{}) })
		if err == nil {
			t.Fatal("ApplyNetworkConfig({}) err = nil, want an error")
		}
	})

	t.Run("dhcp reuses an existing connection profile and brings it up", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "device", "show", "eth0") {
				return exectest.Outcome{Out: []byte("GENERAL.CONNECTION:eth0-profile\n")}
			}
			return exectest.Outcome{}
		}}
		n := NetworkConfig{InterfaceName: "eth0", Addressing: "dhcp"}
		var err error
		exectest.WithFakeRunner(r, func() { err = ApplyNetworkConfig(n) })
		if err != nil {
			t.Fatalf("ApplyNetworkConfig(dhcp) err = %v, want nil", err)
		}
		foundMod, foundUp := false, false
		for _, c := range r.Calls {
			if exectest.CmdContains("", strings.Fields(c), "con", "mod", "eth0-profile", "ipv4.method", "auto") {
				foundMod = true
			}
			if exectest.CmdContains("", strings.Fields(c), "con", "up", "eth0-profile") {
				foundUp = true
			}
		}
		if !foundMod || !foundUp {
			t.Errorf("expected both a con-mod and con-up call, calls = %v", r.Calls)
		}
		// No profile should be added since device show already returned one.
		// HasField (exact token match) avoids "ipv4.addresses" false-matching.
		for _, c := range r.Calls {
			if exectest.HasField(c, "add") {
				t.Errorf("expected no con-add call when a profile already exists, calls = %v", r.Calls)
			}
		}
	})

	t.Run("missing connection profile is created first", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "device", "show", "eth0") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		n := NetworkConfig{InterfaceName: "eth0", Addressing: "dhcp"}
		var err error
		exectest.WithFakeRunner(r, func() { err = ApplyNetworkConfig(n) })
		if err != nil {
			t.Fatalf("ApplyNetworkConfig(dhcp) err = %v, want nil", err)
		}
		found := false
		for _, c := range r.Calls {
			if exectest.CmdContains("", strings.Fields(c), "con", "add", "type", "ethernet", "con-name", "eth0") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a con-add call, calls = %v", r.Calls)
		}
	})

	t.Run("static addressing missing fields errors", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		n := NetworkConfig{InterfaceName: "eth0", Addressing: "static"}
		var err error
		exectest.WithFakeRunner(r, func() { err = ApplyNetworkConfig(n) })
		if err == nil {
			t.Fatal("ApplyNetworkConfig(static, no IP) err = nil, want an error")
		}
	})

	t.Run("static addressing with all fields sets manual ipv4", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "device", "show", "eth0") {
				return exectest.Outcome{Out: []byte("GENERAL.CONNECTION:eth0-profile\n")}
			}
			return exectest.Outcome{}
		}}
		n := NetworkConfig{
			InterfaceName: "eth0",
			Addressing:    "static",
			StaticIP:      "10.0.0.5",
			Prefix:        24,
			Gateway:       "10.0.0.1",
			DNSServers:    []string{"1.1.1.1"},
		}
		var err error
		exectest.WithFakeRunner(r, func() { err = ApplyNetworkConfig(n) })
		if err != nil {
			t.Fatalf("ApplyNetworkConfig(static) err = %v, want nil", err)
		}
		found := false
		for _, c := range r.Calls {
			if exectest.CmdContains("", strings.Fields(c), "ipv4.method", "manual", "10.0.0.5/24", "10.0.0.1") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a con-mod call with the static ipv4 settings, calls = %v", r.Calls)
		}
	})
}
