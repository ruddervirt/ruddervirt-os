// SPDX-License-Identifier: GPL-3.0-only

package exec

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// fakeRunner is a local CommandRunner double - exectest.FakeRunner can't be
// used here since that package imports this one (import cycle).
type fakeRunner struct {
	calls  []string
	stream []string
	err    error
}

func (f *fakeRunner) record(name string, args []string) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.record(name, args)
	return f.err
}

func (f *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	f.record(name, args)
	return nil, f.err
}

func (f *fakeRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	f.record(name, args)
	return []byte("out"), f.err
}

func (f *fakeRunner) CombinedOutputWithStdin(_ context.Context, _ io.Reader, name string, args ...string) ([]byte, error) {
	f.record(name, args)
	return []byte("stdin-out"), f.err
}

func (f *fakeRunner) Stream(_ context.Context, lines chan<- string, name string, args ...string) error {
	f.record(name, args)
	for _, l := range f.stream {
		lines <- l
	}
	return f.err
}

func withFakeRunner(f *fakeRunner, fn func()) {
	orig := DefaultRunner
	DefaultRunner = f
	defer func() { DefaultRunner = orig }()
	fn()
}

func TestSudoArgs(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: SudoArgs's non-root branches aren't reachable")
	}
	t.Run("interactive prepends sudo", func(t *testing.T) {
		name, args := SudoArgs(true, "systemctl", "restart", "k3s")
		if name != "sudo" {
			t.Errorf("name = %q, want sudo", name)
		}
		want := []string{"systemctl", "restart", "k3s"}
		if len(args) != len(want) || args[0] != "systemctl" {
			t.Errorf("args = %v, want %v (name folded into args, wrapped in sudo)", args, want)
		}
	})
	t.Run("non-interactive prepends sudo -n", func(t *testing.T) {
		name, args := SudoArgs(false, "systemctl", "status", "k3s")
		if name != "sudo" {
			t.Errorf("name = %q, want sudo", name)
		}
		if len(args) < 2 || args[0] != "-n" || args[1] != "systemctl" {
			t.Errorf("args = %v, want [-n systemctl status k3s]", args)
		}
	})
}

func TestWrapCmdErr(t *testing.T) {
	if err := WrapCmdErr([]byte("whatever"), nil); err != nil {
		t.Errorf("WrapCmdErr(_, nil) = %v, want nil", err)
	}
	sentinel := errors.New("boom")
	err := WrapCmdErr([]byte("  some output\n"), sentinel)
	if err == nil {
		t.Fatal("WrapCmdErr(_, err) = nil, want an error")
	}
	if !strings.Contains(err.Error(), "some output") || !errors.Is(err, sentinel) {
		t.Errorf("WrapCmdErr err = %v, want it to wrap sentinel and mention trimmed output", err)
	}
}

func TestRunPrivileged(t *testing.T) {
	f := &fakeRunner{}
	withFakeRunner(f, func() {
		cmd := RunPrivileged("k3s-uninstall.sh")
		if err := cmd.Run(); err != nil {
			t.Fatalf("Run() err = %v, want nil", err)
		}
	})
	if len(f.calls) != 1 || !strings.Contains(f.calls[0], "k3s-uninstall.sh") {
		t.Errorf("calls = %v, want one call containing k3s-uninstall.sh", f.calls)
	}
}

func TestRunNonInteractiveOutputAndCombinedOutput(t *testing.T) {
	f := &fakeRunner{}
	withFakeRunner(f, func() {
		cmd := RunNonInteractive(context.Background(), "systemctl", "is-active", "k3s")
		if _, err := cmd.Output(); err != nil {
			t.Fatalf("Output() err = %v, want nil", err)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CombinedOutput() err = %v, want nil", err)
		}
		if string(out) != "out" {
			t.Errorf("CombinedOutput() = %q, want %q", out, "out")
		}
	})
	if len(f.calls) != 2 {
		t.Fatalf("calls = %v, want 2 calls", f.calls)
	}
}

func TestWrappedCmdCombinedOutputWithStdin(t *testing.T) {
	f := &fakeRunner{}
	withFakeRunner(f, func() {
		cmd := RunPrivileged("chpasswd")
		cmd.Stdin = strings.NewReader("root:secret\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CombinedOutput() err = %v, want nil", err)
		}
		if string(out) != "stdin-out" {
			t.Errorf("CombinedOutput() = %q, want %q (the stdin-routed variant)", out, "stdin-out")
		}
	})
}

func TestRunStreamed(t *testing.T) {
	f := &fakeRunner{stream: []string{"line one", "line two"}}
	var got []string
	withFakeRunner(f, func() {
		ch := make(chan StepMsg, 8)
		wrap := func(line string) StepMsg { return line }
		err := RunStreamed(ch, wrap, "journalctl", "-f")
		if err != nil {
			t.Fatalf("RunStreamed err = %v, want nil", err)
		}
		close(ch)
		for msg := range ch {
			got = append(got, msg.(string))
		}
	})
	want := []string{"line one", "line two"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("streamed lines = %v, want %v", got, want)
	}
	if len(f.calls) != 1 || !strings.Contains(f.calls[0], "journalctl") {
		t.Errorf("calls = %v, want one journalctl call", f.calls)
	}
}

func TestRunStreamedPropagatesError(t *testing.T) {
	sentinel := errors.New("stream failed")
	f := &fakeRunner{err: sentinel}
	withFakeRunner(f, func() {
		ch := make(chan StepMsg, 1)
		err := RunStreamed(ch, func(line string) StepMsg { return line }, "false")
		if !errors.Is(err, sentinel) {
			t.Errorf("RunStreamed err = %v, want %v", err, sentinel)
		}
	})
}
