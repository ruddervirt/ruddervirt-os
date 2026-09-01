// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/exec/exectest"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/stabilizer"
)

// lastStepDone drains ch and returns the final stepDoneMsg, failing the
// test if none arrived. Used by this file and by
// stabilizer_settings_tui_test.go/stabilizer_version_tui_test.go, which
// drive installsteps.Step-shaped functions directly.
func lastStepDone(t *testing.T, ch chan installsteps.StepMsg) stepDoneMsg {
	t.Helper()
	var last *stepDoneMsg
	for {
		select {
		case msg := <-ch:
			if d, ok := msg.(stepDoneMsg); ok {
				m := d
				last = &m
			}
		default:
			if last == nil {
				t.Fatal("no stepDoneMsg was sent")
			}
			return *last
		}
	}
}

func TestCheckAileronReadyCmd(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case cmdContains(name, args, "systemctl", "is-active", "k3s.service"):
				return exectest.Outcome{}
			case cmdContains(name, args, "wait", "deployment.apps/aileron"):
				return exectest.Outcome{}
			}
			t.Errorf("unexpected command: %s %v", name, args)
			return exectest.Outcome{}
		}}
		var msg tea.Msg
		exectest.WithFakeRunner(r, func() { msg = checkAileronReadyCmd("/usr/local/bin/kubectl")() })
		got, ok := msg.(aileronReadyCheckMsg)
		if !ok || !got.ready {
			t.Errorf("checkAileronReadyCmd() = %#v, want aileronReadyCheckMsg{ready: true}", msg)
		}
	})

	t.Run("not ready", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		var msg tea.Msg
		exectest.WithFakeRunner(r, func() { msg = checkAileronReadyCmd("/usr/local/bin/kubectl")() })
		got, ok := msg.(aileronReadyCheckMsg)
		if !ok || got.ready {
			t.Errorf("checkAileronReadyCmd() = %#v, want aileronReadyCheckMsg{ready: false}", msg)
		}
	})

	// Regression test: before k3s is installed, /usr/local/bin/k3s is only a
	// placeholder text file with no `#!` shebang (server.bu). POSIX shell's
	// ENOEXEC fallback silently reinterprets that as a no-op script, so
	// kubectl (execing through it) reports a false SUCCESS - a bare `wait
	// deployment.apps/aileron` call alone would wrongly report "ready" on a
	// node never installed. k3sServiceActive() must be checked first.
	t.Run("kubectl false-succeeds via the k3s placeholder script but k3s.service isn't active", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if cmdContains(name, args, "systemctl", "is-active", "k3s.service") {
				return exectest.Outcome{Err: exectest.ErrFake} // real environment: not active
			}
			// Simulates the placeholder-script trap: any kubectl call
			// "succeeds" with no real effect.
			return exectest.Outcome{}
		}}
		var msg tea.Msg
		exectest.WithFakeRunner(r, func() { msg = checkAileronReadyCmd("/usr/local/bin/kubectl")() })
		got, ok := msg.(aileronReadyCheckMsg)
		if !ok || got.ready {
			t.Errorf("checkAileronReadyCmd() = %#v, want ready=false when k3s.service isn't active, even though kubectl itself reports success", msg)
		}
	})
}

// Compile-time sanity: stabilizer.AdoptSteps must never accidentally get
// appended to the global fresh-install pipeline.
func TestAdoptStepsIsSeparateFromInstallSteps(t *testing.T) {
	if len(stabilizer.AdoptSteps) == 0 {
		t.Fatal("stabilizer.AdoptSteps is empty")
	}
	for _, s := range installSteps {
		for _, ss := range stabilizer.AdoptSteps {
			if s.Label == ss.Label {
				t.Errorf("stabilizer.AdoptSteps step %q also appears in the global installSteps pipeline", ss.Label)
			}
		}
	}
}
