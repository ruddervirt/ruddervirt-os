// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"strings"
	"testing"
)

func TestApplyNetworkConfig(t *testing.T) {
	t.Run("no interface selected errors without touching the runner", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			t.Errorf("unexpected command: %s %v", name, args)
			return commandOutcome{}
		}}
		var err error
		withFakeRunner(r, func() { err = applyNetworkConfig(NetworkConfig{}) })
		if err == nil {
			t.Fatal("applyNetworkConfig({}) err = nil, want an error")
		}
	})

	t.Run("dhcp reuses an existing connection profile and brings it up", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "device", "show", "eth0") {
				return commandOutcome{out: []byte("GENERAL.CONNECTION:eth0-profile\n")}
			}
			return commandOutcome{}
		}}
		n := NetworkConfig{InterfaceName: "eth0", Addressing: "dhcp"}
		var err error
		withFakeRunner(r, func() { err = applyNetworkConfig(n) })
		if err != nil {
			t.Fatalf("applyNetworkConfig(dhcp) err = %v, want nil", err)
		}
		foundMod, foundUp := false, false
		for _, c := range r.calls {
			if cmdContains("", strings.Fields(c), "con", "mod", "eth0-profile", "ipv4.method", "auto") {
				foundMod = true
			}
			if cmdContains("", strings.Fields(c), "con", "up", "eth0-profile") {
				foundUp = true
			}
		}
		if !foundMod || !foundUp {
			t.Errorf("expected both a con-mod and con-up call, calls = %v", r.calls)
		}
		// No profile existed to add, since device show already returned one.
		// hasField (exact token match) avoids "ipv4.addresses" false-matching
		// a substring check for "add".
		for _, c := range r.calls {
			if hasField(c, "add") {
				t.Errorf("expected no con-add call when a profile already exists, calls = %v", r.calls)
			}
		}
	})

	t.Run("missing connection profile is created first", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "device", "show", "eth0") {
				return commandOutcome{err: errFake}
			}
			return commandOutcome{}
		}}
		n := NetworkConfig{InterfaceName: "eth0", Addressing: "dhcp"}
		var err error
		withFakeRunner(r, func() { err = applyNetworkConfig(n) })
		if err != nil {
			t.Fatalf("applyNetworkConfig(dhcp) err = %v, want nil", err)
		}
		found := false
		for _, c := range r.calls {
			if cmdContains("", strings.Fields(c), "con", "add", "type", "ethernet", "con-name", "eth0") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a con-add call, calls = %v", r.calls)
		}
	})

	t.Run("static addressing missing fields errors", func(t *testing.T) {
		r := &fakeRunner{}
		n := NetworkConfig{InterfaceName: "eth0", Addressing: "static"}
		var err error
		withFakeRunner(r, func() { err = applyNetworkConfig(n) })
		if err == nil {
			t.Fatal("applyNetworkConfig(static, no IP) err = nil, want an error")
		}
	})

	t.Run("static addressing with all fields sets manual ipv4", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "device", "show", "eth0") {
				return commandOutcome{out: []byte("GENERAL.CONNECTION:eth0-profile\n")}
			}
			return commandOutcome{}
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
		withFakeRunner(r, func() { err = applyNetworkConfig(n) })
		if err != nil {
			t.Fatalf("applyNetworkConfig(static) err = %v, want nil", err)
		}
		found := false
		for _, c := range r.calls {
			if cmdContains("", strings.Fields(c), "ipv4.method", "manual", "10.0.0.5/24", "10.0.0.1") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a con-mod call with the static ipv4 settings, calls = %v", r.calls)
		}
	})
}
