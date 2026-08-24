// SPDX-License-Identifier: GPL-3.0-only

package main

import "testing"

// TestResolveNetworkForInstall covers the branches that don't touch the
// real OS - InterfaceName pre-set skips detectDefaultInterface entirely, so
// these cases stay hermetic. The InterfaceName=="" (auto-detect) branch
// depends on /proc/net/route and isn't covered here.
func TestResolveNetworkForInstall(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "dhcp with interface set is fine",
			cfg: Config{Network: NetworkConfig{
				InterfaceName: "eth0",
				Addressing:    "dhcp",
			}},
			wantErr: false,
		},
		{
			name: "static with all fields set is fine",
			cfg: Config{Network: NetworkConfig{
				InterfaceName: "eth0",
				Addressing:    "static",
				StaticIP:      "10.0.0.5",
				Prefix:        24,
				Gateway:       "10.0.0.1",
			}},
			wantErr: false,
		},
		{
			name: "static missing IP is an error",
			cfg: Config{Network: NetworkConfig{
				InterfaceName: "eth0",
				Addressing:    "static",
				Prefix:        24,
				Gateway:       "10.0.0.1",
			}},
			wantErr: true,
		},
		{
			name: "static missing prefix is an error",
			cfg: Config{Network: NetworkConfig{
				InterfaceName: "eth0",
				Addressing:    "static",
				StaticIP:      "10.0.0.5",
				Gateway:       "10.0.0.1",
			}},
			wantErr: true,
		},
		{
			name: "static missing gateway is an error",
			cfg: Config{Network: NetworkConfig{
				InterfaceName: "eth0",
				Addressing:    "static",
				StaticIP:      "10.0.0.5",
				Prefix:        24,
			}},
			wantErr: true,
		},
	}
	for _, c := range cases {
		cfg := c.cfg
		err := resolveNetworkForInstall(&cfg)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: resolveNetworkForInstall() err = %v, wantErr %v", c.name, err, c.wantErr)
		}
	}
}
