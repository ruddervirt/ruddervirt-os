// SPDX-License-Identifier: GPL-3.0-only

package network

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ruddervirt-setup/internal/exec"
)

// NetworkConfig holds the operator's network settings, persisted as part of
// Config.Network.
type NetworkConfig struct {
	// InterfaceName is the internet-facing interface (has a default
	// route) - auto-detected if left empty, see DetectDefaultInterface
	// and ResolveNetworkForInstall below.
	InterfaceName string `yaml:"interface_name"`
	// Addressing is "dhcp" or "static". The fields below only apply, and
	// are only required, when static - see ApplyNetworkConfig, which
	// configures InterfaceName via nmcli whenever these are saved.
	Addressing string   `yaml:"addressing"`
	StaticIP   string   `yaml:"static_ip"`
	Prefix     int      `yaml:"prefix"`
	Gateway    string   `yaml:"gateway"`
	DNSServers []string `yaml:"dns_servers"`
	PodCIDR    string   `yaml:"pod_cidr"`
	SvcCIDR    string   `yaml:"svc_cidr"`
}

// DetectDefaultInterface returns the interface carrying the machine's
// default route, by reading the kernel's routing table directly.
func DetectDefaultInterface() (string, error) {
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

// ListNetworkInterfaces returns real NICs for the operator to pick from in
// Settings, filtering out loopback and software-only interfaces (docker0,
// veth*, br-*, etc.) by checking for a /sys/class/net/<iface>/device
// backing, which only real (or passed-through/virtio) devices have.
func ListNetworkInterfaces() ([]string, error) {
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

// ResolveLocalIP returns the address other cluster members would use to
// reach this node: the configured static IP when addressing is static, or
// whatever IPv4 address is currently live on the interface (e.g. a DHCP
// lease) otherwise. Always derived fresh, never stored.
func ResolveLocalIP(n NetworkConfig) (string, error) {
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

// ResolveNetworkForInstall is the pre-install gate: it auto-detects the
// interface if not already set, and confirms addressing is fully specified
// (IP, prefix, gateway present when "static"). Returns an error describing
// what's missing if install should be blocked.
func ResolveNetworkForInstall(n *NetworkConfig) error {
	if strings.TrimSpace(n.InterfaceName) == "" {
		iface, err := DetectDefaultInterface()
		if err != nil {
			return fmt.Errorf("no network interface detected (no default route found) - set one manually in Settings before installing")
		}
		n.InterfaceName = iface
	}

	if n.Addressing == "static" {
		if strings.TrimSpace(n.StaticIP) == "" || n.Prefix <= 0 || strings.TrimSpace(n.Gateway) == "" {
			return fmt.Errorf("static IP addressing is selected but IP/prefix/gateway aren't fully set - configure them in Settings")
		}
	}

	return nil
}

// nmcliBin is the standard NetworkManager CLI, present on Fedora CoreOS.
const nmcliBin = "/usr/bin/nmcli"

// connectionNameFor returns the NetworkManager connection profile currently
// bound to iface, creating a fresh ethernet profile if none exists yet.
func connectionNameFor(iface string) (string, error) {
	out, err := exec.RunPrivileged(nmcliBin, "-t", "-f", "GENERAL.CONNECTION", "device", "show", iface).Output()
	if err == nil {
		name := strings.TrimPrefix(strings.TrimSpace(string(out)), "GENERAL.CONNECTION:")
		if name != "" && name != "--" {
			return name, nil
		}
	}

	if out, err := exec.RunPrivileged(nmcliBin, "con", "add", "type", "ethernet", "con-name", iface, "ifname", iface).CombinedOutput(); err != nil {
		return "", exec.WrapCmdErr(out, err)
	}
	return iface, nil
}

// ApplyNetworkConfig configures n.InterfaceName via nmcli to match n: DHCP,
// or a static IP/prefix/gateway/DNS. Called by the install pipeline;
// Settings only checks and persists network values, never touches the live
// interface itself.
func ApplyNetworkConfig(n NetworkConfig) error {
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

	if out, err := exec.RunPrivileged(nmcliBin, args...).CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	if out, err := exec.RunPrivileged(nmcliBin, "con", "up", conName).CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	return nil
}
