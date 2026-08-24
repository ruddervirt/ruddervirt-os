// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
)

// errFake is a generic sentinel a fakeRunner's respond function returns to
// simulate a failed command, shared across every Tier-2 test.
var errFake = errors.New("fake: command failed")

// commandOutcome is what a fakeRunner call returns for one invocation.
type commandOutcome struct {
	out []byte
	err error
}

// fakeRunner is a CommandRunner double shared by every Tier-2 test in this
// package. Swap it in via DefaultRunner (restore the original with defer)
// instead of shelling out for real - see withFakeRunner below.
type fakeRunner struct {
	mu    sync.Mutex
	calls []string
	// respond computes the outcome for a given executable+args. A nil
	// respond (or any command it doesn't explicitly handle) defaults to
	// commandOutcome{} - i.e. "succeeds with no output," matching the
	// common Run()-only exit-code-check call sites.
	respond func(name string, args []string) commandOutcome
}

func (f *fakeRunner) cmdLine(name string, args []string) string {
	return strings.Join(append([]string{name}, args...), " ")
}

func (f *fakeRunner) call(name string, args []string) commandOutcome {
	f.mu.Lock()
	f.calls = append(f.calls, f.cmdLine(name, args))
	f.mu.Unlock()
	if f.respond == nil {
		return commandOutcome{}
	}
	return f.respond(name, args)
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	return f.call(name, args).err
}

func (f *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	o := f.call(name, args)
	return o.out, o.err
}

func (f *fakeRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	o := f.call(name, args)
	return o.out, o.err
}

func (f *fakeRunner) CombinedOutputWithStdin(_ context.Context, _ io.Reader, name string, args ...string) ([]byte, error) {
	o := f.call(name, args)
	return o.out, o.err
}

func (f *fakeRunner) Stream(_ context.Context, lines chan<- string, name string, args ...string) error {
	o := f.call(name, args)
	for _, l := range strings.Split(strings.TrimRight(string(o.out), "\n"), "\n") {
		if l != "" {
			lines <- l
		}
	}
	return o.err
}

// withFakeRunner swaps DefaultRunner for r for the duration of fn, always
// restoring the original afterward - every Tier-2 test uses this instead of
// mutating DefaultRunner directly, so tests can never leak a fake runner
// into a later, unrelated test.
func withFakeRunner(r *fakeRunner, fn func()) {
	orig := DefaultRunner
	DefaultRunner = r
	defer func() { DefaultRunner = orig }()
	fn()
}
