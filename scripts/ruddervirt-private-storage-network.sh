#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-only
#
# Brings up a secondary NIC for the private storage/VM network with a manual
# IPv4 address and a jumbo-frame MTU, via NetworkManager (nmcli). Interactive:
# prompts for the interface name and address, then creates/activates the
# connection. Run on the installed host as the admin user.
set -euo pipefail

read -r -p "Interface name (e.g. eno1): " IFNAME
read -r -p "IPv4 address (e.g. 192.168.10.2): " IPADDR

if [[ -z "${IFNAME}" || -z "${IPADDR}" ]]; then
  echo "Interface name and IP address are required." >&2
  exit 1
fi

if ! ip link show "${IFNAME}" >/dev/null 2>&1; then
  echo "Interface '${IFNAME}' does not exist." >&2
  exit 1
fi

if nmcli -t -f NAME connection show | grep -Fxq "${IFNAME}"; then
  echo "A NetworkManager connection named '${IFNAME}' already exists." >&2
  exit 1
fi

echo ""
echo "About to configure interface:"
echo "  IFNAME: ${IFNAME}"
echo "  IPADDR: ${IPADDR}/24"
echo "  MTU: 8950"
read -r -p "Proceed? [y/N]: " CONFIRM

case "${CONFIRM}" in
  y|Y|yes|YES)
    sudo nmcli connection add type ethernet con-name "${IFNAME}" ifname "${IFNAME}" ipv4.method manual ipv4.addresses "${IPADDR}/24"
    sudo nmcli connection modify "${IFNAME}" 802-3-ethernet.mtu 8950
    sudo nmcli connection up "${IFNAME}"
    ;;
  *)
    echo "Aborted."
    exit 0
    ;;
esac