// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestWriteStorageManifests(t *testing.T) {
	t.Run("openebs writes every embedded file to /etc/ruddervirt/manifests", func(t *testing.T) {
		r := &fakeRunner{}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = writeStorageManifests(ch, "openebs") })
		if err != nil {
			t.Fatalf("writeStorageManifests(openebs) err = %v, want nil", err)
		}
		want := []string{
			"/etc/ruddervirt/manifests/openebs/base/openebs-operator.yaml",
			"/etc/ruddervirt/manifests/openebs/base/storage-profile.yaml",
			"/etc/ruddervirt/manifests/openebs/snapshot-class.yaml",
		}
		for _, dest := range want {
			found := false
			for _, c := range r.calls {
				if cmdContains("", strings.Fields(c), "mv", dest) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("no privileged mv to %q among calls %v", dest, r.calls)
			}
		}
	})

	t.Run("unknown engine errors without touching the runner or ch", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			t.Errorf("unexpected command for unknown engine: %s %v", name, args)
			return commandOutcome{}
		}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = writeStorageManifests(ch, "made-up-engine") })
		if err == nil {
			t.Fatal("writeStorageManifests(made-up-engine) err = nil, want an error")
		}
		if len(drainStrings(ch)) != 0 {
			t.Error("expected no ch messages for an unknown engine")
		}
	})
}

func TestWriteManifestFile(t *testing.T) {
	t.Run("writes the embedded file to /etc/ruddervirt/manifests", func(t *testing.T) {
		r := &fakeRunner{}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = writeManifestFile(ch, "kube-ovn.yaml") })
		if err != nil {
			t.Fatalf("writeManifestFile(kube-ovn.yaml) err = %v, want nil", err)
		}
		const dest = "/etc/ruddervirt/manifests/kube-ovn.yaml"
		found := false
		for _, c := range r.calls {
			if cmdContains("", strings.Fields(c), "mv", dest) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no privileged mv to %q among calls %v", dest, r.calls)
		}
	})

	t.Run("unknown file errors without touching the runner", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			t.Errorf("unexpected command for unknown file: %s %v", name, args)
			return commandOutcome{}
		}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = writeManifestFile(ch, "made-up.yaml") })
		if err == nil {
			t.Fatal("writeManifestFile(made-up.yaml) err = nil, want an error")
		}
	})
}

// TestManifestVersionPlaceholders is a regression guard: applyKubeOvn/
// applyMultus (k3s.go/multus.go) only work if their manifest templates
// still declare these exact placeholders - a manual edit to either YAML
// file (e.g. reverting back to a hardcoded version, or a typo in the
// placeholder name) would silently leave spec.version as the literal
// placeholder string instead of a real chart version, which
// helm-controller would then fail to resolve.
func TestManifestVersionPlaceholders(t *testing.T) {
	cases := []struct {
		file        string
		placeholder string
	}{
		{"kube-ovn.yaml", "__KUBE_OVN_VERSION__"},
		{"multus.yaml", "__MULTUS_VERSION__"},
	}
	for _, c := range cases {
		data, err := manifestsFS.ReadFile("manifests/" + c.file)
		if err != nil {
			t.Fatalf("reading manifests/%s: %v", c.file, err)
		}
		if !strings.Contains(string(data), c.placeholder) {
			t.Errorf("manifests/%s doesn't contain %s", c.file, c.placeholder)
		}
	}
}
