// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// manifestsFS embeds this repo's own copy of every solution manifest
// ruddervirt-setup applies (ruddervirt-setup/manifests/), baked into the
// binary at build time. Nothing under /etc/ruddervirt/manifests is
// provisioned by Ignition (see server.bu) - Ignition only ever runs once,
// at first boot, so a one-time copy would go stale the moment any of these
// manifests changes in a later ruddervirt-setup release. Every one of them
// is instead written fresh from this embed immediately before it's used
// (writeManifestFile for a single file, writeStorageManifests for a whole
// engine's directory tree) - see their call sites in k3s.go and
// aileron.go - so an already-provisioned host picks up a change the same
// way a ruddervirt-setup self-update already does: bump the file here, cut
// a release, operator re-applies. The ruddervirt-setup binary itself is
// always fetched fresh (never baked into the ISO - see server.bu's
// /usr/local/bin/ruddervirt-setup entry), so it's the one source these can
// never go stale against.
//
//go:embed manifests
var manifestsFS embed.FS

// writeManifestFile writes /etc/ruddervirt/manifests/<relPath> from this
// binary's embedded copy of ruddervirt-setup/manifests/<relPath>, before a
// caller reads it from disk (applyKubeOvn, applyAileron, prepareK3sStep's
// static kubectl apply -f manifests).
func writeManifestFile(ch chan<- tea.Msg, relPath string) error {
	data, err := manifestsFS.ReadFile("manifests/" + relPath)
	if err != nil {
		return fmt.Errorf("writing %s: %w", relPath, err)
	}
	ch <- stepOutputMsg(fmt.Sprintf("Writing %s...", relPath))
	return writePrivileged("/etc/ruddervirt/manifests/"+relPath, data)
}

// writeStorageManifests writes /etc/ruddervirt/manifests/<engine> from this
// binary's embedded copy before applyStorageEngine's kubectl apply -k reads
// it from disk. engine must be one of the storage engine directories
// embedded under manifests/ ("openebs", "longhorn", "rook-ceph") - anything
// else returns an error without sending to ch or touching the runner,
// matching applyStorageEngine's own "unknown storage engine" behavior for a
// bad engine name.
func writeStorageManifests(ch chan<- tea.Msg, engine string) error {
	srcRoot := "manifests/" + engine
	if _, err := fs.Stat(manifestsFS, srcRoot); err != nil {
		return fmt.Errorf("unknown storage engine %q", engine)
	}

	ch <- stepOutputMsg(fmt.Sprintf("Writing %s manifests...", engine))
	return fs.WalkDir(manifestsFS, srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := manifestsFS.ReadFile(path)
		if err != nil {
			return err
		}
		// path is rooted at "manifests/..." (the go:embed prefix above) -
		// strip that so the destination lands at
		// /etc/ruddervirt/manifests/<engine>/... , matching where
		// applyGenericStorageEngine/applyRookCeph expect to kubectl apply -k
		// it from.
		rel := strings.TrimPrefix(path, "manifests/")
		return writePrivileged("/etc/ruddervirt/manifests/"+rel, data)
	})
}
