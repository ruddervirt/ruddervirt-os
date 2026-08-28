# SPDX-License-Identifier: GPL-3.0-only
#
# Single entrypoint for RudderVirt OS development (replaces the old local-build.sh).
#
#   make iso              # build the installer ISO -> out/ruddervirt-install-dev-x86_64.iso
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
# TUI_BIN: the Go installer TUI. It's ~10 MB, far past Ignition's 256 KiB
# embed limit, so it can't be inlined into server.ign like a `local:` file.
# Instead `make boot` serves scripts/ over HTTP and server.bu fetches the binary
# from http://10.0.2.2:$(TUI_SERVE_PORT)/ruddervirt-setup on first boot (10.0.2.2
# is the QEMU user-net gateway that maps to the host loopback). It's a real file
# target so make only rebuilds it when its sources change.
TUI_BIN   := scripts/ruddervirt-setup
TUI_SRC   := $(filter-out %_test.go,$(wildcard ruddervirt-setup/*.go)) \
             ruddervirt-setup/go.mod ruddervirt-setup/go.sum \
             $(wildcard ruddervirt-setup/*.yaml) \
             $(shell find ruddervirt-setup/manifests -type f 2>/dev/null)
# Port for the dev binary-serving HTTP server (must match the URL in server.bu).
TUI_SERVE_PORT ?= 8080
# Host port forwarded to the guest's aileron-ui NodePort (30806 - the
# aileron Helm chart's default, see aileronUI.service.nodePort in
# ghcr.io/ruddervirt/charts/aileron's values.yaml). Only reachable once
# "Applying manifests" has installed Aileron.
AILERON_UI_PORT ?= 30806
# BUTANE_IMG: official Butane image, used to render server.bu -> Ignition without
# building the full ISO.
BUTANE_IMG ?= quay.io/coreos/butane:release
RENDER    := scripts/dev/render-ignition-rootfs.py
RUNTIME   := $(shell command -v docker >/dev/null 2>&1 && echo docker || { command -v podman >/dev/null 2>&1 && echo podman; })

SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.ONESHELL:
.DEFAULT_GOAL := help

.PHONY: help iso show-ignition boot ignition test-rootfs test-container clean build-tui test-tui

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

boot: iso $(TUI_BIN)  ## Boot the newest ISO in QEMU (KVM if available; needs qemu, a KVM host)
	command -v qemu-system-x86_64 >/dev/null 2>&1 || { echo "Error: qemu-system-x86_64 not found." >&2; exit 1; }
	iso="$$(ls -t "$(OUT_DIR)"/*.iso 2>/dev/null | head -1 || true)"
	[ -n "$$iso" ] || { echo "Error: no ISO to boot (run 'make iso' first)." >&2; exit 1; }
	# Serve the freshly-built TUI binary to the guest. The binary is too large to
	# embed in Ignition, so server.bu fetches it from
	# http://10.0.2.2:$(TUI_SERVE_PORT)/ruddervirt-setup on first boot. 10.0.2.2 is
	# the QEMU user-net gateway that maps to the host loopback, so binding the
	# server to 127.0.0.1 is enough. Kept alive for the whole run (via the EXIT
	# trap) so it's still up when the installed system's first-boot Ignition runs.
	echo ">>> Serving scripts/ on 127.0.0.1:$(TUI_SERVE_PORT) for guest binary fetch"
	( cd scripts && exec python3 -m http.server "$(TUI_SERVE_PORT)" --bind 127.0.0.1 ) >/dev/null 2>&1 &
	serve_pid=$$!
	trap 'kill $$serve_pid 2>/dev/null || true' EXIT
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
	echo ">>> Aileron UI (once installed) will be reachable at http://localhost:$(AILERON_UI_PORT)"
	qemu-system-x86_64 \
	  -name ruddervirt-test \
	  -machine q35 $$kvm \
	  -smp 8 -m 16384 \
	  -drive file="$(TEST_DISK)",if=virtio,format=qcow2 \
	  -cdrom "$$iso" \
	  -boot order=cd \
	  -nic user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:2222-:22,hostfwd=tcp:127.0.0.1:$(AILERON_UI_PORT)-:30806 \
	  $$display

build-tui: $(TUI_BIN)  ## Build the Go TUI binary (scripts/ruddervirt-setup)

$(TUI_BIN): $(TUI_SRC)
	cd ruddervirt-setup && go build -ldflags "-X main.version=$(VERSION)" -o ../scripts/ruddervirt-setup .

test-tui:  ## Run the Go TUI's unit tests
	cd ruddervirt-setup && go test ./...

ignition:  ## Render server.bu -> out/server.ign (via the Butane container)
	@[ -n "$(RUNTIME)" ] || { echo "Error: neither docker nor podman found in PATH." >&2; exit 1; }
	mkdir -p "$(OUT_DIR)"
	$(RUNTIME) run --rm -i -v "$(CURDIR):/pwd:ro" -w /pwd "$(BUTANE_IMG)" \
	  --files-dir . --strict server.bu > "$(IGNITION)"
	echo ">>> Wrote $(IGNITION)"

test-rootfs: ignition $(TUI_BIN)  ## Materialize the server.bu-injected files into out/test-rootfs/
	rm -rf "$(ROOTFS)"
	# The setup TUI ships via a remote source: URL only reachable during the real
	# boot (the QEMU host loopback), so the renderer would skip it. Substitute the
	# locally-built binary so the test container gets the same admin menu the ISO has.
	LOCAL_FILE_OVERRIDES="/usr/local/bin/ruddervirt-setup=$(TUI_BIN)" \
	  python3 "$(RENDER)" "$(IGNITION)" "$(ROOTFS)"

clean:  ## Remove build artifacts (ISOs, test disk, ignition, rootfs)
	rm -rf "$(OUT_DIR)"/*.iso "$(OUT_DIR)"/*.qcow2 "$(IGNITION)" "$(ROOTFS)"

test-container: test-rootfs  ## Layer 2: open an admin shell in an FCOS userland with the server.bu files in place
	@[ -n "$(RUNTIME)" ] || { echo "Error: neither docker nor podman found in PATH." >&2; exit 1; }
	# Overlay the rendered server.bu files onto the FCOS image and drop into the admin
	# login shell, so you can inspect a realistic /etc/ruddervirt, /usr/local/bin helpers,
	# etc. as the same unprivileged admin user the ISO boots into (not root). tar's
	# --overwrite replaces existing /etc symlinks (resolv.conf, hostname) the way Ignition
	# does, and --keep-directory-symlink descends FCOS's dir symlinks (/usr/local, /opt)
	# instead of erroring on them. -it is added only when attached to a terminal, so this
	# also runs cleanly (overlay then exit) in non-interactive contexts like CI.
	#
	# The ISO's admin/root users live in the Ignition `passwd` section, not in
	# the file overlay, so recreate them here from the *generated* ignition (single source
	# of truth) and hand the list to setup-test-users.sh inside the container. python3 is a
	# host-side dep already (http.server, render); the FCOS image ships no python3. One line
	# per user: name|groups(comma)|shell|password-hash (| avoids read collapsing empty fields).
	users_spec="$$(python3 -c 'import json,sys; users=json.load(open(sys.argv[1])).get("passwd",{}).get("users",[]); print("\n".join("|".join([u.get("name",""), ",".join(u.get("groups",[])), u.get("shell",""), u.get("passwordHash","")]) for u in users if u.get("name")))' "$(IGNITION)")"
	tty=""; [ -t 0 ] && [ -t 1 ] && tty="-it"
	if [ -n "$$tty" ]; then
	  printf '\n\033[1;33m============================================================\033[0m\n'
	  printf '\033[1;33m  ⚠  YOU ARE ENTERING THE TEST CONTAINER (as admin)\033[0m\n'
	  printf '\033[1;33m  Press Ctrl+D (or type "exit") to leave and return here.\033[0m\n'
	  printf '\033[1;33m============================================================\033[0m\n\n'
	fi
	$(RUNTIME) run --rm $$tty \
	  -e USERS_SPEC="$$users_spec" \
	  -v "$(ROOTFS):/rootfs:ro" \
	  -v "$(CURDIR)/scripts/dev/setup-test-users.sh:/setup-test-users.sh:ro" \
	  "$(TEST_IMG)" \
	  /bin/bash -c 'tar -C /rootfs -cf - . | tar -C / --overwrite --keep-directory-symlink -xf - && /setup-test-users.sh && exec runuser -l admin'
