// SPDX-License-Identifier: GPL-3.0-only

package manifests

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"ruddervirt-setup/internal/exec"
)

// manifestsFS embeds this repo's own copy of every solution manifest
// ruddervirt-setup applies (ruddervirt-setup/internal/manifests/manifests/),
// baked into the binary at build time. Nothing under
// /etc/ruddervirt/manifests is provisioned by Ignition (see server.bu) -
// Ignition runs only once, at first boot, so a one-time copy would go stale
// the moment a manifest changes in a later release. Instead each is written
// fresh from this embed immediately before use (WriteManifestFile for a
// single file, WriteStorageManifests for a whole engine tree - see their
// call sites in k3s.go and aileron.go), so an already-provisioned host
// picks up a change the same way a self-update does.
//
//go:embed manifests
var manifestsFS embed.FS

// WriteManifestFile writes /etc/ruddervirt/manifests/<relPath> from this
// binary's embedded copy of ruddervirt-setup/internal/manifests/manifests/
// <relPath>, before a caller reads it from disk (applyKubeOvn, applyAileron,
// prepareK3sStep's static kubectl apply -f manifests). wrap adapts a
// progress line into the caller's own tea.Msg type, and write is the
// caller's privileged file-write primitive (internal/config.WritePrivileged)
// - this package has neither of its own to hardcode.
func WriteManifestFile(ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, write func(path string, data []byte) error, relPath string) error {
	data, err := manifestsFS.ReadFile("manifests/" + relPath)
	if err != nil {
		return fmt.Errorf("writing %s: %w", relPath, err)
	}
	ch <- wrap(fmt.Sprintf("Writing %s...", relPath))
	return write("/etc/ruddervirt/manifests/"+relPath, data)
}

// WriteStorageManifests writes /etc/ruddervirt/manifests/<engine> from this
// binary's embedded copy before applyStorageEngine's kubectl apply -k reads
// it from disk. engine must be one of the storage engine directories
// embedded under manifests/ ("openebs", "longhorn", "rook-ceph") - anything
// else returns an error without sending to ch or calling write, matching
// applyStorageEngine's own "unknown storage engine" behavior. wrap/write
// are the same caller-supplied primitives as WriteManifestFile.
func WriteStorageManifests(ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, write func(path string, data []byte) error, engine string) error {
	srcRoot := "manifests/" + engine
	if _, err := fs.Stat(manifestsFS, srcRoot); err != nil {
		return fmt.Errorf("unknown storage engine %q", engine)
	}

	ch <- wrap(fmt.Sprintf("Writing %s manifests...", engine))
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
		// Strip the "manifests/" go:embed prefix so the destination lands
		// at /etc/ruddervirt/manifests/<engine>/..., where
		// applyGenericStorageEngine/applyRookCeph expect to kubectl apply -k it.
		rel := strings.TrimPrefix(path, "manifests/")
		return write("/etc/ruddervirt/manifests/"+rel, data)
	})
}
