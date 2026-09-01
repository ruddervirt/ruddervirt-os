// SPDX-License-Identifier: GPL-3.0-only

package storage

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
// plus a privileged mkdir -p/mv) so these tests see the same command shape.
func testWritePrivileged(path string, data []byte) error {
	tmp, err := os.CreateTemp("", "ruddervirt-setup-storage-test-*")
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

func TestPrepareLonghornDevice(t *testing.T) {
	const device = "/dev/vda5"

	t.Run("already mounted is a no-op", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "findmnt", "--noheadings", longhornDataPath) {
				return exectest.Outcome{}
			}
			t.Errorf("unexpected command: %s %v", name, args)
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = prepareLonghornDevice(ch, testWrap, testWritePrivileged, device) })
		if err != nil {
			t.Fatalf("prepareLonghornDevice() err = %v, want nil", err)
		}
		if !strings.Contains(strings.Join(drainTestStrings(ch), "\n"), "already mounted") {
			t.Error("expected an 'already mounted' message")
		}
	})

	t.Run("unmounted with no filesystem type reported formats then mounts", func(t *testing.T) {
		// blkid exiting 0 with empty output must still be treated as "no
		// filesystem" - this is the exact shape of the real bug.
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case exectest.CmdContains(name, args, "findmnt"):
				return exectest.Outcome{Err: exectest.ErrFake} // not mounted
			case exectest.CmdContains(name, args, "blkid"):
				return exectest.Outcome{Out: []byte("")} // succeeds, but no TYPE
			default:
				return exectest.Outcome{}
			}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = prepareLonghornDevice(ch, testWrap, testWritePrivileged, device) })
		if err != nil {
			t.Fatalf("prepareLonghornDevice() err = %v, want nil", err)
		}
		found := false
		for _, c := range r.Calls {
			if exectest.CmdContains("", strings.Fields(c), mkfsExt4Bin) {
				found = true
			}
		}
		if !found {
			t.Errorf("expected an mkfs.ext4 call, calls = %v", r.Calls)
		}
	})

	t.Run("unmounted with existing filesystem skips mkfs", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case exectest.CmdContains(name, args, "findmnt"):
				return exectest.Outcome{Err: exectest.ErrFake} // not mounted
			case exectest.CmdContains(name, args, "blkid"):
				return exectest.Outcome{Out: []byte("ext4\n")}
			default:
				return exectest.Outcome{}
			}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = prepareLonghornDevice(ch, testWrap, testWritePrivileged, device) })
		if err != nil {
			t.Fatalf("prepareLonghornDevice() err = %v, want nil", err)
		}
		for _, c := range r.Calls {
			if exectest.CmdContains("", strings.Fields(c), mkfsExt4Bin) {
				t.Errorf("expected no mkfs.ext4 call when a filesystem already exists, calls = %v", r.Calls)
			}
		}
	})
}

func TestPrepareOpenEBSDevice(t *testing.T) {
	const device = "/dev/vda5"

	t.Run("volume group already exists is a no-op", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "vgs", OpenebsVGName) {
				return exectest.Outcome{}
			}
			t.Errorf("unexpected command: %s %v", name, args)
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = prepareOpenEBSDevice(ch, testWrap, device) })
		if err != nil {
			t.Fatalf("prepareOpenEBSDevice() err = %v, want nil", err)
		}
		if !strings.Contains(strings.Join(drainTestStrings(ch), "\n"), "already exists") {
			t.Error("expected an 'already exists' message")
		}
	})

	t.Run("missing volume group creates pv, vg, and thin pool in order", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "vgs") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = prepareOpenEBSDevice(ch, testWrap, device) })
		if err != nil {
			t.Fatalf("prepareOpenEBSDevice() err = %v, want nil", err)
		}
		var seen []string
		for _, c := range r.Calls {
			switch {
			case exectest.CmdContains("", strings.Fields(c), pvcreateBin):
				seen = append(seen, "pvcreate")
			case exectest.CmdContains("", strings.Fields(c), vgcreateBin):
				seen = append(seen, "vgcreate")
			case exectest.CmdContains("", strings.Fields(c), lvcreateBin):
				seen = append(seen, "lvcreate")
			}
		}
		want := []string{"pvcreate", "vgcreate", "lvcreate"}
		if strings.Join(seen, ",") != strings.Join(want, ",") {
			t.Errorf("creation order = %v, want %v", seen, want)
		}
	})
}

func TestPrepareStorageDevice(t *testing.T) {
	t.Run("unknown engine errors", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			// AppliedStorageEngine() reads a real system path that won't
			// exist in the sandbox, so nothing here should ever run for an
			// unrecognized engine.
			t.Errorf("unexpected command: %s %v", name, args)
			return exectest.Outcome{}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = PrepareStorageDevice("made-up-engine", ch, testWrap, testWritePrivileged) })
		if err == nil {
			t.Fatal("PrepareStorageDevice(made-up-engine) err = nil, want an error")
		}
	})

	t.Run("rook-ceph needs no device prep and marks itself applied", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = PrepareStorageDevice("rook-ceph", ch, testWrap, testWritePrivileged) })
		if err != nil {
			t.Fatalf("PrepareStorageDevice(rook-ceph) err = %v, want nil", err)
		}
		if !strings.Contains(strings.Join(drainTestStrings(ch), "\n"), "nothing to prepare") {
			t.Error("expected a 'nothing to prepare' message for rook-ceph")
		}
	})
}
