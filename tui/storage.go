package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// storageMarkerPath records which storage engine has actually had the disk
// prepared for it (see prepareStorageDevice). Its presence is what the
// storage.engine settingField's locked func checks - once it exists, the
// Settings screen won't let the operator pick a different engine, since
// switching means reformatting/re-provisioning a disk that may already
// hold VM data.
const storageMarkerPath = "/var/lib/ruddervirt/storage-engine.applied"

const (
	// longhornDataPath is where Longhorn stores replica data as ordinary
	// files - unlike Ceph, it needs a mounted filesystem, not a raw device.
	longhornDataPath  = "/var/lib/longhorn"
	longhornMountUnit = "/etc/systemd/system/var-lib-longhorn.mount"

	// openebsVGName/openebsThinPoolLV back the OpenEBS LVM LocalPV CSI
	// driver's StorageClass (manifests/openebs/base/storage-class.yaml
	// references this VG name). A thin pool (rather than plain/thick LVM)
	// is what makes CSI snapshots practical - thick LVM snapshots need
	// space reserved up front per snapshot and don't scale past a handful.
	openebsVGName     = "ruddervirt-vg"
	openebsThinPoolLV = "thinpool"
)

const (
	findmntBin  = "/usr/bin/findmnt"
	lsblkBin    = "/usr/bin/lsblk"
	blkidBin    = "/usr/sbin/blkid"
	mkfsExt4Bin = "/usr/sbin/mkfs.ext4"
	vgsBin      = "/usr/sbin/vgs"
	pvcreateBin = "/usr/sbin/pvcreate"
	vgcreateBin = "/usr/sbin/vgcreate"
	lvcreateBin = "/usr/sbin/lvcreate"
)

// bootDiskPartitionNumber is the boot disk's leftover partition (see
// server.bu's storage.disks stanza: partition 4 is the 50GB root, partition
// 5 is "the rest of the disk", left unformatted). Rook-Ceph auto-discovers
// it as a raw block device; Longhorn and OpenEBS need it prepared
// differently, see prepareStorageDevice.
const bootDiskPartitionNumber = "5"

// rootDiskDevice finds the whole-disk device (e.g. /dev/vda, /dev/nvme0n1)
// backing the running system, by resolving /sysroot's mount source (the
// ostree real partition mount - reliable even under composefs/unified-core,
// where "/" itself is an overlay rather than a plain bind mount) down to its
// parent disk via lsblk.
//
// /dev/disk/by-id/coreos-boot-disk (used at Ignition time in server.bu to
// address "whatever disk this booted from" for partitioning) is a
// config-time-only alias Ignition resolves internally - it's never created
// as an actual symlink in the running system, so it can't be read here
// ("lstat /dev/disk/by-id/coreos-boot-disk: no such file or directory").
// Deriving the disk from the live mount table instead works regardless of
// by-id link availability, which also varies by disk transport (e.g.
// virtio-blk disks like /dev/vda have no serial for udev to build one
// from).
func rootDiskDevice() (string, error) {
	out, err := runPrivileged(findmntBin, "-n", "-o", "SOURCE", "/sysroot").Output()
	if err != nil {
		return "", fmt.Errorf("finding /sysroot's device: %w", err)
	}
	rootPart := strings.TrimSpace(string(out))
	if rootPart == "" {
		return "", fmt.Errorf("findmnt returned no source device for /sysroot")
	}

	out, err = runPrivileged(lsblkBin, "-no", "PKNAME", rootPart).Output()
	if err != nil {
		return "", fmt.Errorf("finding parent disk of %s: %w", rootPart, err)
	}
	pkname := strings.TrimSpace(string(out))
	if pkname == "" {
		return "", fmt.Errorf("lsblk returned no parent disk for %s", rootPart)
	}
	return "/dev/" + pkname, nil
}

// storagePartitionPath resolves the device path for bootDiskPartitionNumber
// on the disk the system actually booted from.
func storagePartitionPath() (string, error) {
	disk, err := rootDiskDevice()
	if err != nil {
		return "", fmt.Errorf("determining boot disk: %w", err)
	}
	return disk + partitionSuffix(disk) + bootDiskPartitionNumber, nil
}

// partitionSuffix returns "p" if disk's device name ends in a digit (e.g.
// nvme0n1, mmcblk0 - the "p" disambiguates the partition number from the
// disk name's own trailing digit), or "" otherwise (e.g. sda, vda).
func partitionSuffix(disk string) string {
	base := filepath.Base(disk)
	if base == "" {
		return ""
	}
	last := base[len(base)-1]
	if last >= '0' && last <= '9' {
		return "p"
	}
	return ""
}

// storageEngineApplied reports whether the storage device has already been
// prepared for some engine - used by the storage.engine settingField to
// lock itself.
func storageEngineApplied() bool {
	_, err := appliedStorageEngine()
	return err == nil
}

// appliedStorageEngine returns the engine name recorded in storageMarkerPath,
// or an error if the disk hasn't been prepared for any engine yet.
func appliedStorageEngine() (string, error) {
	data, err := os.ReadFile(storageMarkerPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// markStorageEngineApplied records engine as the one the disk is now
// prepared for. /var/lib/ruddervirt is root-owned like configPath's parent,
// so this goes through the same temp-file + privileged-move path.
func markStorageEngineApplied(engine string) error {
	return writePrivileged(storageMarkerPath, []byte(engine))
}

// prepareStorageDevice prepares storagePartitionPath for cfg.Storage.Engine
// and is called as its own install step, before prepareK3sStep applies the
// engine's Helm chart. It's the backend half of the engine lock: the
// Settings screen already keeps the operator from picking a different
// engine once storageMarkerPath exists, but this guard defends against a
// hand-edited config file bypassing that.
func prepareStorageDevice(cfg Config, ch chan<- tea.Msg) error {
	if applied, err := appliedStorageEngine(); err == nil {
		if applied == cfg.Storage.Engine {
			ch <- stepOutputMsg(fmt.Sprintf("Storage device already prepared for %s", cfg.Storage.Engine))
			return nil
		}
		return fmt.Errorf("storage engine is locked to %q (disk already prepared) - reinstall the OS to switch to %q", applied, cfg.Storage.Engine)
	}

	switch cfg.Storage.Engine {
	case "rook-ceph":
		// Rook-Ceph discovers the raw partition itself (see the
		// useAllDevices patch in manifests/rook-ceph/overlays/ruddervirt) -
		// no need to resolve its device path here at all.
		ch <- stepOutputMsg("Rook-Ceph uses the raw device directly - nothing to prepare")
	case "longhorn":
		device, err := storagePartitionPath()
		if err != nil {
			return err
		}
		if err := prepareLonghornDevice(ch, device); err != nil {
			return err
		}
	case "openebs":
		device, err := storagePartitionPath()
		if err != nil {
			return err
		}
		if err := prepareOpenEBSDevice(ch, device); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown storage engine %q", cfg.Storage.Engine)
	}

	return markStorageEngineApplied(cfg.Storage.Engine)
}

// prepareStorageStep adapts prepareStorageDevice to the installStep shape.
func prepareStorageStep(cfg Config, ch chan<- tea.Msg) {
	const label = "Preparing storage device"
	ch <- stepDoneMsg{label: label, err: prepareStorageDevice(cfg, ch)}
}

// planStorageDevice previews prepareStorageDevice, reusing the same
// appliedStorageEngine() marker check. Reports a blocked/error line rather
// than a false "will prepare disk for X" if a hand-edited config disagrees
// with the disk's locked engine - the Settings screen's storage.engine
// field already prevents choosing a different engine once the marker
// exists (see its locked func in config.go), but the preview should stay
// honest about what the real run would do regardless of how cfg got here.
func planStorageDevice(cfg Config) string {
	if applied, err := appliedStorageEngine(); err == nil {
		if applied == cfg.Storage.Engine {
			return fmt.Sprintf("skip - storage device already prepared for %s", cfg.Storage.Engine)
		}
		return fmt.Sprintf("BLOCKED - disk locked to %s (reinstall OS to switch to %s)", applied, cfg.Storage.Engine)
	}
	return fmt.Sprintf("will prepare disk for %s", cfg.Storage.Engine)
}

// prepareLonghornDevice formats device (if it doesn't already hold a
// filesystem) and mounts it at longhornDataPath.
func prepareLonghornDevice(ch chan<- tea.Msg, device string) error {
	if runPrivileged(findmntBin, "--noheadings", longhornDataPath).Run() == nil {
		ch <- stepOutputMsg(fmt.Sprintf("%s already mounted", longhornDataPath))
		return nil
	}

	if runPrivileged(blkidBin, device).Run() != nil {
		ch <- stepOutputMsg(fmt.Sprintf("Formatting %s as ext4...", device))
		if err := runStreamed(ch, mkfsExt4Bin, "-F", device); err != nil {
			return err
		}
	} else {
		ch <- stepOutputMsg(fmt.Sprintf("%s already has a filesystem, skipping mkfs", device))
	}

	if out, err := runPrivileged("/usr/bin/mkdir", "-p", longhornDataPath).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}

	unit := fmt.Sprintf(`[Unit]
Description=Longhorn data volume

[Mount]
What=%s
Where=%s
Type=ext4

[Install]
WantedBy=local-fs.target
`, device, longhornDataPath)
	if err := writePrivileged(longhornMountUnit, []byte(unit)); err != nil {
		return err
	}

	if out, err := runPrivileged("/usr/bin/systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	ch <- stepOutputMsg(fmt.Sprintf("Mounting %s...", longhornDataPath))
	return runStreamed(ch, "/usr/bin/systemctl", "enable", "--now", "var-lib-longhorn.mount")
}

// prepareOpenEBSDevice turns device into an LVM thin-pool volume group,
// which the LVM LocalPV CSI driver provisions volumes from.
func prepareOpenEBSDevice(ch chan<- tea.Msg, device string) error {
	if runPrivileged(vgsBin, "--noheadings", "-o", "vg_name", openebsVGName).Run() == nil {
		ch <- stepOutputMsg(fmt.Sprintf("Volume group %s already exists", openebsVGName))
		return nil
	}

	ch <- stepOutputMsg(fmt.Sprintf("Creating physical volume on %s...", device))
	if err := runStreamed(ch, pvcreateBin, "-f", device); err != nil {
		return err
	}
	ch <- stepOutputMsg(fmt.Sprintf("Creating volume group %s...", openebsVGName))
	if err := runStreamed(ch, vgcreateBin, openebsVGName, device); err != nil {
		return err
	}
	ch <- stepOutputMsg(fmt.Sprintf("Creating thin pool %s/%s...", openebsVGName, openebsThinPoolLV))
	return runStreamed(ch, lvcreateBin, "--type", "thin-pool", "-l", "95%VG", "-n", openebsThinPoolLV, openebsVGName)
}
