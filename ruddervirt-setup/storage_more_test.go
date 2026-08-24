// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPrepareLonghornDevice(t *testing.T) {
	const device = "/dev/vda5"

	t.Run("already mounted is a no-op", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "findmnt", "--noheadings", longhornDataPath) {
				return commandOutcome{}
			}
			t.Errorf("unexpected command: %s %v", name, args)
			return commandOutcome{err: errFake}
		}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = prepareLonghornDevice(ch, device) })
		if err != nil {
			t.Fatalf("prepareLonghornDevice() err = %v, want nil", err)
		}
		if !strings.Contains(strings.Join(drainStrings(ch), "\n"), "already mounted") {
			t.Error("expected an 'already mounted' message")
		}
	})

	t.Run("unmounted with no filesystem type reported formats then mounts", func(t *testing.T) {
		// blkid succeeding with empty output (e.g. it can only read the
		// partition table's PARTUUID, no TYPE tag) must still be treated as
		// "no filesystem" - this is the exact shape of the real bug: plain
		// blkid exits 0 on a bare, unformatted partition.
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			switch {
			case cmdContains(name, args, "findmnt"):
				return commandOutcome{err: errFake} // not mounted
			case cmdContains(name, args, "blkid"):
				return commandOutcome{out: []byte("")} // succeeds, but no TYPE
			default:
				return commandOutcome{}
			}
		}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = prepareLonghornDevice(ch, device) })
		if err != nil {
			t.Fatalf("prepareLonghornDevice() err = %v, want nil", err)
		}
		found := false
		for _, c := range r.calls {
			if cmdContains("", strings.Fields(c), mkfsExt4Bin) {
				found = true
			}
		}
		if !found {
			t.Errorf("expected an mkfs.ext4 call, calls = %v", r.calls)
		}
	})

	t.Run("unmounted with existing filesystem skips mkfs", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			switch {
			case cmdContains(name, args, "findmnt"):
				return commandOutcome{err: errFake} // not mounted
			case cmdContains(name, args, "blkid"):
				return commandOutcome{out: []byte("ext4\n")}
			default:
				return commandOutcome{}
			}
		}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = prepareLonghornDevice(ch, device) })
		if err != nil {
			t.Fatalf("prepareLonghornDevice() err = %v, want nil", err)
		}
		for _, c := range r.calls {
			if cmdContains("", strings.Fields(c), mkfsExt4Bin) {
				t.Errorf("expected no mkfs.ext4 call when a filesystem already exists, calls = %v", r.calls)
			}
		}
	})
}

func TestPrepareOpenEBSDevice(t *testing.T) {
	const device = "/dev/vda5"

	t.Run("volume group already exists is a no-op", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "vgs", openebsVGName) {
				return commandOutcome{}
			}
			t.Errorf("unexpected command: %s %v", name, args)
			return commandOutcome{err: errFake}
		}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = prepareOpenEBSDevice(ch, device) })
		if err != nil {
			t.Fatalf("prepareOpenEBSDevice() err = %v, want nil", err)
		}
		if !strings.Contains(strings.Join(drainStrings(ch), "\n"), "already exists") {
			t.Error("expected an 'already exists' message")
		}
	})

	t.Run("missing volume group creates pv, vg, and thin pool in order", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "vgs") {
				return commandOutcome{err: errFake}
			}
			return commandOutcome{}
		}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = prepareOpenEBSDevice(ch, device) })
		if err != nil {
			t.Fatalf("prepareOpenEBSDevice() err = %v, want nil", err)
		}
		var seen []string
		for _, c := range r.calls {
			switch {
			case cmdContains("", strings.Fields(c), pvcreateBin):
				seen = append(seen, "pvcreate")
			case cmdContains("", strings.Fields(c), vgcreateBin):
				seen = append(seen, "vgcreate")
			case cmdContains("", strings.Fields(c), lvcreateBin):
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
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			// appliedStorageEngine() reads a real system path that won't
			// exist in the test sandbox, so this always falls through to
			// the switch - nothing here should ever be called for an
			// unrecognized engine.
			t.Errorf("unexpected command: %s %v", name, args)
			return commandOutcome{}
		}}
		cfg := Config{Storage: StorageConfig{Engine: "made-up-engine"}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = prepareStorageDevice(cfg, ch) })
		if err == nil {
			t.Fatal("prepareStorageDevice(made-up-engine) err = nil, want an error")
		}
	})

	t.Run("rook-ceph needs no device prep and marks itself applied", func(t *testing.T) {
		r := &fakeRunner{}
		cfg := Config{Storage: StorageConfig{Engine: "rook-ceph"}}
		ch := make(chan tea.Msg, 100)
		var err error
		withFakeRunner(r, func() { err = prepareStorageDevice(cfg, ch) })
		if err != nil {
			t.Fatalf("prepareStorageDevice(rook-ceph) err = %v, want nil", err)
		}
		if !strings.Contains(strings.Join(drainStrings(ch), "\n"), "nothing to prepare") {
			t.Error("expected a 'nothing to prepare' message for rook-ceph")
		}
	})
}
