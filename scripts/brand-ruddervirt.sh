#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-only
#
# Apply RudderVirt OS branding on the installed system. Runs on every boot so it
# stays correct across OS updates:
#   - /etc/os-release PRETTY_NAME (kept in sync with the read-only
#     /usr/lib/os-release; only PRETTY_NAME is overridden)
#   - the GRUB/BLS boot-menu entry titles under /boot/loader/entries (ostree
#     regenerates these on each deployment, so we re-apply the branding)
#
# Branding is cosmetic, so this is best-effort and never fails the unit.
set -uo pipefail

PRETTY_NAME="RudderVirt OS"

brand_os_release() {
  local src="/usr/lib/os-release" dest="/etc/os-release" tmp
  [[ -r "${src}" ]] || return 0
  tmp="${dest}.tmp"
  if /usr/bin/sed -e "s|^PRETTY_NAME=.*|PRETTY_NAME=\"${PRETTY_NAME}\"|" "${src}" > "${tmp}"; then
    /usr/bin/chmod 0644 "${tmp}"
    /usr/bin/mv -f "${tmp}" "${dest}"
    /usr/sbin/restorecon "${dest}" 2>/dev/null || true
  else
    /usr/bin/rm -f "${tmp}"
  fi
}

brand_boot_entries() {
  # Nothing to rebrand (already branded, or no entries)? Skip the remount so we
  # leave /boot read-only on the common path.
  /usr/bin/grep -sq 'Fedora CoreOS' /boot/loader/entries/*.conf || return 0

  # /boot is mounted read-only on Fedora CoreOS; remount it read-write just long
  # enough to rewrite the titles, then restore the read-only mount.
  /usr/bin/mount -o remount,rw /boot 2>/dev/null || return 0
  local entry
  for entry in /boot/loader/entries/*.conf; do
    [[ -f "${entry}" ]] || continue
    /usr/bin/sed -i 's/Fedora CoreOS/RudderVirt OS/g' "${entry}" 2>/dev/null || true
  done
  /usr/bin/mount -o remount,ro /boot 2>/dev/null || true
}

brand_os_release || true
brand_boot_entries || true
exit 0
