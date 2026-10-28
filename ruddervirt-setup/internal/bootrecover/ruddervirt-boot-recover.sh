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
REAPED_TOTAL=0     # corpses swept this boot, accumulated across reap cycles

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
  REAPED_TOTAL=$((REAPED_TOTAL + count))
  if (( count > 0 )); then
    log "reaped ${count} shutdown corpse(s) (${REAPED_TOTAL} this boot)"
  fi
}

wait_for_cni() {
  # phase=Running-scoped wait, not `rollout status`: the latter reads the
  # (possibly corpse-wedged) daemonset numberAvailable and can lie after a
  # reboot.
  #
  # Loop with a reap on every cycle: the post-reboot reconcile burst can
  # spawn corpses at any point in the convergence window, not just in the
  # first seconds - e.g. a controller re-creating pods while kube-ovn is
  # still coming up after a hard reboot. Each cycle reaps first, then
  # checks which core workloads are Ready, and we only declare convergence
  # after a cycle that required no further work.
  local deadline=$((SECONDS + CNI_DEADLINE)) w kind sel done_workloads
  while :; do
    reap_corpses
    for w in "${CNI_WORKLOADS[@]}"; do
      set -- $w
      kind=$1; sel=$2
      case " $done_workloads " in
        *" $w "*) continue ;;
      esac
      if "$KUBECTL" -n kube-system wait --for=condition=Ready pod -l "app=$sel" \
          --field-selector=status.phase=Running --timeout=1s >/dev/null 2>&1; then
        log "kube-ovn $kind ready"
        done_workloads="$done_workloads $w"
      fi
    done
    set -- $done_workloads
    if (( $# == ${#CNI_WORKLOADS[@]} )); then
      return 0
    fi
    if ((SECONDS >= deadline)); then
      log "kube-ovn core workloads not ready after ${CNI_DEADLINE}s - giving up"
      return 1
    fi
    sleep 5
  done
}

main() {
  log "starting post-boot recovery"
  wait_for_api || return 1
  reap_corpses
  wait_for_cni || return 1
  log "cluster converged (${REAPED_TOTAL:-0} corpse(s) reaped this boot)"
}

main
