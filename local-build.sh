#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-only
#
# Build a RudderVirt OS installer ISO locally, and optionally boot it in QEMU.
#
# Usage: ./local-build.sh [--test] [extra create-iso.py args...]
#   ./local-build.sh                  # -> out/ruddervirt-install-dev.iso
#   ./local-build.sh --test           # build, then boot the ISO in QEMU
#   ./local-build.sh --version test1  # custom version string in the filename
#   ./local-build.sh --show-ignition  # also print the generated Ignition config
set -euo pipefail

IMAGE="ruddervirt-os-builder"
REPO_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
OUT_DIR="${REPO_DIR}/out"
TEST_DISK="${OUT_DIR}/ruddervirt-test.qcow2"

# Pull a --test/--run flag out of the args; the rest pass through to the builder.
TEST=0
BUILD_ARGS=()
for arg in "$@"; do
  case "${arg}" in
    --test|--run|--qemu) TEST=1 ;;
    *) BUILD_ARGS+=("${arg}") ;;
  esac
done

if command -v docker >/dev/null 2>&1; then
  RUNTIME=docker
elif command -v podman >/dev/null 2>&1; then
  RUNTIME=podman
else
  echo "Error: neither docker nor podman found in PATH." >&2
  exit 1
fi

cd "${REPO_DIR}"
mkdir -p "${OUT_DIR}"

echo ">>> Building ${IMAGE} image (${RUNTIME})"
"${RUNTIME}" build -t "${IMAGE}" .

echo ">>> Building installer ISO into ${OUT_DIR}"
"${RUNTIME}" run --rm -t -v "${OUT_DIR}:/output" "${IMAGE}" ${BUILD_ARGS[@]+"${BUILD_ARGS[@]}"}

echo
echo ">>> Done. ISOs in ${OUT_DIR}:"
ls -lh "${OUT_DIR}"/*.iso 2>/dev/null || echo "  (no ISO produced)"

if [ "${TEST}" -eq 1 ]; then
  command -v qemu-system-x86_64 >/dev/null 2>&1 || { echo "Error: qemu-system-x86_64 not found." >&2; exit 1; }

  iso="$(ls -t "${OUT_DIR}"/*.iso 2>/dev/null | head -1 || true)"
  [ -n "${iso}" ] || { echo "Error: no ISO to boot." >&2; exit 1; }

  # Start every run from a fresh disk so each boot does a clean install. The
  # installer requires >=50 GiB; qcow2 is sparse so 100G costs almost nothing.
  rm -f "${TEST_DISK}"
  echo ">>> Creating fresh test disk ${TEST_DISK} (100G)"
  qemu-img create -f qcow2 "${TEST_DISK}" 100G

  KVM=()
  if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
    KVM=(-accel kvm -cpu host)
  else
    echo ">>> /dev/kvm not usable; running under (slow) emulation"
  fi

  # GUI window if we have a display, otherwise headless over VNC.
  if [ -n "${DISPLAY:-}" ]; then
    DISPLAY_ARGS=(-vga virtio -display gtk)
  else
    DISPLAY_ARGS=(-vga virtio -display none -vnc 127.0.0.1:0)
    echo ">>> Headless: VNC on 127.0.0.1:5900"
    echo "    Tunnel from your machine:  ssh -L 5900:localhost:5900 <this-host>"
    echo "    then point a VNC client at localhost:5900"
  fi

  # Boot order is disk-first: an empty disk falls through to the ISO (install),
  # and after install the disk boots the installed system.
  echo ">>> Booting ${iso##*/} in QEMU"
  qemu-system-x86_64 \
    -name ruddervirt-test \
    -machine q35 ${KVM[@]+"${KVM[@]}"} \
    -smp 4 -m 8192 \
    -drive file="${TEST_DISK}",if=virtio,format=qcow2 \
    -cdrom "${iso}" \
    -boot order=cd \
    -nic user,model=virtio-net-pci \
    "${DISPLAY_ARGS[@]}"
fi
