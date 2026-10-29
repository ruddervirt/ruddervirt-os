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
    log "reaped ${count} shutdown corpse(s) (running total ${REAPED_TOTAL} this run)"
  fi
}

sweep_failed() {
  # Controller-owned pods ended in phase Failed with real termination records
  # (exit codes) - the siblings of the ContainerStatusUnknown corpses: after a
  # reboot/k3s restart, kubelet records a clean death for everything it could
  # reconcile before dying, the controller replaces the pod, and the Failed
  # object is never cleaned (kubelet's terminated-pod GC threshold is far too
  # high to ever kick in). One wave per reboot, harmless but accumulating.
  #
  # Deleting is safe: phase=Failed guarantees no live containers, and every
  # Deployment/DaemonSet/StatefulSet pod is recreated if (and only if) the
  # controller still needs it - a superseded dead pod is never resurrected.
  # Job-owned pods are excluded (-l '!job-name'): the Job controller counts
  # failed pods against backoffLimit and deleting an object can reset that
  # accounting, causing bounded retries to loop forever.
  local failed ns name count
  failed=$("$KUBECTL" get pods -A -l '!job-name' --no-headers \
      --field-selector=status.phase=Failed 2>/dev/null)
  count=0
  while read -r ns name _; do
    [ -n "${ns:-}" ] || continue
    [ -n "${name:-}" ] || continue
    if "$KUBECTL" -n "$ns" delete pod "$name" --force --grace-period=0 >/dev/null 2>&1; then
      count=$((count + 1))
      log "removed stale failed pod $ns/$name"
    fi
  done <<< "$failed"
  REAPED_TOTAL=$((REAPED_TOTAL + count))
  if (( count > 0 )); then
    log "removed ${count} stale failed pod(s) (running total ${REAPED_TOTAL} this run)"
  fi
}

sweep_completed() {
  # ReplicaSet-owned pods kubelet finalized as Succeeded when it re-adopts
  # pods after a reboot - a controller pod can never "complete" under
  # restartPolicy=Always in normal life, so Succeeded here means finalization
  # residue from a restart wave, superseded by the controller's real pods.
  # Deleting is safe for the same reasons as sweep_failed, and Job-owned pods
  # are excluded likewise (their finished Job objects are the owner's domain,
  # not a pod sweeper's).
  local succeeded ns name count
  succeeded=$("$KUBECTL" get pods -A -l '!job-name' --no-headers \
      --field-selector=status.phase=Succeeded 2>/dev/null)
  count=0
  while read -r ns name _; do
    [ -n "${ns:-}" ] || continue
    [ -n "${name:-}" ] || continue
    if "$KUBECTL" -n "$ns" delete pod "$name" --force --grace-period=0 >/dev/null 2>&1; then
      count=$((count + 1))
      log "removed completed pod $ns/$name"
    fi
  done <<< "$succeeded"
  REAPED_TOTAL=$((REAPED_TOTAL + count))
  if (( count > 0 )); then
    log "removed ${count} completed pod(s) (running total ${REAPED_TOTAL} this run)"
  fi
}

sweep_all() {
  reap_corpses
  sweep_failed
  sweep_completed
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
    sweep_all
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
  # --reap-only: single sweep, no API/CNI waits - the timer unit's mode. A
  # k3s/containerd restart (ruddervirt-setup re-run, crash-restart, ...)
  # kills containers outside the boot window, so a low-frequency sweep that
  # runs indefinitely catches what the boot oneshot can't (it exits once
  # CNI converges). API-down is a silent no-op: reap_corpses just finds
  # nothing.
  if [ "${1:-}" = "--reap-only" ]; then
    log "starting stale-pod sweep (timer)"
    sweep_all
    log "sweep complete (${REAPED_TOTAL:-0} stale pod(s) removed)"
    return 0
  fi
  log "starting post-boot recovery"
  wait_for_api || return 1
  sweep_all
  wait_for_cni || return 1
  log "cluster converged (${REAPED_TOTAL:-0} stale pod(s) removed this boot)"
}

main "$@"
