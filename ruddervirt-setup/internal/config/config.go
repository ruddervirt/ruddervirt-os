// SPDX-License-Identifier: GPL-3.0-only

package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/k3s"
	"ruddervirt-setup/internal/network"
	"ruddervirt-setup/internal/storage"
	versionspkg "ruddervirt-setup/internal/versions"
)

const (
	// ConfigPath is where Config is persisted - root-owned, so writes go
	// through WritePrivileged rather than a direct os.WriteFile.
	ConfigPath = "/etc/ruddervirt/ruddervirt-setup.yaml"
	// ZincatiConfigPath matches the drop-in server.bu provisions at image
	// build time, so this TUI and Ignition's baked-in default share one
	// source of truth.
	ZincatiConfigPath = "/etc/zincati/config.d/99-ruddervirt-updates.toml"
)

type SystemConfig struct {
	AutoUpdate bool `yaml:"auto_update"`
	// PasswordChanged records that the admin password has been confirmed
	// changed from server.bu's well-known default (see
	// checkPasswordChangedCmd in password.go). Once true, "configure" skips
	// re-checking /etc/shadow every time.
	PasswordChanged bool `yaml:"password_changed"`
	// HostnameDeclared records that the operator has confirmed/set the
	// hostname away from (or explicitly kept) server.bu's default (see
	// internal/hostname.HostnameIsDefault). Once true, "configure" skips
	// re-checking the live hostname.
	HostnameDeclared bool `yaml:"hostname_declared"`
	// AileronUIEnabled toggles the aileronUI.enabled Helm value (see
	// applyAileron in aileron_bridge.go). Defaults to true, but the chart
	// flags the UI as UNAUTHENTICATED and able to create/delete VMs and open
	// any console - worth letting an operator turn off on an untrusted network.
	AileronUIEnabled bool `yaml:"aileron_ui_enabled"`
}

type VersionsConfig struct {
	K3s      string `yaml:"k3s"`
	KubeVirt string `yaml:"kubevirt"`
	CDI      string `yaml:"cdi"`
	Aileron  string `yaml:"aileron"`
	KubeOVN  string `yaml:"kubeovn"`
	Multus   string `yaml:"multus"`
}

// StorageConfig.Engine picks which storage layer prepareStorageDevice and
// prepareK3sStep set up: "rook-ceph", "longhorn", or "openebs" (LVM
// LocalPV). Once the disk is actually prepared for an engine, the choice
// locks (see the storage.engine SettingField's Locked func below) - switching
// engines on a node that may hold VM data isn't a supported migration.
type StorageConfig struct {
	Engine string `yaml:"engine"`
}

// StabilizerConfig holds only the non-secret values needed to (re-)apply the
// stabilizer HelmChart, e.g. on a future version bump. NATS credentials and
// the Nebula mesh identity are NEVER stored here, or anywhere in Config -
// see pendingStabilizer* in stabilizer.go.
type StabilizerConfig struct {
	Zone    string `yaml:"zone"`
	NatsURL string `yaml:"nats_url"`
	Version string `yaml:"version"`
}

type Config struct {
	Network    network.NetworkConfig `yaml:"network"`
	System     SystemConfig          `yaml:"system"`
	Versions   VersionsConfig        `yaml:"versions"`
	Storage    StorageConfig         `yaml:"storage"`
	Stabilizer StabilizerConfig      `yaml:"stabilizer"`
}

func DefaultConfig() Config {
	return Config{
		Network: network.NetworkConfig{
			InterfaceName: "", // auto-detected at startup/install time if empty
			Addressing:    "dhcp",
			StaticIP:      "",
			Prefix:        24,
			Gateway:       "",
			DNSServers:    []string{"1.1.1.1", "8.8.8.8"},
			PodCIDR:       "10.42.0.0/16",
			SvcCIDR:       "10.43.0.0/16",
		},
		System: SystemConfig{
			AutoUpdate:       true,
			AileronUIEnabled: true,
		},
		Versions: VersionsConfig{
			K3s:      versionspkg.DefaultK3sVersion,
			KubeVirt: versionspkg.DefaultKubeVirtVersion,
			CDI:      versionspkg.DefaultCDIVersion,
			Aileron:  versionspkg.DefaultAileronVersion,
			KubeOVN:  versionspkg.DefaultKubeOVNVersion,
			Multus:   versionspkg.DefaultMultusVersion,
		},
		Storage: StorageConfig{
			Engine: "openebs",
		},
		Stabilizer: StabilizerConfig{
			Version: versionspkg.DefaultStabilizerVersion,
		},
	}
}

// LoadConfig reads path and overlays it onto the defaults. A missing file is
// not an error - it just means the operator hasn't changed anything yet.
// The file is only written on an explicit save (see SaveConfig), since
// /etc/ruddervirt is root-owned and writing it at startup, before the TUI
// has even painted, would surprise the operator with a password prompt.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), err
	}
	return cfg, nil
}

// ConfigSaved reports whether ConfigPath exists, i.e. the operator has saved
// Settings at least once via "configure". Gates the home screen's
// "Services" summary, since probing systemctl/kubectl is pointless (and
// misleading) on an unconfigured system. A var (like exec.DefaultRunner) so
// tests can swap it instead of depending on the real /etc/ruddervirt path.
var ConfigSaved = func() bool {
	_, err := os.Stat(ConfigPath)
	return err == nil
}

// SaveConfig writes cfg to path. /etc/ruddervirt is root-owned, so the file
// is written to a temp path first and moved into place with runPrivileged,
// mirroring how installSteps writes the k3s systemd unit.
func SaveConfig(cfg Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return WritePrivileged(path, data)
}

// WriteZincatiConfig switches Fedora CoreOS's update agent between its
// periodic Saturday-midnight window (the server.bu default) and fully
// disabled, by rewriting the one Zincati drop-in this project owns.
func WriteZincatiConfig(autoUpdate bool, path string) error {
	content := `[updates]
enabled = false
`
	if autoUpdate {
		content = `[updates]
enabled = true
strategy = "periodic"

[updates.periodic]
time_zone = "US/Eastern"

[[updates.periodic.window]]
days = [ "Sat" ]
start_time = "00:00"
length_minutes = 60
`
	}
	return WritePrivileged(path, []byte(content))
}

// WritePrivileged writes data to path via a temp file + privileged move, so
// callers never need to hold write access to path's parent directory
// themselves.
func WritePrivileged(path string, data []byte) error {
	tmp, err := os.CreateTemp("", "ruddervirt-setup-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// os.CreateTemp defaults to 0600; these files are meant to be readable
	// config/env files, not secrets, so open them up like k3s.service does.
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}

	if out, err := exec.RunPrivileged("/usr/bin/mkdir", "-p", filepath.Dir(path)).CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	if out, err := exec.RunPrivileged("/usr/bin/mv", tmpPath, path).CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	return nil
}

// VersionCache carries the model's in-flight k3s/aileron version-fetch
// results, plus other live-detected cluster state, into SettingField.Options
// and SettingField.Locked, so those funcs read it via this parameter instead
// of a package-level global.
type VersionCache struct {
	K3s     []string
	Aileron []string
	// StabilizerDetected is true when a HelmChart named "stabilizer" exists
	// on the cluster (see internal/aileron.StabilizerChartPresent). Locks
	// the Aileron SettingFields below.
	StabilizerDetected bool
}

// SettingField binds one editable Settings-screen row to the Config field it
// reads and writes. Text fields use Get/Set with validation; boolean fields
// use toggle instead and flip immediately on Enter with no text-edit step.
type SettingField struct {
	Key   string
	Label string
	Get   func(cfg *Config) string
	Set   func(cfg *Config, val string) error
	// Options, if non-nil, marks this as a select field: Enter opens a
	// picker listing its return value instead of a free-text edit box.
	// Computed fresh each time (not cached on the field) since the choices
	// can depend on live state (detected NICs, fetched k3s releases).
	Options      func(cfg *Config, versions VersionCache) []string
	Advanced     bool // hidden behind the Settings screen's "Advanced settings" toggle
	StaticOnly   bool // only shown (nested under "IP addressing") when Network.Addressing == "static"
	NetworkSetup bool // hidden behind the "Local physical network setup" toggle
	// UpdateScreen moves the field off Settings onto the Update screen
	// instead (see UpdateRows in internal/tui/screens/update.go), so
	// upgrades live in one place separate from plain settings.
	UpdateScreen bool
	// Locked, if non-nil, can make the field temporarily read-only: Enter
	// becomes a no-op and the row shows the returned reason instead of
	// letting the operator edit/pick a new value. nil (the default for
	// every existing field) always means editable.
	Locked func(cfg *Config, versions VersionCache) (bool, string)
}

func parseRequiredIP(name, val string) (string, error) {
	val = strings.TrimSpace(val)
	if net.ParseIP(val) == nil {
		return "", fmt.Errorf("%s must be a valid IP address", name)
	}
	return val, nil
}

func parseCIDRField(name, val string) (string, error) {
	val = strings.TrimSpace(val)
	if _, _, err := net.ParseCIDR(val); err != nil {
		return "", fmt.Errorf("%s must be a valid CIDR (e.g. 10.42.0.0/16)", name)
	}
	return val, nil
}

func parsePrefixLen(val string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil || n < 1 || n > 32 {
		return 0, fmt.Errorf("prefix length must be a number between 1 and 32")
	}
	return n, nil
}

func parseDNSList(val string) ([]string, error) {
	var out []string
	for _, p := range strings.Split(val, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if net.ParseIP(p) == nil {
			return nil, fmt.Errorf("dns servers must be a comma-separated list of valid IPs")
		}
		out = append(out, p)
	}
	return out, nil
}

// stabilizerLocked backs both Aileron SettingFields' Locked funcs: if aileron
// is owned by a separate "stabilizer" Helm chart (see
// internal/aileron.StabilizerChartPresent), ruddervirt-setup must not touch
// it. prepareK3sStep (k3s_bridge.go) re-checks this at apply time too, so
// it's not just the interactive picker that's guarded.
func stabilizerLocked(c *Config, versions VersionCache) (bool, string) {
	if versions.StabilizerDetected {
		return true, "managed by stabilizer"
	}
	return false, ""
}

var SettingFields = []SettingField{
	{
		Key: "network.interface_name", Label: "Local network-interface (internet-facing)", NetworkSetup: true,
		Get: func(c *Config) string { return c.Network.InterfaceName },
		Options: func(c *Config, versions VersionCache) []string {
			ifaces, _ := network.ListNetworkInterfaces()
			return ifaces
		},
		Set: func(c *Config, v string) error {
			c.Network.InterfaceName = v
			return nil
		},
	},
	{
		Key: "network.addressing", Label: "IP addressing", NetworkSetup: true,
		Get:     func(c *Config) string { return c.Network.Addressing },
		Options: func(c *Config, versions VersionCache) []string { return []string{"dhcp", "static"} },
		Set: func(c *Config, v string) error {
			c.Network.Addressing = v
			return nil
		},
	},
	{
		// Only reachable in the UI when addressing is static (see
		// screens.SettingsModel.Rows), so validation can stay strict.
		Key: "network.static_ip", Label: "Static IP", StaticOnly: true,
		Get: func(c *Config) string { return c.Network.StaticIP },
		Set: func(c *Config, v string) error {
			s, err := parseRequiredIP("static IP", v)
			if err != nil {
				return err
			}
			c.Network.StaticIP = s
			return nil
		},
	},
	{
		Key: "network.prefix", Label: "Prefix length", StaticOnly: true,
		Get: func(c *Config) string { return strconv.Itoa(c.Network.Prefix) },
		Set: func(c *Config, v string) error {
			n, err := parsePrefixLen(v)
			if err != nil {
				return err
			}
			c.Network.Prefix = n
			return nil
		},
	},
	{
		Key: "network.gateway", Label: "Gateway", StaticOnly: true,
		Get: func(c *Config) string { return c.Network.Gateway },
		Set: func(c *Config, v string) error {
			s, err := parseRequiredIP("gateway", v)
			if err != nil {
				return err
			}
			c.Network.Gateway = s
			return nil
		},
	},
	{
		Key: "network.dns_servers", Label: "DNS servers (comma-separated)", StaticOnly: true,
		Get: func(c *Config) string { return strings.Join(c.Network.DNSServers, ", ") },
		Set: func(c *Config, v string) error {
			list, err := parseDNSList(v)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				return fmt.Errorf("at least one DNS server is required for static addressing")
			}
			c.Network.DNSServers = list
			return nil
		},
	},
	{
		Key: "network.pod_cidr", Label: "Pod CIDR", Advanced: true,
		Get: func(c *Config) string { return c.Network.PodCIDR },
		Set: func(c *Config, v string) error {
			s, err := parseCIDRField("pod CIDR", v)
			if err != nil {
				return err
			}
			c.Network.PodCIDR = s
			return nil
		},
	},
	{
		Key: "network.svc_cidr", Label: "Service CIDR", Advanced: true,
		Get: func(c *Config) string { return c.Network.SvcCIDR },
		Set: func(c *Config, v string) error {
			s, err := parseCIDRField("service CIDR", v)
			if err != nil {
				return err
			}
			c.Network.SvcCIDR = s
			return nil
		},
	},
	{
		Key: "system.auto_update", Label: "Automatic updates",
		Get: func(c *Config) string {
			if c.System.AutoUpdate {
				return "on"
			}
			return "off"
		},
		Options: func(c *Config, versions VersionCache) []string { return []string{"on", "off"} },
		Set: func(c *Config, v string) error {
			c.System.AutoUpdate = v == "on"
			return nil
		},
	},
	{
		Key: "versions.k3s", Label: "k3s version", UpdateScreen: true,
		Get: func(c *Config) string { return c.Versions.K3s },
		Options: func(c *Config, versions VersionCache) []string {
			// k3s doesn't support downgrades, so never offer anything older
			// than what's configured - equivalent to "the installed
			// version," but available even before the first install runs.
			var available []string
			for _, v := range versions.K3s {
				cmp, ok := k3s.CompareK3sVersions(v, c.Versions.K3s)
				if ok && cmp < 0 {
					continue
				}
				available = append(available, v)
			}
			return available
		},
		Set: func(c *Config, v string) error {
			c.Versions.K3s = v
			return nil
		},
	},
	{
		Key: "versions.kubeovn", Label: "kube-ovn version", UpdateScreen: true,
		Get: func(c *Config) string { return c.Versions.KubeOVN },
		Options: func(c *Config, versions VersionCache) []string {
			// Hand-curated allowlist (supported-versions.yaml), same as
			// KubeVirt/CDI below, not a live fetch - CNI components get no
			// benefit of the doubt on an untested upstream release.
			return versionspkg.SupportedVersionsAtLeast(versionspkg.SupportedVersions.KubeOVN, c.Versions.KubeOVN)
		},
		Set: func(c *Config, v string) error {
			c.Versions.KubeOVN = v
			return nil
		},
	},
	{
		Key: "versions.multus", Label: "Multus version", UpdateScreen: true,
		Get: func(c *Config) string { return c.Versions.Multus },
		Options: func(c *Config, versions VersionCache) []string {
			// Same hand-curated-allowlist reasoning as versions.kubeovn above.
			return versionspkg.SupportedVersionsAtLeast(versionspkg.SupportedVersions.Multus, c.Versions.Multus)
		},
		Set: func(c *Config, v string) error {
			c.Versions.Multus = v
			return nil
		},
	},
	{
		Key: "versions.kubevirt", Label: "KubeVirt version", UpdateScreen: true,
		Get: func(c *Config) string { return c.Versions.KubeVirt },
		Options: func(c *Config, versions VersionCache) []string {
			return versionspkg.SupportedVersionsAtLeast(versionspkg.SupportedVersions.KubeVirt, c.Versions.KubeVirt)
		},
		Set: func(c *Config, v string) error {
			c.Versions.KubeVirt = v
			return nil
		},
	},
	{
		Key: "versions.cdi", Label: "CDI version", UpdateScreen: true,
		Get: func(c *Config) string { return c.Versions.CDI },
		Options: func(c *Config, versions VersionCache) []string {
			return versionspkg.SupportedVersionsAtLeast(versionspkg.SupportedVersions.CDI, c.Versions.CDI)
		},
		Set: func(c *Config, v string) error {
			c.Versions.CDI = v
			return nil
		},
	},
	{
		Key: "versions.aileron", Label: "Aileron version", UpdateScreen: true,
		Get: func(c *Config) string { return c.Versions.Aileron },
		Options: func(c *Config, versions VersionCache) []string {
			// Aileron ships too frequently for a hand-curated list (unlike
			// KubeVirt/CDI) - fetch live from GitHub, same reasoning as
			// versions.k3s: never offer a downgrade from what's configured.
			var available []string
			for _, v := range versions.Aileron {
				cmp, ok := versionspkg.CompareSemver(v, c.Versions.Aileron)
				if ok && cmp < 0 {
					continue
				}
				available = append(available, v)
			}
			sort.SliceStable(available, func(i, j int) bool {
				cmp, ok := versionspkg.CompareSemver(available[i], available[j])
				return ok && cmp > 0
			})
			return available
		},
		Set: func(c *Config, v string) error {
			c.Versions.Aileron = v
			return nil
		},
		// Locks whenever a HelmChart named "stabilizer" is on the cluster -
		// see stabilizerLocked.
		Locked: stabilizerLocked,
	},
	{
		// UNAUTHENTICATED when on - see AileronUIEnabled's doc comment.
		Key: "system.aileron_ui_enabled", Label: "Aileron UI (unauthenticated)",
		Get: func(c *Config) string {
			if c.System.AileronUIEnabled {
				return "on"
			}
			return "off"
		},
		Options: func(c *Config, versions VersionCache) []string { return []string{"on", "off"} },
		Set: func(c *Config, v string) error {
			c.System.AileronUIEnabled = v == "on"
			return nil
		},
		// Same reasoning as versions.aileron above - both Aileron fields
		// lock together once a "stabilizer" chart is detected.
		Locked: stabilizerLocked,
	},
	{
		Key: "storage.engine", Label: "Storage engine", Advanced: true,
		Get:     func(c *Config) string { return c.Storage.Engine },
		Options: func(c *Config, versions VersionCache) []string { return []string{"openebs", "longhorn", "rook-ceph"} },
		Set: func(c *Config, v string) error {
			c.Storage.Engine = v
			return nil
		},
		// Locks once the disk is prepared for an engine (see storage.go) -
		// switching engines on a node that may hold VM data isn't a
		// supported migration.
		Locked: func(c *Config, versions VersionCache) (bool, string) {
			if storage.StorageEngineApplied() {
				return true, "locked — disk already prepared for " + c.Storage.Engine + ", reinstall OS to change"
			}
			return false, ""
		},
	},
}
