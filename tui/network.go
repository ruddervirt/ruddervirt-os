package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// detectDefaultInterface returns the interface carrying the machine's
// default route - i.e. whatever can currently reach the internet - by
// reading the kernel's routing table directly, no external commands needed.
func detectDefaultInterface() (string, error) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", err
	}

	bestIface := ""
	bestMetric := -1
	lines := strings.Split(string(data), "\n")
	for _, line := range lines[1:] { // first line is the header
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		iface, dest, metricField := fields[0], fields[1], fields[6]
		if dest != "00000000" { // only the default route (0.0.0.0/0)
			continue
		}
		metric, err := strconv.Atoi(metricField)
		if err != nil {
			continue
		}
		if bestMetric == -1 || metric < bestMetric {
			bestMetric = metric
			bestIface = iface
		}
	}

	if bestIface == "" {
		return "", fmt.Errorf("no default route found")
	}
	return bestIface, nil
}

// listNetworkInterfaces returns real NICs (backed by actual hardware) for
// the operator to pick from in Settings, filtering out loopback and
// software-only interfaces (docker0, veth*, br-*, tailscale0, etc.) by
// checking for a /sys/class/net/<iface>/device backing - only interfaces
// tied to a real (or passed-through/virtio) device have one.
func listNetworkInterfaces() ([]string, error) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if name == "lo" {
			continue
		}
		if _, err := os.Stat(filepath.Join("/sys/class/net", name, "device")); err != nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// resolveLocalIP returns the address other cluster members would use to
// reach this node: the configured static IP when addressing is static (no
// need to ask the OS - that's exactly what will be assigned), or whatever
// IPv4 address is currently live on the interface otherwise (e.g. a DHCP
// lease). Not a stored setting - always derived fresh from the interface.
func resolveLocalIP(n NetworkConfig) (string, error) {
	if n.Addressing == "static" {
		if strings.TrimSpace(n.StaticIP) == "" {
			return "", fmt.Errorf("static addressing selected but no static IP is set")
		}
		return n.StaticIP, nil
	}

	if strings.TrimSpace(n.InterfaceName) == "" {
		return "", fmt.Errorf("no interface selected")
	}
	iface, err := net.InterfaceByName(n.InterfaceName)
	if err != nil {
		return "", err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil {
			return ip4.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address currently assigned to %s", n.InterfaceName)
}

// resolveNetworkForInstall is the pre-install gate: it fills in the
// internet-facing interface via auto-detection if not already set, and
// confirms addressing is fully specified (IP, prefix, and gateway are all
// present when addressing is "static"). Returns an error describing what's
// missing if install should be blocked.
func resolveNetworkForInstall(cfg *Config) error {
	if strings.TrimSpace(cfg.Network.InterfaceName) == "" {
		iface, err := detectDefaultInterface()
		if err != nil {
			return fmt.Errorf("no network interface detected (no default route found) - set one manually in Settings before installing")
		}
		cfg.Network.InterfaceName = iface
	}

	if cfg.Network.Addressing == "static" {
		if strings.TrimSpace(cfg.Network.StaticIP) == "" || cfg.Network.Prefix <= 0 || strings.TrimSpace(cfg.Network.Gateway) == "" {
			return fmt.Errorf("static IP addressing is selected but IP/prefix/gateway aren't fully set - configure them in Settings")
		}
	}

	return nil
}

// nmcliBin is the standard NetworkManager CLI, present on Fedora CoreOS.
const nmcliBin = "/usr/bin/nmcli"

// connectionNameFor returns the NetworkManager connection profile currently
// bound to iface, creating a fresh ethernet profile if none exists yet
// (e.g. the interface has never been configured through NetworkManager).
func connectionNameFor(iface string) (string, error) {
	out, err := runPrivileged(nmcliBin, "-t", "-f", "GENERAL.CONNECTION", "device", "show", iface).Output()
	if err == nil {
		name := strings.TrimPrefix(strings.TrimSpace(string(out)), "GENERAL.CONNECTION:")
		if name != "" && name != "--" {
			return name, nil
		}
	}

	if out, err := runPrivileged(nmcliBin, "con", "add", "type", "ethernet", "con-name", iface, "ifname", iface).CombinedOutput(); err != nil {
		return "", wrapCmdErr(out, err)
	}
	return iface, nil
}

// applyNetworkConfig configures n.InterfaceName via nmcli to match n: DHCP,
// or a static IP/prefix/gateway/DNS. Called by the install pipeline's
// "Configuring network" step - Settings only checks and persists network
// values, it never touches the live interface itself.
func applyNetworkConfig(n NetworkConfig) error {
	iface := strings.TrimSpace(n.InterfaceName)
	if iface == "" {
		return fmt.Errorf("no interface selected")
	}

	conName, err := connectionNameFor(iface)
	if err != nil {
		return fmt.Errorf("finding connection for %s: %w", iface, err)
	}

	var args []string
	switch n.Addressing {
	case "static":
		if strings.TrimSpace(n.StaticIP) == "" || n.Prefix <= 0 || strings.TrimSpace(n.Gateway) == "" {
			return fmt.Errorf("static addressing requires IP, prefix, and gateway to be set")
		}
		args = []string{
			"con", "mod", conName,
			"ipv4.method", "manual",
			"ipv4.addresses", fmt.Sprintf("%s/%d", n.StaticIP, n.Prefix),
			"ipv4.gateway", n.Gateway,
			"ipv4.dns", strings.Join(n.DNSServers, ","),
		}
	default: // "dhcp"
		args = []string{
			"con", "mod", conName,
			"ipv4.method", "auto",
			"ipv4.addresses", "",
			"ipv4.gateway", "",
			"ipv4.dns", "",
		}
	}

	if out, err := runPrivileged(nmcliBin, args...).CombinedOutput(); err != nil {
		return wrapCmdErr(out, err)
	}
	if out, err := runPrivileged(nmcliBin, "con", "up", conName).CombinedOutput(); err != nil {
		return wrapCmdErr(out, err)
	}
	return nil
}
