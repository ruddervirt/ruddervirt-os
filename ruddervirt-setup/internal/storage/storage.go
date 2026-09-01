// SPDX-License-Identifier: GPL-3.0-only

package storage

import (
	"fmt"
	"path/filepath"
	"strings"

	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/marker"
)

// storageMarkerPath records which storage engine has had the disk prepared
// for it (see PrepareStorageDevice). Once it exists, Settings won't let the
// operator switch engines, since that means reformatting a disk that may
// already hold VM data.
const storageMarkerPath = "/var/lib/ruddervirt/storage-engine.applied"

const (
	// longhornDataPath is where Longhorn stores replica data as ordinary
	// files - unlike Ceph, it needs a mounted filesystem, not a raw device.
	longhornDataPath  = "/var/lib/longhorn"
	longhornMountUnit = "/etc/systemd/system/var-lib-longhorn.mount"

	// OpenebsVGName/OpenebsThinPoolLV back the OpenEBS LVM LocalPV CSI
	// driver's StorageClass (manifests/openebs/base/storage-class.yaml
	// references this VG name). A thin pool, not plain/thick LVM, is what
	// makes CSI snapshots practical at scale.
	//
	// OpenebsThinPoolLV's name isn't arbitrary: the LVM-LocalPV driver only
	// reuses an existing pool named exactly "<volgroup>_thinpool" - any
	// other name makes it silently ignore this 95%-of-VG pool and
	// thin-provision volumes into its own tiny auto-created one instead,
	// which then runs out of space under real load, surfacing as ext4 I/O
	// errors rather than an obvious provisioning failure.
	OpenebsVGName     = "ruddervirt-vg"
	OpenebsThinPoolLV = OpenebsVGName + "_thinpool"
)

const (
	findmntBin  = "/usr/bin/findmnt"
	lsblkBin    = "/usr/bin/lsblk"
	blkidBin    = "/usr/sbin/blkid"
	mkfsExt4Bin = "/usr/sbin/mkfs.ext4"
	vgsBin      = "/usr/sbin/vgs"
	// LvsBin is exported for status.go's openebsVGCapacity.
	LvsBin      = "/usr/sbin/lvs"
	pvcreateBin = "/usr/sbin/pvcreate"
	vgcreateBin = "/usr/sbin/vgcreate"
	lvcreateBin = "/usr/sbin/lvcreate"
)

// bootDiskPartitionNumber is the boot disk's leftover partition (see
// server.bu's storage.disks stanza: partition 4 is the 50GB root, partition
// 5 is "the rest of the disk", left unformatted). Rook-Ceph auto-discovers
// it as a raw block device; Longhorn and OpenEBS need it prepared, see
// PrepareStorageDevice.
const bootDiskPartitionNumber = "5"

// rootDiskDevice finds the whole-disk device (e.g. /dev/vda, /dev/nvme0n1)
// backing the running system, by resolving /sysroot's mount source (the
// ostree real partition mount - reliable even under composefs/unified-core,
// where "/" is an overlay rather than a plain bind mount) down to its
// parent disk via lsblk.
//
// /dev/disk/by-id/coreos-boot-disk (used at Ignition time in server.bu) is
// a config-time-only alias that's never created as an actual symlink in the
// running system, so it can't be read here. Deriving the disk from the live
// mount table instead works regardless of by-id link availability, which
// also varies by disk transport (e.g. virtio-blk disks have no serial for
// udev to build one from).
func rootDiskDevice() (string, error) {
	out, err := exec.RunPrivileged(findmntBin, "-n", "-o", "SOURCE", "/sysroot").Output()
	if err != nil {
		return "", fmt.Errorf("finding /sysroot's device: %w", err)
	}
	rootPart := strings.TrimSpace(string(out))
	if rootPart == "" {
		return "", fmt.Errorf("findmnt returned no source device for /sysroot")
	}

	out, err = exec.RunPrivileged(lsblkBin, "-no", "PKNAME", rootPart).Output()
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
// nvme0n1, mmcblk0, to disambiguate the partition number from the disk
// name's trailing digit), or "" otherwise (e.g. sda, vda).
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

// StorageEngineApplied reports whether the storage device has already been
// prepared for some engine - used by the storage.engine settingField to
// lock itself.
func StorageEngineApplied() bool {
	_, err := AppliedStorageEngine()
	return err == nil
}

// AppliedStorageEngine returns the engine name recorded in storageMarkerPath,
// or an error if the disk hasn't been prepared for any engine yet.
func AppliedStorageEngine() (string, error) {
	return marker.Read(storageMarkerPath)
}

// markStorageEngineApplied records engine as the one the disk is prepared
// for. write is the caller's privileged file-write primitive, since this
// package has no access to package main.
func markStorageEngineApplied(write func(path string, data []byte) error, engine string) error {
	return marker.Write(write, storageMarkerPath, engine)
}

// PrepareStorageDevice prepares storagePartitionPath for engine, run as its
// own install step before the engine's Helm chart is applied. It's the
// backend half of the engine lock: Settings already blocks picking a
// different engine once storageMarkerPath exists, but this guard defends
// against a hand-edited config file bypassing that. wrap adapts a progress
// line into the caller's tea.Msg type; write is the caller's privileged
// file-write primitive.
func PrepareStorageDevice(engine string, ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, write func(path string, data []byte) error) error {
	if applied, err := AppliedStorageEngine(); err == nil {
		if applied == engine {
			ch <- wrap(fmt.Sprintf("Storage device already prepared for %s", engine))
			return nil
		}
		return fmt.Errorf("storage engine is locked to %q (disk already prepared) - reinstall the OS to switch to %q", applied, engine)
	}

	switch engine {
	case "rook-ceph":
		// Rook-Ceph discovers the raw partition itself (see the
		// useAllDevices patch in manifests/rook-ceph/overlays/ruddervirt).
		ch <- wrap("Rook-Ceph uses the raw device directly - nothing to prepare")
	case "longhorn":
		device, err := storagePartitionPath()
		if err != nil {
			return err
		}
		if err := prepareLonghornDevice(ch, wrap, write, device); err != nil {
			return err
		}
	case "openebs":
		device, err := storagePartitionPath()
		if err != nil {
			return err
		}
		if err := prepareOpenEBSDevice(ch, wrap, device); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown storage engine %q", engine)
	}

	return markStorageEngineApplied(write, engine)
}

// prepareLonghornDevice formats device (if it doesn't already hold a
// filesystem) and mounts it at longhornDataPath.
func prepareLonghornDevice(ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, write func(path string, data []byte) error, device string) error {
	if exec.RunPrivileged(findmntBin, "--noheadings", longhornDataPath).Run() == nil {
		ch <- wrap(fmt.Sprintf("%s already mounted", longhornDataPath))
		return nil
	}

	// blkid's exit code isn't reliable here: it exits 0 on a bare,
	// unformatted device as long as it can read the partition table. -o
	// value -s TYPE narrows to the filesystem-type tag, so empty output is
	// the real "no filesystem" signal.
	fsType, _ := exec.RunPrivileged(blkidBin, "-o", "value", "-s", "TYPE", device).Output()
	if strings.TrimSpace(string(fsType)) == "" {
		ch <- wrap(fmt.Sprintf("Formatting %s as ext4...", device))
		if err := exec.RunStreamed(ch, wrap, mkfsExt4Bin, "-F", device); err != nil {
			return err
		}
	} else {
		ch <- wrap(fmt.Sprintf("%s already has a filesystem, skipping mkfs", device))
	}

	if out, err := exec.RunPrivileged("/usr/bin/mkdir", "-p", longhornDataPath).CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
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
	if err := write(longhornMountUnit, []byte(unit)); err != nil {
		return err
	}

	if out, err := exec.RunPrivileged("/usr/bin/systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	ch <- wrap(fmt.Sprintf("Mounting %s...", longhornDataPath))
	return exec.RunStreamed(ch, wrap, "/usr/bin/systemctl", "enable", "--now", "var-lib-longhorn.mount")
}

// prepareOpenEBSDevice turns device into an LVM thin-pool volume group,
// which the LVM LocalPV CSI driver provisions volumes from.
func prepareOpenEBSDevice(ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, device string) error {
	if exec.RunPrivileged(vgsBin, "--noheadings", "-o", "vg_name", OpenebsVGName).Run() == nil {
		ch <- wrap(fmt.Sprintf("Volume group %s already exists", OpenebsVGName))
		return nil
	}

	ch <- wrap(fmt.Sprintf("Creating physical volume on %s...", device))
	if err := exec.RunStreamed(ch, wrap, pvcreateBin, "-f", device); err != nil {
		return err
	}
	ch <- wrap(fmt.Sprintf("Creating volume group %s...", OpenebsVGName))
	if err := exec.RunStreamed(ch, wrap, vgcreateBin, OpenebsVGName, device); err != nil {
		return err
	}
	ch <- wrap(fmt.Sprintf("Creating thin pool %s/%s...", OpenebsVGName, OpenebsThinPoolLV))
	return exec.RunStreamed(ch, wrap, lvcreateBin, "--type", "thin-pool", "-l", "95%VG", "-n", OpenebsThinPoolLV, OpenebsVGName)
}
