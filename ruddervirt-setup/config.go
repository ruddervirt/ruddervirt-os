// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"
)

const (
	configPath = "/etc/ruddervirt/ruddervirt-setup.yaml"
	// zincatiConfigPath matches the drop-in server.bu provisions at image
	// build time, so this TUI's writes and Ignition's baked-in default both
	// target the same single source of truth.
	zincatiConfigPath = "/etc/zincati/config.d/99-ruddervirt-updates.toml"
)

type NetworkConfig struct {
	// InterfaceName is the internet-facing interface (has a default route),
	// e.g. behind a NAT/firewall - auto-detected if left empty, see
	// detectDefaultInterface and resolveNetworkForInstall in network.go.
	InterfaceName string `yaml:"interface_name"`
	// Addressing is "dhcp" or "static". The fields below only apply, and
	// are only required, when static - see applyNetworkConfig in
	// network.go, which is what actually configures InterfaceName via
	// nmcli whenever any of these are saved.
	Addressing string   `yaml:"addressing"`
	StaticIP   string   `yaml:"static_ip"`
	Prefix     int      `yaml:"prefix"`
	Gateway    string   `yaml:"gateway"`
	DNSServers []string `yaml:"dns_servers"`
	PodCIDR    string   `yaml:"pod_cidr"`
	SvcCIDR    string   `yaml:"svc_cidr"`
}

type SystemConfig struct {
	AutoUpdate bool `yaml:"auto_update"`
	// PasswordChanged records that the admin account's login password has
	// been confirmed changed from the well-known default baked into
	// server.bu - see checkPasswordChangedCmd in password.go. Once true,
	// entering "configure" skips the check (and its /etc/shadow read)
	// instead of re-verifying every time.
	PasswordChanged bool `yaml:"password_changed"`
	// HostnameDeclared records that the operator has confirmed/set the
	// system hostname away from (or explicitly kept) the well-known default
	// baked into server.bu - see hostnameIsDefault in hostname.go. Once
	// true, entering "configure" skips re-checking the live hostname.
	HostnameDeclared bool `yaml:"hostname_declared"`
	// AileronUIEnabled toggles the aileronUI.enabled Helm value (see
	// applyAileron in aileron.go and manifests/aileron-helmchart.yaml).
	// Defaults to true (matching the chart's own default), but the chart's
	// own values.yaml flags the UI as UNAUTHENTICATED and able to
	// create/delete VMs and open any console - worth letting an operator
	// turn off on an untrusted network.
	AileronUIEnabled bool `yaml:"aileron_ui_enabled"`
}

type VersionsConfig struct {
	K3s      string `yaml:"k3s"`
	KubeVirt string `yaml:"kubevirt"`
	CDI      string `yaml:"cdi"`
	Aileron  string `yaml:"aileron"`
}

// StorageConfig.Engine picks which storage layer prepareStorageDevice and
// prepareK3sStep set up: "rook-ceph", "longhorn", or "openebs" (LVM
// LocalPV). See storage.go - once the disk has actually been prepared for
// an engine, that choice locks (see the storage.engine settingField's
// locked func below), since switching engines on a node that already has
// VM data on it isn't a supported migration.
type StorageConfig struct {
	Engine string `yaml:"engine"`
}

// StabilizerConfig holds only the non-secret values needed to (re-)apply the
// stabilizer HelmChart - e.g. on a future version bump. Zone and NatsURL are
// plain orchestration config, not secrets. NATS credentials and the Nebula
// mesh identity are NEVER stored here, or anywhere else in Config - see
// pendingStabilizer* in stabilizer.go.
type StabilizerConfig struct {
	Zone    string `yaml:"zone"`
	NatsURL string `yaml:"nats_url"`
	Version string `yaml:"version"`
}

type Config struct {
	Network    NetworkConfig    `yaml:"network"`
	System     SystemConfig     `yaml:"system"`
	Versions   VersionsConfig   `yaml:"versions"`
	Storage    StorageConfig    `yaml:"storage"`
	Stabilizer StabilizerConfig `yaml:"stabilizer"`
}

func defaultConfig() Config {
	return Config{
		Network: NetworkConfig{
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
			K3s:      defaultK3sVersion,
			KubeVirt: defaultKubeVirtVersion,
			CDI:      defaultCDIVersion,
			Aileron:  defaultAileronVersion,
		},
		Storage: StorageConfig{
			Engine: "openebs",
		},
		Stabilizer: StabilizerConfig{
			Version: defaultStabilizerVersion,
		},
	}
}

// loadConfig reads path and overlays it onto the defaults. A missing file is
// not an error: it just means the operator hasn't changed anything yet, so
// the in-memory defaults are used as-is. The file is only ever written when
// a setting is actually saved (see saveConfig), since /etc/ruddervirt is
// root-owned and writing to it requires the same sudo escalation as the
// install steps - doing that at startup, before the TUI has even painted,
// would surprise the operator with an unexplained password prompt.
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return defaultConfig(), err
	}
	return cfg, nil
}

// configSaved reports whether configPath exists - i.e. the operator has
// saved Settings at least once via "configure". Used to gate the home
// screen's "Services" summary: probing systemctl/kubectl is pointless (and
// misleading) on a system nothing has ever been configured on. A var (like
// DefaultRunner) rather than a plain func so tests can swap it instead of
// depending on the real /etc/ruddervirt path.
var configSaved = func() bool {
	_, err := os.Stat(configPath)
	return err == nil
}

// saveConfig writes cfg to path. /etc/ruddervirt is root-owned, so the file
// is written to a temp path first and moved into place with runPrivileged,
// mirroring how installSteps writes the k3s systemd unit.
func saveConfig(cfg Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return writePrivileged(path, data)
}

// writeZincatiConfig switches Fedora CoreOS's update agent between its
// periodic Saturday-midnight window (the server.bu default) and fully
// disabled, by rewriting the one Zincati drop-in this project owns.
func writeZincatiConfig(autoUpdate bool, path string) error {
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
	return writePrivileged(path, []byte(content))
}

// writePrivileged writes data to path via a temp file + privileged move, so
// callers never need to hold write access to path's parent directory
// themselves.
func writePrivileged(path string, data []byte) error {
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

	if out, err := runPrivileged("/usr/bin/mkdir", "-p", filepath.Dir(path)).CombinedOutput(); err != nil {
		return wrapCmdErr(out, err)
	}
	if out, err := runPrivileged("/usr/bin/mv", tmpPath, path).CombinedOutput(); err != nil {
		return wrapCmdErr(out, err)
	}
	return nil
}

// versionCache carries the model's in-flight k3s/aileron version-fetch
// results, plus other live-detected cluster state, into settingField.options
// and settingField.locked - the model is the single source of truth for
// these, so those funcs read them via this parameter instead of a
// package-level global.
type versionCache struct {
	K3s     []string
	Aileron []string
	// StabilizerDetected is true when a HelmChart object named "stabilizer"
	// exists on the cluster - see stabilizerChartPresent (aileron.go). Locks
	// the Aileron settingFields below.
	StabilizerDetected bool
}

// settingField binds one editable Settings-screen row to the Config field it
// reads and writes. Text fields use get/set with validation; boolean fields
// use toggle instead and flip immediately on Enter with no text-edit step.
type settingField struct {
	key   string
	label string
	get   func(cfg *Config) string
	set   func(cfg *Config, val string) error
	// options, if non-nil, marks this as a select field: Enter opens a
	// picker listing its return value instead of a free-text edit box.
	// Computed fresh each time (not cached on the field) since the choices
	// can depend on live state (detected NICs, fetched k3s releases).
	options      func(cfg *Config, versions versionCache) []string
	advanced     bool // hidden behind the Settings screen's "Advanced settings" toggle
	staticOnly   bool // only shown (nested under "IP addressing") when Network.Addressing == "static"
	networkSetup bool // hidden behind the "Local physical network setup" toggle
	// updateScreen moves the field out of Settings entirely and onto the
	// Update screen instead (see updateVersionsRows in view.go) - for the
	// four component-version fields, so "configure" only has the
	// straightforward settings and every upgrade lives in one place.
	updateScreen bool
	// locked, if non-nil, can make the field temporarily read-only: Enter
	// becomes a no-op and the row shows the returned reason instead of
	// letting the operator edit/pick a new value. nil (the default for
	// every existing field) always means editable.
	locked func(cfg *Config, versions versionCache) (bool, string)
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

// stabilizerLocked backs both Aileron settingFields' locked funcs: if
// aileron is owned by a separate "stabilizer" Helm chart on this cluster
// (see stabilizerChartPresent, aileron.go), ruddervirt-setup must not touch
// it, so both fields report themselves as managed by stabilizer instead of
// letting the operator edit or pick a new value here. prepareK3sStep (k3s.go)
// re-checks this at apply time too, so it isn't just the interactive picker
// that's guarded.
func stabilizerLocked(c *Config, versions versionCache) (bool, string) {
	if versions.StabilizerDetected {
		return true, "managed by stabilizer"
	}
	return false, ""
}

var settingFields = []settingField{
	{
		key: "network.interface_name", label: "Local network-interface (internet-facing)", networkSetup: true,
		get: func(c *Config) string { return c.Network.InterfaceName },
		options: func(c *Config, versions versionCache) []string {
			ifaces, _ := listNetworkInterfaces()
			return ifaces
		},
		set: func(c *Config, v string) error {
			c.Network.InterfaceName = v
			return nil
		},
	},
	{
		key: "network.addressing", label: "IP addressing", networkSetup: true,
		get:     func(c *Config) string { return c.Network.Addressing },
		options: func(c *Config, versions versionCache) []string { return []string{"dhcp", "static"} },
		set: func(c *Config, v string) error {
			c.Network.Addressing = v
			return nil
		},
	},
	{
		// Only reachable in the UI when addressing is static (see
		// settingsRows in setup.go), so validation can just always be
		// strict - no need for a lenient dhcp-mode branch.
		key: "network.static_ip", label: "Static IP", staticOnly: true,
		get: func(c *Config) string { return c.Network.StaticIP },
		set: func(c *Config, v string) error {
			s, err := parseRequiredIP("static IP", v)
			if err != nil {
				return err
			}
			c.Network.StaticIP = s
			return nil
		},
	},
	{
		key: "network.prefix", label: "Prefix length", staticOnly: true,
		get: func(c *Config) string { return strconv.Itoa(c.Network.Prefix) },
		set: func(c *Config, v string) error {
			n, err := parsePrefixLen(v)
			if err != nil {
				return err
			}
			c.Network.Prefix = n
			return nil
		},
	},
	{
		key: "network.gateway", label: "Gateway", staticOnly: true,
		get: func(c *Config) string { return c.Network.Gateway },
		set: func(c *Config, v string) error {
			s, err := parseRequiredIP("gateway", v)
			if err != nil {
				return err
			}
			c.Network.Gateway = s
			return nil
		},
	},
	{
		key: "network.dns_servers", label: "DNS servers (comma-separated)", staticOnly: true,
		get: func(c *Config) string { return strings.Join(c.Network.DNSServers, ", ") },
		set: func(c *Config, v string) error {
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
		key: "network.pod_cidr", label: "Pod CIDR", advanced: true,
		get: func(c *Config) string { return c.Network.PodCIDR },
		set: func(c *Config, v string) error {
			s, err := parseCIDRField("pod CIDR", v)
			if err != nil {
				return err
			}
			c.Network.PodCIDR = s
			return nil
		},
	},
	{
		key: "network.svc_cidr", label: "Service CIDR", advanced: true,
		get: func(c *Config) string { return c.Network.SvcCIDR },
		set: func(c *Config, v string) error {
			s, err := parseCIDRField("service CIDR", v)
			if err != nil {
				return err
			}
			c.Network.SvcCIDR = s
			return nil
		},
	},
	{
		key: "system.auto_update", label: "Automatic updates",
		get: func(c *Config) string {
			if c.System.AutoUpdate {
				return "on"
			}
			return "off"
		},
		options: func(c *Config, versions versionCache) []string { return []string{"on", "off"} },
		set: func(c *Config, v string) error {
			c.System.AutoUpdate = v == "on"
			return nil
		},
	},
	{
		key: "versions.k3s", label: "k3s version", updateScreen: true,
		get: func(c *Config) string { return c.Versions.K3s },
		options: func(c *Config, versions versionCache) []string {
			// k3s doesn't support downgrades, so never offer anything
			// older than whatever's currently configured (install always
			// downloads exactly this value, so it's the same thing as
			// "the installed version" in steady state - and unlike
			// querying a real /usr/local/bin/k3s binary, it's always
			// available, even before the very first install has run).
			var available []string
			for _, v := range versions.K3s {
				cmp, ok := compareK3sVersions(v, c.Versions.K3s)
				if ok && cmp < 0 {
					continue
				}
				available = append(available, v)
			}
			return available
		},
		set: func(c *Config, v string) error {
			c.Versions.K3s = v
			return nil
		},
	},
	{
		key: "versions.kubevirt", label: "KubeVirt version", updateScreen: true,
		get: func(c *Config) string { return c.Versions.KubeVirt },
		options: func(c *Config, versions versionCache) []string {
			return supportedVersionsAtLeast(supportedVersions.KubeVirt, c.Versions.KubeVirt)
		},
		set: func(c *Config, v string) error {
			c.Versions.KubeVirt = v
			return nil
		},
	},
	{
		key: "versions.cdi", label: "CDI version", updateScreen: true,
		get: func(c *Config) string { return c.Versions.CDI },
		options: func(c *Config, versions versionCache) []string {
			return supportedVersionsAtLeast(supportedVersions.CDI, c.Versions.CDI)
		},
		set: func(c *Config, v string) error {
			c.Versions.CDI = v
			return nil
		},
	},
	{
		key: "versions.aileron", label: "Aileron version", updateScreen: true,
		get: func(c *Config) string { return c.Versions.Aileron },
		options: func(c *Config, versions versionCache) []string {
			// Aileron ships too frequently for a hand-curated list (unlike
			// KubeVirt/CDI) - fetch live from GitHub, same reasoning as
			// versions.k3s: never offer a downgrade from what's configured.
			var available []string
			for _, v := range versions.Aileron {
				cmp, ok := compareSemver(v, c.Versions.Aileron)
				if ok && cmp < 0 {
					continue
				}
				available = append(available, v)
			}
			sort.SliceStable(available, func(i, j int) bool {
				cmp, ok := compareSemver(available[i], available[j])
				return ok && cmp > 0
			})
			return available
		},
		set: func(c *Config, v string) error {
			c.Versions.Aileron = v
			return nil
		},
		// Locks whenever a HelmChart named "stabilizer" is on the cluster -
		// see stabilizerLocked.
		locked: stabilizerLocked,
	},
	{
		// UNAUTHENTICATED when on - see AileronUIEnabled's doc comment.
		key: "system.aileron_ui_enabled", label: "Aileron UI (unauthenticated)",
		get: func(c *Config) string {
			if c.System.AileronUIEnabled {
				return "on"
			}
			return "off"
		},
		options: func(c *Config, versions versionCache) []string { return []string{"on", "off"} },
		set: func(c *Config, v string) error {
			c.System.AileronUIEnabled = v == "on"
			return nil
		},
		// Same reasoning as versions.aileron above - both Aileron fields
		// lock together once a "stabilizer" chart is detected.
		locked: stabilizerLocked,
	},
	{
		key: "storage.engine", label: "Storage engine", advanced: true,
		get:     func(c *Config) string { return c.Storage.Engine },
		options: func(c *Config, versions versionCache) []string { return []string{"openebs", "longhorn", "rook-ceph"} },
		set: func(c *Config, v string) error {
			c.Storage.Engine = v
			return nil
		},
		// Locks the instant the disk has actually been prepared for an
		// engine (see storage.go) - switching engines on a node that
		// may already hold VM data isn't a supported migration, so the
		// operator can't even open the picker once that's happened.
		locked: func(c *Config, versions versionCache) (bool, string) {
			if storageEngineApplied() {
				return true, "locked — disk already prepared for " + c.Storage.Engine + ", reinstall OS to change"
			}
			return false, ""
		},
	},
}

func saveSettingCmd(cfg Config) tea.Cmd {
	return func() tea.Msg {
		return settingsSavedMsg{err: saveConfig(cfg, configPath)}
	}
}
