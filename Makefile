# SPDX-License-Identifier: GPL-3.0-only
#
# Single entrypoint for RudderVirt OS development (replaces the old local-build.sh).
#
#   make iso              # build the installer ISO -> out/ruddervirt-install-dev.iso
#   make iso VERSION=v1   # custom version string in the filename
#   make show-ignition    # build, and print the generated Ignition config
#   make boot             # boot the newest ISO in QEMU (needs qemu + a KVM host)
#   make ignition         # render server.bu -> out/server.ign (lightweight, no ISO)
#   make test-rootfs      # materialize the server.bu-injected files into out/test-rootfs/
#   make test-container   # open a shell in an FCOS userland with the server.bu files in place
#   make clean            # remove build artifacts

IMAGE     ?= ruddervirt-os-builder
VERSION   ?= dev
# TEST_IMG: canonical Fedora CoreOS image used for the Layer-2 test container.
TEST_IMG  ?= quay.io/fedora/fedora-coreos:stable
OUT_DIR   := $(CURDIR)/out
TEST_DISK := $(OUT_DIR)/ruddervirt-test.qcow2
IGNITION  := $(OUT_DIR)/server.ign
ROOTFS    := $(OUT_DIR)/test-rootfs
# BUTANE_IMG: official Butane image, used to render server.bu -> Ignition without
# building the full ISO.
BUTANE_IMG ?= quay.io/coreos/butane:release
RENDER    := scripts/dev/render-ignition-rootfs.py
RUNTIME   := $(shell command -v docker >/dev/null 2>&1 && echo docker || { command -v podman >/dev/null 2>&1 && echo podman; })

SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.ONESHELL:
.DEFAULT_GOAL := help

.PHONY: help iso show-ignition boot ignition test-rootfs test-container clean build-tui

help:  ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n",$$1,$$2}'

iso:  ## Build the installer ISO into out/ (VERSION=, ARGS=)
	@[ -n "$(RUNTIME)" ] || { echo "Error: neither docker nor podman found in PATH." >&2; exit 1; }
	mkdir -p "$(OUT_DIR)"
	echo ">>> Building $(IMAGE) image ($(RUNTIME))"
	$(RUNTIME) build -t "$(IMAGE)" .
	echo ">>> Building installer ISO into $(OUT_DIR)"
	$(RUNTIME) run --rm -t -v "$(OUT_DIR):/output" "$(IMAGE)" --version "$(VERSION)" $(ARGS)
	echo ">>> Done. ISOs in $(OUT_DIR):"
	ls -lh "$(OUT_DIR)"/*.iso 2>/dev/null || echo "  (no ISO produced)"

show-ignition:  ## Build and print the generated Ignition config
	$(MAKE) iso ARGS=--show-ignition

boot:  ## Boot the newest ISO in QEMU (KVM if available; needs qemu, a KVM host)
	command -v qemu-system-x86_64 >/dev/null 2>&1 || { echo "Error: qemu-system-x86_64 not found." >&2; exit 1; }
	iso="$$(ls -t "$(OUT_DIR)"/*.iso 2>/dev/null | head -1 || true)"
	[ -n "$$iso" ] || { echo "Error: no ISO to boot (run 'make iso' first)." >&2; exit 1; }
	# Start every run from a fresh disk so each boot does a clean install. The
	# installer requires >=50 GiB; qcow2 is sparse so 100G costs almost nothing.
	rm -f "$(TEST_DISK)"
	echo ">>> Creating fresh test disk $(TEST_DISK) (100G)"
	qemu-img create -f qcow2 "$(TEST_DISK)" 100G
	kvm=""
	if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
	  kvm="-accel kvm -cpu host"
	else
	  echo ">>> /dev/kvm not usable; running under (slow) emulation"
	fi
	# GUI window if we have a display, otherwise headless over VNC.
	if [ -n "$${DISPLAY:-}" ]; then
	  display="-vga virtio -display gtk"
	else
	  display="-vga virtio -display none -vnc 127.0.0.1:0"
	  echo ">>> Headless: VNC on 127.0.0.1:5900"
	  echo "    Tunnel from your machine:  ssh -L 5900:localhost:5900 <this-host>"
	  echo "    then point a VNC client at localhost:5900"
	fi
	# Boot order is disk-first: an empty disk falls through to the ISO (install),
	# and after install the disk boots the installed system.
	echo ">>> Booting $${iso##*/} in QEMU"
	qemu-system-x86_64 \
	  -name ruddervirt-test \
	  -machine q35 $$kvm \
	  -smp 4 -m 8192 \
	  -drive file="$(TEST_DISK)",if=virtio,format=qcow2 \
	  -cdrom "$$iso" \
	  -boot order=cd \
	  -nic user,model=virtio-net-pci \
	  $$display

ignition:  ## Render server.bu -> out/server.ign (via the Butane container)
	@[ -n "$(RUNTIME)" ] || { echo "Error: neither docker nor podman found in PATH." >&2; exit 1; }
	mkdir -p "$(OUT_DIR)"
	$(RUNTIME) run --rm -i -v "$(CURDIR):/pwd:ro" -w /pwd "$(BUTANE_IMG)" \
	  --files-dir . --strict server.bu > "$(IGNITION)"
	echo ">>> Wrote $(IGNITION)"

test-rootfs: ignition  ## Materialize the server.bu-injected files into out/test-rootfs/
	rm -rf "$(ROOTFS)"
	python3 "$(RENDER)" "$(IGNITION)" "$(ROOTFS)"

clean:  ## Remove build artifacts (ISOs, test disk, ignition, rootfs)
	rm -rf "$(OUT_DIR)"/*.iso "$(OUT_DIR)"/*.qcow2 "$(IGNITION)" "$(ROOTFS)"

build-tui:  ## Build the Go TUI binary
	cd tui && go build -o tea-test setup.go

test-container: test-rootfs  ## Layer 2: open a shell in an FCOS userland with the server.bu files in place
	@[ -n "$(RUNTIME)" ] || { echo "Error: neither docker nor podman found in PATH." >&2; exit 1; }
	# Overlay the rendered server.bu files onto the FCOS image and drop into a shell, so
	# you can inspect a realistic /etc/ruddervirt, /usr/local/bin helpers, etc. tar's
	# --overwrite replaces existing /etc symlinks (resolv.conf, hostname) the way Ignition
	# does, and --keep-directory-symlink descends FCOS's dir symlinks (/usr/local, /opt)
	# instead of erroring on them. -it is added only when attached to a terminal, so this
	# also runs cleanly (overlay then exit) in non-interactive contexts like CI.
	tty=""; [ -t 0 ] && [ -t 1 ] && tty="-it"
	if [ -n "$$tty" ]; then
	  printf '\n\033[1;33m============================================================\033[0m\n'
	  printf '\033[1;33m  ⚠  YOU ARE ENTERING THE TEST CONTAINER\033[0m\n'
	  printf '\033[1;33m  Press Ctrl+D (or type "exit") to leave and return here.\033[0m\n'
	  printf '\033[1;33m============================================================\033[0m\n\n'
	fi
	$(RUNTIME) run --rm $$tty \
	  -v "$(ROOTFS):/rootfs:ro" \
	  "$(TEST_IMG)" \
	  /bin/bash -c 'tar -C /rootfs -cf - . | tar -C / --overwrite --keep-directory-symlink -xf - && exec bash'
