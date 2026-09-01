// SPDX-License-Identifier: GPL-3.0-only

package network

import "testing"

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
