#!/usr/bin/env bash
set -euo pipefail

NETWORK_ENV="/etc/ruddervirt/ruddervirt-networking.env"
TEMPLATE_CONFIG="/etc/ruddervirt/k3s-config.yaml"
OUTPUT_CONFIG="/etc/rancher/k3s/config.yaml"
if [[ -f "${NETWORK_ENV}" ]]; then
  # shellcheck disable=SC1091
  source "${NETWORK_ENV}"
fi

POD_CIDR="${POD_CIDR:-10.42.0.0/16}"
SVC_CIDR="${SVC_CIDR:-10.43.0.0/16}"
LOCAL_IP="${LOCAL_IP:-}"

svc_base="${SVC_CIDR%%/*}"
svc_prefix="${SVC_CIDR##*/}"
IFS='.' read -r svc_a svc_b svc_c svc_d <<< "${svc_base}"
if [[ "${svc_prefix}" != "16" ]]; then
  echo "SVC_CIDR must be /16: ${SVC_CIDR}" >&2
  exit 1
fi
if [[ -z "${svc_a}" || -z "${svc_b}" ]]; then
  echo "Invalid SVC_CIDR: ${SVC_CIDR}" >&2
  exit 1
fi
CLUSTER_DNS="${svc_a}.${svc_b}.0.10"
if [[ ! -f "${TEMPLATE_CONFIG}" ]]; then
  echo "Template config not found at ${TEMPLATE_CONFIG}" >&2
  exit 1
fi

umask 022
/usr/bin/mkdir -p /etc/rancher/k3s

tmp_config="$(/usr/bin/mktemp)"
/usr/bin/sed \
  -e "s|__POD_CIDR__|${POD_CIDR}|g" \
  -e "s|__SVC_CIDR__|${SVC_CIDR}|g" \
  -e "s|__CLUSTER_DNS__|${CLUSTER_DNS}|g" \
  -e "s|__LOCAL_IP__|${LOCAL_IP}|g" \
  "${TEMPLATE_CONFIG}" > "${tmp_config}"

/usr/bin/install -m 0644 "${tmp_config}" "${OUTPUT_CONFIG}"
/usr/bin/rm -f "${tmp_config}"
