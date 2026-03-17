#!/usr/bin/env bash
set -euo pipefail

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml

if [[ ! -f "${KUBECONFIG}" ]]; then
  echo "Kubeconfig not found at ${KUBECONFIG}" >&2
  exit 1
fi

for _ in {1..60}; do
  if /usr/local/bin/kubectl get --raw=/readyz >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

if ! /usr/local/bin/kubectl get --raw=/readyz >/dev/null 2>&1; then
  echo "K3s API did not become ready" >&2
  exit 1
fi

if [[ -f "/etc/ruddervirt/ruddervirt-networking.env" ]]; then
  # shellcheck disable=SC1091
  source /etc/ruddervirt/ruddervirt-networking.env
fi

JOIN_CIDR="172.31.0.0/16"
POD_CIDR="${POD_CIDR:?Missing POD_CIDR}"
SVC_CIDR="${SVC_CIDR:?Missing SVC_CIDR}"

if [[ -z "${POD_GATEWAY:-}" ]]; then
  pod_base="${POD_CIDR%%/*}"
  IFS='.' read -r pod_a pod_b pod_c pod_d <<< "${pod_base}"
  if [[ -z "${pod_a}" || -z "${pod_d}" ]]; then
    echo "Invalid POD_CIDR: ${POD_CIDR}" >&2
    exit 1
  fi
  if [[ "${pod_d}" -ge 255 ]]; then
    echo "Unable to derive POD_GATEWAY from POD_CIDR: ${POD_CIDR}" >&2
    exit 1
  fi
  POD_GATEWAY="${pod_a}.${pod_b}.${pod_c}.$((pod_d + 1))"
fi

if [[ -x "/opt/bin/install-kube-ovn.sh" ]]; then
  /usr/bin/sed -i -E "s|^(export[[:space:]]+)?JOIN_CIDR=.*$|JOIN_CIDR=${JOIN_CIDR}|" /opt/bin/install-kube-ovn.sh
  /usr/bin/sed -i -E "s|^(export[[:space:]]+)?POD_CIDR=.*$|POD_CIDR=${POD_CIDR}|" /opt/bin/install-kube-ovn.sh
  /usr/bin/sed -i -E "s|^(export[[:space:]]+)?SVC_CIDR=.*$|SVC_CIDR=${SVC_CIDR}|" /opt/bin/install-kube-ovn.sh
  /usr/bin/sed -i -E "s|^(export[[:space:]]+)?POD_GATEWAY=.*$|POD_GATEWAY=${POD_GATEWAY}|" /opt/bin/install-kube-ovn.sh

  kube_ovn_rc=0
  /opt/bin/install-kube-ovn.sh || kube_ovn_rc=$?
  if [[ ${kube_ovn_rc} -ne 0 && ${kube_ovn_rc} -ne 7 ]]; then
    echo "install-kube-ovn.sh failed with exit code ${kube_ovn_rc}" >&2
    exit ${kube_ovn_rc}
  fi
  if [[ ${kube_ovn_rc} -eq 7 ]]; then
    echo "install-kube-ovn.sh exited with 7; continuing"
  fi
else
  echo "Missing executable /opt/bin/install-kube-ovn.sh" >&2
  exit 1
fi

/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/ceph-rook.yml"

for _ in {1..60}; do
  if /usr/local/bin/kubectl -n rook-ceph get deployment/rook-ceph-operator >/dev/null 2>&1; then
    break
  fi
  sleep 5
done

if ! /usr/local/bin/kubectl -n rook-ceph get deployment/rook-ceph-operator >/dev/null 2>&1; then
  echo "rook-ceph-operator deployment not found" >&2
  exit 1
fi

/usr/local/bin/kubectl -n rook-ceph rollout status deployment/rook-ceph-operator --timeout=900s

for _ in {1..60}; do
  if /usr/local/bin/kubectl -n rook-ceph get cephcluster/rook-ceph >/dev/null 2>&1; then
    break
  fi
  sleep 10
done

if ! /usr/local/bin/kubectl -n rook-ceph get cephcluster/rook-ceph >/dev/null 2>&1; then
  echo "cephcluster/rook-ceph not found" >&2
  exit 1
fi

/usr/local/bin/kubectl -n rook-ceph wait --for=condition=Ready cephcluster/rook-ceph --timeout=1800s

/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/kubevirt-operator.yaml"
/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/kubevirt-crd.yaml"
/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/cdi-operator.yaml"
/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/cdi-crd.yaml"
/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/multus.yaml"
/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/kubevirt-patches.yaml"
/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/snapshot-class.yaml"

/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/ruddervirt-nebula.yaml"
/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/user-nebula-cert.yaml" || true

/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/user-module-token.yaml" || true
/usr/local/bin/kubectl apply -f "/etc/ruddervirt/manifests/user-pull-secret.yaml" || true

/usr/bin/mkdir -p /var/lib/ruddervirt
/usr/bin/touch /var/lib/ruddervirt/prepare-k3s.done
