#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-only
#
# Post-reboot cluster recovery: reaps shutdown corpses cluster-wide (pods the
# reboot left stuck with terminated reason ContainerStatusUnknown - they wedge
# DaemonSet status, e.g. ovs-ovn Ready=0 while its real pod is healthy), then
# waits for kube-ovn's core workloads to converge. Host services needing a
# converged cluster should declare Wants= + After= on this oneshot directly
# (same boot transaction, no intermediate target). Idempotent; fails loudly in
# the journal without blocking boot.
set -u

KUBECTL=/usr/local/bin/kubectl
TAG=ruddervirt-boot-recover
API_DEADLINE=300   # seconds to wait for the k3s API after boot
CNI_DEADLINE=300   # seconds to wait for kube-ovn core workload convergence

log() { echo "[$TAG] $*"; }

# kube-ovn's load-bearing workloads, keyed by each workload's "app" pod
# label (same set as ruddervirt-setup's k3s.KubeOvnCoreWorkloads).
# kube-ovn-pinger is a diagnostic sidecar and deliberately not waited on.
CNI_WORKLOADS=(
  "daemonset/ovs-ovn ovs"
  "daemonset/kube-ovn-cni kube-ovn-cni"
  "deployment/kube-ovn-controller kube-ovn-controller"
  "deployment/ovn-central ovn-central"
)

wait_for_api() {
  local deadline=$((SECONDS + API_DEADLINE))
  until "$KUBECTL" get --raw=/readyz >/dev/null 2>&1; do
    if ((SECONDS >= deadline)); then
      log "k3s API not ready after ${API_DEADLINE}s - giving up"
      return 1
    fi
    sleep 5
  done
  log "k3s API ready"
}

reap_corpses() {
  # Any pod (any namespace) whose (init)container terminated with reason
  # ContainerStatusUnknown and is no longer Running: it has no live
  # containers, so force-delete is safe - the controllers recreate as needed.
  # Succeeded pods (e.g. completed helm-install jobs) are left alone.
  local corpses ns name count
  corpses=$("$KUBECTL" get pods -A --no-headers \
      --field-selector=status.phase!=Running,status.phase!=Succeeded 2>/dev/null \
    | awk '$4 ~ /ContainerStatusUnknown/ {print $1"/"$2}')
  count=0
  while IFS=/ read -r ns name; do
    [ -n "${ns:-}" ] || continue
    [ -n "${name:-}" ] || continue
    if "$KUBECTL" -n "$ns" delete pod "$name" --force --grace-period=0 >/dev/null 2>&1; then
      count=$((count + 1))
      log "reaped corpse $ns/$name"
    fi
  done <<< "$corpses"
  log "reaped ${count} shutdown corpse(s)"
}

wait_for_cni() {
  # phase=Running-scoped wait, not `rollout status`: the latter reads the
  # (possibly corpse-wedged) daemonset numberAvailable and can lie after a
  # reboot.
  local deadline=$((SECONDS + CNI_DEADLINE)) w kind sel
  for w in "${CNI_WORKLOADS[@]}"; do
    set -- $w
    kind=$1; sel=$2
    until "$KUBECTL" -n kube-system wait --for=condition=Ready pod -l "app=$sel" \
        --field-selector=status.phase=Running --timeout=1s >/dev/null 2>&1; do
      if ((SECONDS >= deadline)); then
        log "kube-ovn $kind (app=$sel) not ready after ${CNI_DEADLINE}s - giving up"
        return 1
      fi
      sleep 5
    done
    log "kube-ovn $kind ready"
  done
}

main() {
  log "starting post-boot recovery"
  wait_for_api || return 1
  reap_corpses
  wait_for_cni || return 1
  log "cluster converged"
}

main
