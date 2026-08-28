// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOSUpdateSteps(t *testing.T) {
	t.Run("has exactly one step, running upgrade --bypass-driver", func(t *testing.T) {
		if len(osUpdateSteps) != 1 {
			t.Fatalf("got %d steps, want 1", len(osUpdateSteps))
		}
	})

	t.Run("runs rpm-ostree upgrade --bypass-driver and reports success", func(t *testing.T) {
		var sawCall bool
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "upgrade", "--bypass-driver") {
				sawCall = true
			}
			return commandOutcome{}
		}}
		ch := make(chan tea.Msg, 100)
		withFakeRunner(r, func() { osUpdateSteps[0].run(Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.err != nil {
			t.Fatalf("err = %v, want nil", done.err)
		}
		if !sawCall {
			t.Error("never called rpm-ostree upgrade --bypass-driver")
		}
	})

	t.Run("propagates a failure", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte("boom"), err: errFake}
		}}
		ch := make(chan tea.Msg, 100)
		withFakeRunner(r, func() { osUpdateSteps[0].run(Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.err == nil {
			t.Fatal("err = nil, want non-nil")
		}
	})
}

func TestOSUpdateAvailable(t *testing.T) {
	t.Run("AvailableUpdate stanza in status output means true", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "status") {
				return commandOutcome{out: []byte("State: idle\nAvailableUpdate:\n  Version: 40.1\n")}
			}
			return commandOutcome{}
		}}
		var got bool
		withFakeRunner(r, func() { got = osUpdateAvailable() })
		if !got {
			t.Error("want true when status output contains AvailableUpdate")
		}
	})

	t.Run("no stanza means false", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "status") {
				return commandOutcome{out: []byte("State: idle\n")}
			}
			return commandOutcome{}
		}}
		var got bool
		withFakeRunner(r, func() { got = osUpdateAvailable() })
		if got {
			t.Error("want false with no AvailableUpdate stanza")
		}
	})

	t.Run("status failure (not FCOS, no binary, etc) is false, not a crash", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte("command not found"), err: errFake}
		}}
		var got bool
		withFakeRunner(r, func() { got = osUpdateAvailable() })
		if got {
			t.Error("want false on failure")
		}
	})

	t.Run("uses non-interactive (sudo -n) calls, never risking a password prompt", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome { return commandOutcome{} }}
		withFakeRunner(r, func() { osUpdateAvailable() })
		if r.calls == nil {
			t.Fatal("no calls recorded")
		}
		for _, c := range r.calls {
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
		if got := currentOSVersionFromPath(path); got != "39.20240101.3.0" {
			t.Errorf("got %q, want 39.20240101.3.0", got)
		}
	})

	t.Run("strips surrounding quotes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "os-release")
		if err := os.WriteFile(path, []byte(`VERSION_ID="39.1"`+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if got := currentOSVersionFromPath(path); got != "39.1" {
			t.Errorf("got %q, want 39.1", got)
		}
	})

	t.Run("missing file returns empty, not an error", func(t *testing.T) {
		if got := currentOSVersionFromPath("/does/not/exist"); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("no VERSION_ID line returns empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "os-release")
		if err := os.WriteFile(path, []byte("NAME=Fedora\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if got := currentOSVersionFromPath(path); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}
