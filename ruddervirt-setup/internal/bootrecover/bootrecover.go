// SPDX-License-Identifier: GPL-3.0-only

// Package bootrecover owns the post-reboot cluster recovery assets:
//
//   - ruddervirt-boot-recover.sh: the oneshot script that reaps shutdown
//     corpses and waits for kube-ovn to converge (embedded, single source of
//     truth - no server.bu copy)
//   - the ruddervirt-boot-recover.service unit that runs it on every boot,
//     once k3s is installed (guarded by ConditionPathExists)
//   - the kubelet graceful-shutdown config (kubelet.config, shutdownGrace-
//     Period 120s) that lets kubelet drain pod statuses before the k3s unit
//     is SIGKILLed - the counterpart to k3sUnitContent's TimeoutStopSec=180
//     in install_steps.go
//   - the systemd softdog watchdog drop-in (RuntimeWatchdogSec=60 + the
//     iTCO_wdt/softdog modules), so a hung host reboots on its own and
//     boot-recover brings the cluster back
//
// These are delivered by ruddervirt-setup itself rather than server.bu's
// first-boot Ignition: Ignition runs exactly once, so nodes installed before
// this feature shipped would never receive it. Writing them from a setup
// step on every run keeps every node in the fleet on the same assets, with
// server.bu only seeding first-boot bootstrap necessities.
package bootrecover

import (
	_ "embed"
	"fmt"
	"os"
)

//go:embed ruddervirt-boot-recover.sh
var recoveryScript []byte

// ServiceUnit is the oneshot that runs on boot after k3s stabilizes. It is
// enabled via systemctl by install_steps.go's "Writing boot-recover assets"
// step. Inert before a node has been configured (ConditionPathExists), so the
// pre-k3s state of a fresh ISO is unaffected.
const ServiceUnit = `[Unit]
Description=Recover cluster state after boot (reap shutdown corpses, wait for CNI)
Wants=k3s.service
After=k3s.service
ConditionPathExists=/etc/rancher/k3s/k3s.yaml

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/ruddervirt-boot-recover

[Install]
WantedBy=multi-user.target
`

// KubeletConfig gives kubelet up to 120s to mark its pods terminating
// (statuses flushed to the API server) before the local k3s process is
// SIGKILLed (k3sUnitContent's TimeoutStopSec=180 in install_steps.go).
// Referenced from k3s-config.yaml's kubelet-arg config=/etc/kubelet.config.
const KubeletConfig = `apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
shutdownGracePeriod: 120s
shutdownGracePeriodCriticalPods: 10s
`

// WatchdogSystemdConf arms the systemd software watchdog. RuntimeWatchdogSec
// is read at systemd start, so it only takes effect on the next reboot (the
// modules-load.d entry below makes sure a device exists to pet).
const WatchdogSystemdConf = `[Manager]
RuntimeWatchdogSec=60
`

// WatchdogModulesLoad loads iTCO_wdt (BMC-equipped bare metal) and softdog
// (anything without a real watchdog device, e.g. VMs) so systemd always has
// /dev/watchdog to pet. Best-effort: module absence just means no watchdog.
const WatchdogModulesLoad = `iTCO_wdt
softdog
`

// Asset is one file WriteAssets is responsible for, with its desired mode.
type Asset struct {
	Path string
	Mode os.FileMode
	Data []byte
}

// Assets lists every file the setup step writes, in write order.
func Assets() []Asset {
	return []Asset{
		{
			Path: "/usr/local/bin/ruddervirt-boot-recover",
			Mode: 0o755,
			Data: recoveryScript,
		},
		{
			Path: "/etc/systemd/system/ruddervirt-boot-recover.service",
			Mode: 0o644,
			Data: []byte(ServiceUnit),
		},
		{
			Path: "/etc/kubelet.config",
			Mode: 0o644,
			Data: []byte(KubeletConfig),
		},
		{
			Path: "/etc/systemd/system.conf.d/10-ruddervirt-watchdog.conf",
			Mode: 0o644,
			Data: []byte(WatchdogSystemdConf),
		},
		{
			Path: "/etc/modules-load.d/ruddervirt-watchdog.conf",
			Mode: 0o644,
			Data: []byte(WatchdogModulesLoad),
		},
	}
}

// WriteAssets writes every asset via write (the caller's privileged
// temp-file write, e.g. internal/config.WritePrivileged), chmodding any
// asset whose mode differs from the write primitive's default 0644. Stops at
// the first failing asset so the caller can error the step loudly instead of
// leaving a half-synced filesystem.
func WriteAssets(write func(path string, data []byte) error, chmod func(path string, mode os.FileMode) error) error {
	for _, a := range Assets() {
		path, data := a.Path, a.Data
		if err := write(path, data); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		if a.Mode != 0 && a.Mode != 0o644 {
			if err := chmod(path, a.Mode); err != nil {
				return fmt.Errorf("chmod %s: %w", path, err)
			}
		}
	}
	return nil
}
