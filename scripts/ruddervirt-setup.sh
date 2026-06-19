#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-only
#
# ruddervirt-setup: the single operator entrypoint for installing, updating, or
# tearing down the RudderVirt solution (k3s + Rook-Ceph + KubeVirt + CDI +
# Kube-OVN) on a node provisioned from the RudderVirt OS image.
#
# All solution artifacts (the k3s binary, manifests, helper scripts, config
# templates) are bundled in the OS image. This script applies them; it does not
# run automatically. To update the solution, build/flash a newer OS image (which
# carries newer bundled artifacts) and re-run `ruddervirt-setup update`.
set -euo pipefail

SELF="/usr/local/bin/ruddervirt-setup"
K3S_UNIT="/etc/systemd/system/k3s.service"
ETCD_VERSION="v3.5.21"

log()  { /usr/bin/printf '\n>>> %s\n' "$*"; }
die()  { /usr/bin/printf 'ERROR: %s\n' "$*" >&2; exit 1; }

require_root() {
  [[ "${EUID}" -eq 0 ]] || die "ruddervirt-setup must be run as root (try: sudo ruddervirt-setup ${1:-})"
}

# Re-invoke a setup subcommand with root privileges. The interactive menu runs
# as the unprivileged admin user, but install/update/purge/network need root.
run_privileged() {
  if [[ "${EUID}" -eq 0 ]]; then
    "${SELF}" "$@"
  else
    /usr/bin/sudo "${SELF}" "$@"
  fi
}

install_packages() {
  log "Installing node and solution packages (btop, mdadm, iperf3, k3s-selinux, k9s)"
  # Collect only what's missing, then layer it all in a single rpm-ostree
  # transaction. rpm-ostree allows one transaction at a time, so installing the
  # node tools here (rather than from a first-boot service) avoids the
  # "Transaction in progress" collision that aborts the operator's install.
  local pkgs=() p
  # Base node tooling plus the k3s SELinux policy, all from the Fedora repos.
  for p in btop mdadm iperf3 k3s-selinux; do
    /usr/bin/rpm -q "${p}" >/dev/null 2>&1 || pkgs+=("${p}")
  done
  # k9s ships as a bundled local rpm rather than from a repo.
  if ! command -v k9s >/dev/null 2>&1 && [[ -f /var/tmp/k9s.rpm ]]; then
    pkgs+=(/var/tmp/k9s.rpm)
  fi
  if (( ${#pkgs[@]} )); then
    /usr/bin/rpm-ostree install --apply-live --allow-inactive --assumeyes "${pkgs[@]}"
  else
    log "All packages already installed"
  fi
}

install_etcdctl() {
  [[ -x /opt/bin/etcdctl ]] && return 0
  log "Installing etcdctl ${ETCD_VERSION}"
  /usr/bin/mkdir -p /opt/bin
  /usr/bin/curl -fsSL "https://github.com/etcd-io/etcd/releases/download/${ETCD_VERSION}/etcd-${ETCD_VERSION}-linux-amd64.tar.gz" \
    | /usr/bin/tar xz --strip-components=1 -C /opt/bin "etcd-${ETCD_VERSION}-linux-amd64/etcdctl"
  /usr/bin/chmod +x /opt/bin/etcdctl
}

render_config() {
  log "Rendering k3s config"
  /usr/local/bin/render-k3s-config
}

write_k3s_unit() {
  log "Installing k3s systemd unit"
  /usr/bin/cat > "${K3S_UNIT}" <<'EOF'
[Unit]
Description=Run K3s
Wants=network-online.target
After=network-online.target

[Service]
Type=notify
EnvironmentFile=-/etc/default/%N
EnvironmentFile=-/etc/sysconfig/%N
EnvironmentFile=-/etc/systemd/system/%N.env
KillMode=process
Delegate=yes
LimitNOFILE=1048576
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
TimeoutStartSec=0
Restart=always
RestartSec=5s
ExecStartPre=/bin/mkdir -p /etc/rancher/k3s
ExecStartPre=-/sbin/modprobe br_netfilter
ExecStartPre=-/sbin/modprobe overlay
ExecStartPre=-/sbin/modprobe iscsi_tcp
ExecStartPre=-/sbin/modprobe dm_crypt
ExecStartPre=-/sbin/modprobe nfs
ExecStartPre=-/sbin/modprobe wireguard
ExecStartPre=-/sbin/modprobe tun
ExecStartPre=-/sbin/modprobe geneve
ExecStartPre=-/sbin/modprobe openvswitch
ExecStartPre=-/sbin/modprobe ip_tables
ExecStartPre=-/sbin/modprobe iptable_nat
ExecStart=/bin/bash -c '/usr/local/bin/k3s server'

[Install]
WantedBy=multi-user.target
EOF
  /usr/bin/systemctl daemon-reload
}

apply_manifests() {
  log "Applying solution manifests (Rook-Ceph, KubeVirt, CDI, Kube-OVN, ...)"
  /usr/local/bin/prepare-k3s
}

cmd_install() {
  require_root install
  install_packages
  install_etcdctl
  render_config
  write_k3s_unit
  log "Enabling and starting k3s"
  /usr/bin/systemctl enable --now k3s.service
  apply_manifests
  log "Install complete."
}

cmd_update() {
  require_root update
  install_packages
  install_etcdctl
  render_config
  write_k3s_unit
  log "Restarting k3s"
  /usr/bin/systemctl restart k3s.service
  apply_manifests
  log "Update complete."
}

cmd_purge() {
  require_root purge
  /usr/bin/printf 'This will STOP k3s and DELETE ALL k3s/cluster data. Type "yes" to continue: '
  local reply
  read -r reply
  [[ "${reply}" == "yes" ]] || die "Aborted."
  /usr/bin/systemctl disable --now k3s.service 2>/dev/null || true
  /usr/bin/rm -rf /var/lib/rancher/k3s /etc/rancher/k3s
  /usr/bin/rm -f /var/lib/ruddervirt/prepare-k3s.done
  log "Purge complete."
}

cmd_network() {
  require_root network
  local desired_mtu=8950 ifname ipaddr confirm
  read -r -p "Interface name (e.g. eno1): " ifname
  read -r -p "IPv4 address (e.g. 192.168.10.2): " ipaddr

  [[ -n "${ifname}" && -n "${ipaddr}" ]] || die "Interface name and IP address are required."
  /usr/sbin/ip link show "${ifname}" >/dev/null 2>&1 || die "Interface '${ifname}' does not exist."
  if /usr/bin/nmcli -t -f NAME connection show | /usr/bin/grep -Fxq "${ifname}"; then
    die "A NetworkManager connection named '${ifname}' already exists."
  fi

  /usr/bin/printf '\nAbout to configure interface:\n  IFNAME: %s\n  IPADDR: %s/24\n  MTU: %s\n' \
    "${ifname}" "${ipaddr}" "${desired_mtu}"
  read -r -p "Proceed? [y/N]: " confirm
  case "${confirm}" in
    y|Y|yes|YES) ;;
    *) log "Aborted."; return 0 ;;
  esac

  /usr/bin/nmcli connection add type ethernet con-name "${ifname}" ifname "${ifname}" \
    ipv4.method manual ipv4.addresses "${ipaddr}/24"
  /usr/bin/nmcli connection modify "${ifname}" 802-3-ethernet.mtu "${desired_mtu}"
  /usr/bin/nmcli connection up "${ifname}"
  log "Network configured on ${ifname}."
}

# Escape hatch: replace this menu with an interactive bash shell. The exported
# RUDDERVIRT_SHELL guard stops the login profile from relaunching the menu.
cmd_shell() {
  log "Dropping to a shell. Run 'ruddervirt-setup' for the menu; 'exit' (or Ctrl-D) to log out."
  export RUDDERVIRT_SHELL=1
  exec /bin/bash -l
}

# Interactive menu shown when ruddervirt-setup is run with no subcommand (e.g.
# on admin login). Kept resilient: a failed action returns here, not logout.
menu() {
  set +e
  local choice
  while :; do
    /usr/bin/cat <<'EOF'

  ============================================================
   RudderVirt OS — setup
  ============================================================
    1) install   Install the solution on this node
    2) update    Re-apply the solution after an OS image update
    3) network   Configure a private network interface
    4) purge     Stop k3s and delete all cluster data (DESTRUCTIVE)
    5) shell     Drop to a bash shell
    6) logout    End this session
EOF
    /usr/bin/printf 'Select an option [1-6]: '
    if ! read -r choice; then
      /usr/bin/printf '\n'
      exit 0
    fi
    case "${choice}" in
      1|install) run_privileged install ;;
      2|update)  run_privileged update ;;
      3|network) run_privileged network ;;
      4|purge)   run_privileged purge ;;
      5|shell)   cmd_shell ;;
      6|logout|quit|exit|q) exit 0 ;;
      "") : ;;
      *) /usr/bin/printf 'Invalid selection: %s\n' "${choice}" ;;
    esac
  done
}

usage() {
  /usr/bin/cat <<'EOF'
Usage: ruddervirt-setup [command]

With no command, an interactive menu is shown.

Commands:
  install   Install the RudderVirt solution on this node
  update    Re-apply the solution after flashing a newer OS image
  network   Configure a private network interface
  purge     Stop k3s and delete all cluster data (DESTRUCTIVE)
  shell     Drop to a bash shell (escape the menu)
  menu      Show the interactive menu
EOF
}

main() {
  case "${1:-}" in
    install) cmd_install ;;
    update)  cmd_update ;;
    purge)   cmd_purge ;;
    network) cmd_network ;;
    shell)   cmd_shell ;;
    menu|"") menu ;;
    -h|--help|help) usage ;;
    *) usage; exit 1 ;;
  esac
}

main "$@"
