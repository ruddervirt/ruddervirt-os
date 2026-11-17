// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"os"
	"strings"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/k3s"
	"ruddervirt-setup/internal/status"
	"ruddervirt-setup/internal/storage"
)

// prepareStorageStep adapts storage.PrepareStorageDevice to the installsteps.Step
// shape (install_steps.go), which - like every installsteps.Step - only accepts
// (cfg config.Config, ch chan<- installsteps.StepMsg).
func prepareStorageStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Preparing storage device"

	// The storage engine is cluster-wide, but this step formats THIS host's
	// disk for it, so a joining node whose config disagrees with the cluster
	// would prepare a layout the cluster can't use - then sit in "Waiting for
	// all services to become ready" for 30 minutes against a namespace that
	// doesn't exist. This step runs after "Enabling and starting k3s", so by
	// now a joining node has joined and the cluster can simply be asked.
	//
	// Refusing here is the only moment it's cheap: PrepareStorageDevice
	// writes the engine marker, which locks the storage.engine SettingField
	// (see config.go) and makes an OS reinstall the only way back. Detection
	// failing (no engine installed yet - the normal case while the first node
	// bootstraps - or no reachable cluster) falls through to the configured
	// engine.
	//
	// status.K3sServiceActive is a mandatory gate before trusting ANY kubectl
	// result below, not an optimization: until k3s is installed
	// /usr/local/bin/k3s is a shebang-less placeholder (see server.bu), and
	// the kubectl wrapper execs it - POSIX shell's ENOEXEC fallback then runs
	// it as an empty script, so kubectl exits 0 and every `get` reads as a
	// SUCCESS. internal/status documents the same trap for the home screen.
	// Without this, a false success would make DetectClusterEngine name an
	// engine at random and refuse a perfectly good node.
	if status.K3sServiceActive() {
		if detected, ok := storage.DetectClusterEngine(kubectlBinPath); ok && detected != cfg.Storage.Engine {
			ch <- stepDoneMsg{Label: label, Err: fmt.Errorf(
				"this cluster already runs the %q storage engine, but this node is configured for %q - set \"Storage engine\" to %q under Settings -> Advanced, then re-apply",
				detected, cfg.Storage.Engine, detected)}
			return
		}

		// Preparing the disk is pointless on a node no CSI node plugin will
		// ever schedule onto, and nothing downstream notices: the engine's
		// pods are Ready on the other nodes, so "Waiting for all services to
		// become ready" passes and the install reports success while this
		// host silently contributes no storage at all. Only a verdict the
		// cluster actually gave us (ok) counts - an unregistered node or an
		// unreachable cluster falls through.
		if node, err := os.Hostname(); err == nil {
			name := strings.ToLower(strings.TrimSpace(node))
			if labelled, ok := storage.NodeIsStorageCapable(kubectlBinPath, name); ok && !labelled {
				ch <- stepDoneMsg{Label: label, Err: fmt.Errorf(
					"node %q is missing the %s=%s label, so no storage engine's CSI node plugin will run on it and preparing this disk would achieve nothing - add %q to node-label in %s and reinstall, or label the already-registered node now with: sudo %s label node %s %s=%s",
					name, storage.MetalNodeLabelKey, storage.MetalNodeLabelValue,
					storage.MetalNodeLabelKey+"="+storage.MetalNodeLabelValue, k3s.ClusterConfigPath,
					kubectlBinPath, name, storage.MetalNodeLabelKey, storage.MetalNodeLabelValue)}
				return
			}
		}
	}

	ch <- stepDoneMsg{Label: label, Err: storage.PrepareStorageDevice(cfg.Storage.Engine, ch, wrapStepOutput, config.WritePrivileged)}
}

// planStorageDevice previews prepareStorageStep, reusing the same
// storage.AppliedStorageEngine() marker check. Reports BLOCKED rather than a
// false "will prepare disk for X" if a hand-edited config disagrees with the
// disk's locked engine - Settings already prevents picking a different
// engine once the marker exists (config.go), but the preview stays honest
// regardless of how cfg got here.
func planStorageDevice(cfg config.Config) string {
	if applied, err := storage.AppliedStorageEngine(); err == nil {
		if applied == cfg.Storage.Engine {
			return fmt.Sprintf("skip - storage device already prepared for %s", cfg.Storage.Engine)
		}
		return fmt.Sprintf("BLOCKED - disk locked to %s (reinstall OS to switch to %s)", applied, cfg.Storage.Engine)
	}
	return fmt.Sprintf("will prepare disk for %s", cfg.Storage.Engine)
}
