// SPDX-License-Identifier: GPL-3.0-only

package manifests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/exec/exectest"
)

// testMsg/testWrap/drainTestStrings stand in for package main's own
// exec.StepMsg type/wrapper/drain helper.
type testMsg string

func testWrap(line string) exec.StepMsg { return testMsg(line) }

func drainTestStrings(ch chan exec.StepMsg) []string {
	var out []string
	for {
		select {
		case msg := <-ch:
			if s, ok := msg.(testMsg); ok {
				out = append(out, string(s))
			}
		default:
			return out
		}
	}
}

// testWritePrivileged mirrors internal/config.WritePrivileged (temp file
// plus a privileged mkdir -p/mv) so tests see the same "mv" command shape.
func testWritePrivileged(path string, data []byte) error {
	tmp, err := os.CreateTemp("", "ruddervirt-setup-manifests-test-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}
	if out, err := exec.RunPrivileged("/usr/bin/mkdir", "-p", filepath.Dir(path)).CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	if out, err := exec.RunPrivileged("/usr/bin/mv", tmpPath, path).CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	return nil
}

func TestWriteStorageManifests(t *testing.T) {
	t.Run("openebs writes every embedded file to /etc/ruddervirt/manifests", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = WriteStorageManifests(ch, testWrap, testWritePrivileged, "openebs") })
		if err != nil {
			t.Fatalf("WriteStorageManifests(openebs) err = %v, want nil", err)
		}
		want := []string{
			"/etc/ruddervirt/manifests/openebs/base/openebs-operator.yaml",
			"/etc/ruddervirt/manifests/openebs/base/storage-profile.yaml",
			"/etc/ruddervirt/manifests/openebs/snapshot-class.yaml",
		}
		for _, dest := range want {
			found := false
			for _, c := range r.Calls {
				if exectest.CmdContains("", strings.Fields(c), "mv", dest) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("no privileged mv to %q among calls %v", dest, r.Calls)
			}
		}
	})

	t.Run("unknown engine errors without touching the runner or ch", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			t.Errorf("unexpected command for unknown engine: %s %v", name, args)
			return exectest.Outcome{}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = WriteStorageManifests(ch, testWrap, testWritePrivileged, "made-up-engine") })
		if err == nil {
			t.Fatal("WriteStorageManifests(made-up-engine) err = nil, want an error")
		}
		if len(drainTestStrings(ch)) != 0 {
			t.Error("expected no ch messages for an unknown engine")
		}
	})
}

func TestWriteManifestFile(t *testing.T) {
	t.Run("writes the embedded file to /etc/ruddervirt/manifests", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = WriteManifestFile(ch, testWrap, testWritePrivileged, "kube-ovn.yaml") })
		if err != nil {
			t.Fatalf("WriteManifestFile(kube-ovn.yaml) err = %v, want nil", err)
		}
		const dest = "/etc/ruddervirt/manifests/kube-ovn.yaml"
		found := false
		for _, c := range r.Calls {
			if exectest.CmdContains("", strings.Fields(c), "mv", dest) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no privileged mv to %q among calls %v", dest, r.Calls)
		}
	})

	t.Run("unknown file errors without touching the runner", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			t.Errorf("unexpected command for unknown file: %s %v", name, args)
			return exectest.Outcome{}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = WriteManifestFile(ch, testWrap, testWritePrivileged, "made-up.yaml") })
		if err == nil {
			t.Fatal("WriteManifestFile(made-up.yaml) err = nil, want an error")
		}
	})
}

// TestManifestVersionPlaceholders guards against a manual YAML edit (e.g. a
// typo'd placeholder) silently leaving spec.version as a literal string
// instead of a real chart version, which helm-controller would then fail
// to resolve.
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
