// SPDX-License-Identifier: GPL-3.0-only

// Package exectest provides a CommandRunner test double
// (internal/exec.CommandRunner) shared by every Tier-2 test across this
// module.
package exectest

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	"ruddervirt-setup/internal/exec"
)

// ErrFake is a generic sentinel a FakeRunner's Respond function returns to
// simulate a failed command, shared across every Tier-2 test.
var ErrFake = errors.New("fake: command failed")

// Outcome is what a FakeRunner call returns for one invocation.
type Outcome struct {
	Out []byte
	Err error
}

// FakeRunner is a exec.CommandRunner double shared by every Tier-2 test in
// this module - see WithFakeRunner below.
type FakeRunner struct {
	mu    sync.Mutex
	Calls []string
	// Respond computes the outcome for a given executable+args. A nil
	// Respond (or an unhandled command) defaults to Outcome{}, i.e.
	// "succeeds with no output."
	Respond func(name string, args []string) Outcome
}

func (f *FakeRunner) cmdLine(name string, args []string) string {
	return strings.Join(append([]string{name}, args...), " ")
}

func (f *FakeRunner) call(name string, args []string) Outcome {
	f.mu.Lock()
	f.Calls = append(f.Calls, f.cmdLine(name, args))
	f.mu.Unlock()
	if f.Respond == nil {
		return Outcome{}
	}
	return f.Respond(name, args)
}

func (f *FakeRunner) Run(_ context.Context, name string, args ...string) error {
	return f.call(name, args).Err
}

func (f *FakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	o := f.call(name, args)
	return o.Out, o.Err
}

func (f *FakeRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	o := f.call(name, args)
	return o.Out, o.Err
}

func (f *FakeRunner) CombinedOutputWithStdin(_ context.Context, _ io.Reader, name string, args ...string) ([]byte, error) {
	o := f.call(name, args)
	return o.Out, o.Err
}

func (f *FakeRunner) Stream(_ context.Context, lines chan<- string, name string, args ...string) error {
	o := f.call(name, args)
	for _, l := range strings.Split(strings.TrimRight(string(o.Out), "\n"), "\n") {
		if l != "" {
			lines <- l
		}
	}
	return o.Err
}

// WithFakeRunner swaps exec.DefaultRunner for r during fn, always restoring
// the original after - prevents a fake runner leaking into a later test.
func WithFakeRunner(r *FakeRunner, fn func()) {
	orig := exec.DefaultRunner
	exec.DefaultRunner = r
	defer func() { exec.DefaultRunner = orig }()
	fn()
}

// CmdContains reports whether every one of substrs appears somewhere in
// name+args joined by spaces - used by fake Respond functions to key off a
// command's shape without hardcoding the exact sudo/-n wrapping the real
// runner applies.
func CmdContains(name string, args []string, substrs ...string) bool {
	line := strings.Join(append([]string{name}, args...), " ")
	for _, s := range substrs {
		if !strings.Contains(line, s) {
			return false
		}
	}
	return true
}

// HasField reports whether tok appears as an exact space-separated token in
// line - unlike CmdContains, which does substring matching and so would
// wrongly match "add" inside "ipv4.addresses".
func HasField(line, tok string) bool {
	for _, f := range strings.Fields(line) {
		if f == tok {
			return true
		}
	}
	return false
}
