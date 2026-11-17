// SPDX-License-Identifier: GPL-3.0-only

// Package exec is the sole seam between business logic and the real OS
// process table, so tests can swap it for a fake instead of shelling out.
package exec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// StepMsg stands in for bubbletea's tea.Msg so this package never needs to
// import bubbletea. It's a defined type over the empty interface, so any
// tea.Msg value satisfies it and round-trips back into a tea.Msg-typed slot.
type StepMsg any

// CommandRunner is the seam between business logic and the real OS process
// table, so tests can swap in a fake instead of shelling out for real.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
	// CombinedOutputWithStdin is like CombinedOutput but pipes stdin first -
	// only setAdminPassword's chpasswd call needs this.
	CombinedOutputWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error)
	// Stream sends each combined stdout/stderr line to lines as it arrives
	// and returns the command's final error. Caller owns closing lines.
	Stream(ctx context.Context, lines chan<- string, name string, args ...string) error
}

// DefaultRunner is swapped for a fake in tests; production code never
// constructs a CommandRunner directly.
var DefaultRunner CommandRunner = realRunner{}

type realRunner struct{}

func (realRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

func (realRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func (realRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (realRunner) CombinedOutputWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	return cmd.CombinedOutput()
}

func (realRunner) Stream(ctx context.Context, lines chan<- string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return err
	}
	pw.Close()
	scanner := bufio.NewScanner(pr)
	for scanner.Scan() {
		lines <- scanner.Text()
	}
	pr.Close()
	return cmd.Wait()
}

// SudoArgs prepends sudo (interactive, or "sudo -n" when the caller must
// never risk a password prompt) unless already root, in which case name/args
// pass through unchanged.
func SudoArgs(interactive bool, name string, args ...string) (string, []string) {
	if os.Getuid() == 0 {
		return name, args
	}
	if interactive {
		return "sudo", append([]string{name}, args...)
	}
	return "sudo", append([]string{"-n", name}, args...)
}

// wrappedCmd mimics the *exec.Cmd methods/fields callers use, routing them
// through DefaultRunner so call sites stay fakeable without changing shape.
type wrappedCmd struct {
	ctx   context.Context
	name  string
	args  []string
	Stdin io.Reader
}

func (c wrappedCmd) Run() error { return DefaultRunner.Run(c.ctx, c.name, c.args...) }

func (c wrappedCmd) Output() ([]byte, error) { return DefaultRunner.Output(c.ctx, c.name, c.args...) }

func (c wrappedCmd) CombinedOutput() ([]byte, error) {
	if c.Stdin != nil {
		return DefaultRunner.CombinedOutputWithStdin(c.ctx, c.Stdin, c.name, c.args...)
	}
	return DefaultRunner.CombinedOutput(c.ctx, c.name, c.args...)
}

// RunPrivileged wraps name/args in interactive sudo (may prompt) unless
// already root.
func RunPrivileged(name string, args ...string) wrappedCmd {
	n, a := SudoArgs(true, name, args...)
	return wrappedCmd{ctx: context.Background(), name: n, args: a}
}

// RunNonInteractive wraps name/args in `sudo -n` (fails instead of prompting
// when there's no cached sudo ticket) unless already root - background
// status polls must never risk a password prompt fighting bubbletea's
// raw-terminal mode (same concern as k3s.InstalledK3sVersion).
func RunNonInteractive(ctx context.Context, name string, args ...string) wrappedCmd {
	n, a := SudoArgs(false, name, args...)
	return wrappedCmd{ctx: ctx, name: n, args: a}
}

// RunStreamed runs a privileged command and sends each output line, wrapped
// via wrap, to ch, returning the command's final error instead of a
// stepDoneMsg so composite steps can chain several before reporting
// done/failed. wrap lets callers turn each line into their own StepMsg
// without this package needing to know about their concrete type.
func RunStreamed(ch chan<- StepMsg, wrap func(line string) StepMsg, name string, args ...string) error {
	n, a := SudoArgs(true, name, args...)
	lines := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		errCh <- DefaultRunner.Stream(context.Background(), lines, n, a...)
		close(lines)
	}()
	for line := range lines {
		ch <- wrap(line)
	}
	return <-errCh
}

// WrapCmdErr returns nil if err is nil; otherwise leads with what the
// command actually printed - out is a CombinedOutput()/Output() result.
func WrapCmdErr(out []byte, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
}

// ReadFilePrivileged reads path as the invoking user, falling back to a
// privileged `cat` when the file's mode denies that. Config files this tool
// writes itself are mode 0644 (see internal/config.WritePrivileged), but one
// an operator placed by hand with sudo may well be 0600 - and silently
// reading nothing there would be worse than prompting, since callers use the
// contents to decide what to do (see internal/k3s.NodeMembership). Only for
// Step.Run paths: Step.Plan must never risk a sudo prompt, so it passes plain
// os.ReadFile instead.
func ReadFilePrivileged(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil || !errors.Is(err, os.ErrPermission) {
		return data, err
	}
	out, cmdErr := RunPrivileged("/usr/bin/cat", path).Output()
	if cmdErr != nil {
		// Report the original permission error, not sudo/cat's - it's the
		// one that tells the operator what's actually wrong with the file.
		return nil, err
	}
	return out, nil
}
