// SPDX-License-Identifier: GPL-3.0-only

package osupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec/exectest"
	"ruddervirt-setup/internal/installsteps"
)

// lastStepDone drains ch and returns the final installsteps.StepDoneMsg,
// failing the test if none arrived - a package-local copy of package main's
// adopt_test.go helper, since this package can't import package main.
func lastStepDone(t *testing.T, ch chan installsteps.StepMsg) installsteps.StepDoneMsg {
	t.Helper()
	var last *installsteps.StepDoneMsg
	for {
		select {
		case msg := <-ch:
			if d, ok := msg.(installsteps.StepDoneMsg); ok {
				m := d
				last = &m
			}
		default:
			if last == nil {
				t.Fatal("no StepDoneMsg was sent")
			}
			return *last
		}
	}
}

func TestOSUpdateSteps(t *testing.T) {
	t.Run("has exactly one step, running upgrade --bypass-driver", func(t *testing.T) {
		if len(OSUpdateSteps) != 1 {
			t.Fatalf("got %d steps, want 1", len(OSUpdateSteps))
		}
	})

	t.Run("runs rpm-ostree upgrade --bypass-driver and reports success", func(t *testing.T) {
		var sawCall bool
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "upgrade", "--bypass-driver") {
				sawCall = true
			}
			return exectest.Outcome{}
		}}
		ch := make(chan installsteps.StepMsg, 100)
		exectest.WithFakeRunner(r, func() { OSUpdateSteps[0].Run(config.Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.Err != nil {
			t.Fatalf("err = %v, want nil", done.Err)
		}
		if !sawCall {
			t.Error("never called rpm-ostree upgrade --bypass-driver")
		}
	})

	t.Run("propagates a failure", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("boom"), Err: exectest.ErrFake}
		}}
		ch := make(chan installsteps.StepMsg, 100)
		exectest.WithFakeRunner(r, func() { OSUpdateSteps[0].Run(config.Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.Err == nil {
			t.Fatal("err = nil, want non-nil")
		}
	})
}

func TestOSUpdateAvailable(t *testing.T) {
	t.Run("AvailableUpdate stanza in status output means true", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "status") {
				return exectest.Outcome{Out: []byte("State: idle\nAvailableUpdate:\n  Version: 40.1\n")}
			}
			return exectest.Outcome{}
		}}
		var got bool
		exectest.WithFakeRunner(r, func() { got = OSUpdateAvailable() })
		if !got {
			t.Error("want true when status output contains AvailableUpdate")
		}
	})

	t.Run("no stanza means false", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "status") {
				return exectest.Outcome{Out: []byte("State: idle\n")}
			}
			return exectest.Outcome{}
		}}
		var got bool
		exectest.WithFakeRunner(r, func() { got = OSUpdateAvailable() })
		if got {
			t.Error("want false with no AvailableUpdate stanza")
		}
	})

	t.Run("status failure (not FCOS, no binary, etc) is false, not a crash", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("command not found"), Err: exectest.ErrFake}
		}}
		var got bool
		exectest.WithFakeRunner(r, func() { got = OSUpdateAvailable() })
		if got {
			t.Error("want false on failure")
		}
	})

	t.Run("uses non-interactive (sudo -n) calls, never risking a password prompt", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome { return exectest.Outcome{} }}
		exectest.WithFakeRunner(r, func() { OSUpdateAvailable() })
		if r.Calls == nil {
			t.Fatal("no calls recorded")
		}
		for _, c := range r.Calls {
			if !strings.Contains(c, "sudo -n") {
				t.Errorf("call %q didn't use non-interactive sudo (-n)", c)
			}
		}
	})
}

func TestCurrentOSVersion(t *testing.T) {
	t.Run("reads VERSION_ID from a well-formed file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "os-release")
		if err := os.WriteFile(path, []byte("NAME=Fedora\nVERSION_ID=39.20240101.3.0\nID=fedora\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if got := CurrentOSVersionFromPath(path); got != "39.20240101.3.0" {
			t.Errorf("got %q, want 39.20240101.3.0", got)
		}
	})

	t.Run("strips surrounding quotes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "os-release")
		if err := os.WriteFile(path, []byte(`VERSION_ID="39.1"`+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if got := CurrentOSVersionFromPath(path); got != "39.1" {
			t.Errorf("got %q, want 39.1", got)
		}
	})

	t.Run("missing file returns empty, not an error", func(t *testing.T) {
		if got := CurrentOSVersionFromPath("/does/not/exist"); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("no VERSION_ID line returns empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "os-release")
		if err := os.WriteFile(path, []byte("NAME=Fedora\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if got := CurrentOSVersionFromPath(path); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}
