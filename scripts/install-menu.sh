#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-only
#
# pre-install script for `coreos-installer iso customize --pre-install`.
# Runs as part of the boot-time auto-install service BEFORE installation.
# Presents an interactive menu on the console so the operator can choose the
# destination disk, then writes an installer config fragment that
# coreos-installer reads to set --dest-device.
set -euo pipefail

INSTALLER_D="/etc/coreos/installer.d"
DEST_FILE="${INSTALLER_D}/99-dest-device.yaml"

# Minimum target disk size. The Ignition config in server.bu resizes the root
# partition to 51200 MiB (50 GiB), so a smaller disk cannot hold the install.
MIN_DISK_MIB=51200
MIN_DISK_BYTES=$((MIN_DISK_MIB * 1024 * 1024))

# Bind stdio to the operator console. The auto-install service has no
# interactive stdin; /dev/console is the kernel/systemd-directed operator
# console (VGA tty OR serial), which is more robust than hardcoding /dev/tty1
# that would be wrong on headless/serial systems.
if [[ -c /dev/console ]]; then
  exec </dev/console >/dev/console 2>&1
fi

# coreos-installer runs this as an early-boot service, so systemd's "[ OK ]
# Started ..." status lines and kernel log spam interleave with our prompt on
# the console. Silence both while we run, and restore on exit so the operator
# still sees install/boot progress afterward.
restore_console() {
  kill -s RTMIN+20 1 2>/dev/null || true   # systemd: re-enable show-status
}
trap restore_console EXIT
kill -s RTMIN+21 1 2>/dev/null || true     # systemd: disable show-status
if [[ -w /proc/sys/kernel/printk ]]; then
  /usr/bin/printf '%s\n' "1 4 1 7" > /proc/sys/kernel/printk 2>/dev/null || true
fi
# Let PID 1 process the signal and any in-flight status lines flush, then clear
# the screen so prior boot output doesn't clutter the menu.
/usr/bin/sleep 1
/usr/bin/printf '\033[2J\033[3J\033[H'

say() { /usr/bin/printf '%s\n' "$*"; }
banner() {
  say ""
  say "================================================================"
  say "  $*"
  say "================================================================"
}
fail() { say "ERROR: $*" >&2; exit 1; }

# Confirm a destructive selection by requiring the operator to type "yes".
confirm_wipe() {
  local dev="$1"
  banner "CONFIRM: ${dev} will be WIPED and the OS installed onto it"
  while :; do
    /usr/bin/printf "Type 'yes' to ERASE %s and continue, or 'no' to abort: " "${dev}"
    local reply
    read -r reply || fail "No input received on console; aborting."
    case "${reply}" in
      yes|YES|y|Y) return 0 ;;
      no|NO|n|N)   fail "Aborted by user." ;;
      *)           say "Please type 'yes' or 'no'." ;;
    esac
  done
}

# Reduce a source device (partition or whole disk) to its parent whole disk and
# record it as part of the live boot medium to exclude from candidates.
live_disks=""
add_live_disk() {
  local dev="$1" pk
  [[ -n "${dev}" ]] || return 0
  pk="$(/usr/bin/lsblk -no PKNAME "${dev}" 2>/dev/null | /usr/bin/head -n1 || true)"
  if [[ -n "${pk}" ]]; then
    live_disks="${live_disks} /dev/${pk}"
  else
    live_disks="${live_disks} ${dev}"
  fi
}

# Resolve the device(s) backing the live environment so we never offer them.
for mp in / /run/media/iso /sysroot /boot; do
  src="$(/usr/bin/findmnt -nro SOURCE --target "${mp}" 2>/dev/null | /usr/bin/head -n1 || true)"
  add_live_disk "${src}"
done
while read -r src; do
  add_live_disk "${src}"
done < <(/usr/bin/findmnt -nro SOURCE -t iso9660,squashfs,overlay 2>/dev/null || true)

is_live_disk() {
  local cand="$1" d
  for d in ${live_disks}; do
    [[ "${cand}" == "${d}" ]] && return 0
  done
  return 1
}

# Enumerate candidate whole disks: TYPE==disk, excluding pseudo devices and the
# live boot medium.
candidates=()
labels=()
while read -r name type; do
  [[ "${type}" == "disk" ]] || continue
  case "${name}" in
    /dev/loop*|/dev/ram*|/dev/sr*|/dev/zram*) continue ;;
  esac
  if is_live_disk "${name}"; then
    say "Skipping live boot medium: ${name}"
    continue
  fi
  size="$(/usr/bin/lsblk -dnro SIZE "${name}" 2>/dev/null | /usr/bin/head -n1)"
  size_bytes="$(/usr/bin/lsblk -dnbo SIZE "${name}" 2>/dev/null | /usr/bin/head -n1)"
  if [[ -z "${size_bytes}" ]] || (( size_bytes < MIN_DISK_BYTES )); then
    say "Skipping ${name} (${size:-unknown size}): smaller than the required ${MIN_DISK_MIB} MiB"
    continue
  fi
  model="$(/usr/bin/lsblk -dnro MODEL "${name}" 2>/dev/null | /usr/bin/head -n1)"
  [[ -n "${model}" ]] || model="(unknown model)"
  candidates+=("${name}")
  labels+=("${name}  ${size}  ${model}")
done < <(/usr/bin/lsblk -dpno NAME,TYPE)

banner "RudderVirt installer: choose the installation disk"
say "WARNING: the chosen disk will be COMPLETELY ERASED."
say ""

chosen=""
if [[ "${#candidates[@]}" -eq 0 ]]; then
  fail "No eligible installation disks found (excluding the live boot medium and any disk smaller than ${MIN_DISK_MIB} MiB). Aborting install."
elif [[ "${#candidates[@]}" -eq 1 ]]; then
  chosen="${candidates[0]}"
  say "Exactly one eligible disk found:"
  say "  ${labels[0]}"
else
  say "Available disks:"
  i=1
  for l in "${labels[@]}"; do
    /usr/bin/printf '  %d) %s\n' "${i}" "${l}"
    i=$((i + 1))
  done
  say ""
  while :; do
    /usr/bin/printf 'Enter the number of the disk to install onto [1-%d]: ' "${#candidates[@]}"
    read -r reply || fail "No input received on console; aborting."
    if [[ "${reply}" =~ ^[0-9]+$ ]] && (( reply >= 1 && reply <= ${#candidates[@]} )); then
      chosen="${candidates[$((reply - 1))]}"
      break
    fi
    say "Invalid selection: '${reply}'. Try again."
  done
fi

[[ -n "${chosen}" ]] || fail "No disk chosen."
[[ -b "${chosen}" ]] || fail "Chosen path is not a block device: ${chosen}"

confirm_wipe "${chosen}"

umask 022
/usr/bin/mkdir -p "${INSTALLER_D}"
/usr/bin/printf 'dest-device: "%s"\n' "${chosen}" > "${DEST_FILE}"

say ""
say "Selected installation disk: ${chosen}"
say "Wrote ${DEST_FILE}:"
/usr/bin/cat "${DEST_FILE}"
say "Proceeding with installation onto ${chosen} ..."
