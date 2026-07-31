package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// CommandRunner is the sole seam between business logic and the real OS
// process table. Every exec.Command in this package goes through
// DefaultRunner (directly or via runPrivileged/runNonInteractive/
// runStreamed below) so tests can swap it for a fake instead of shelling
// out for real.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
	// CombinedOutputWithStdin behaves like CombinedOutput but pipes stdin to
	// the command first - only setAdminPassword's chpasswd call needs this.
	CombinedOutputWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error)
	// Stream runs name, sending each combined stdout/stderr line to lines as
	// it arrives, and returns the command's final error. Stream never
	// closes lines - the caller owns it.
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

// sudoArgs prepends sudo (interactive, or "sudo -n" when the caller must
// never risk a password prompt) unless already root, in which case name/args
// pass through unchanged.
func sudoArgs(interactive bool, name string, args ...string) (string, []string) {
	if os.Getuid() == 0 {
		return name, args
	}
	if interactive {
		return "sudo", append([]string{name}, args...)
	}
	return "sudo", append([]string{"-n", name}, args...)
}

// wrappedCmd mimics the handful of *exec.Cmd methods/fields this package's
// callers use, routing them through DefaultRunner instead of the real OS -
// runPrivileged/runNonInteractive return one of these instead of a raw
// *exec.Cmd so every call site stays fakeable without changing its shape.
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

// runPrivileged wraps name/args in interactive sudo (may prompt) unless
// already root.
func runPrivileged(name string, args ...string) wrappedCmd {
	n, a := sudoArgs(true, name, args...)
	return wrappedCmd{ctx: context.Background(), name: n, args: a}
}

// runNonInteractive wraps name/args in `sudo -n` (fails immediately instead
// of prompting when there's no cached sudo ticket) unless already root -
// status checks are best-effort background polls, so they must never risk a
// password prompt fighting bubbletea's raw-terminal mode (same concern as
// installedK3sVersion in k3s.go).
func runNonInteractive(ctx context.Context, name string, args ...string) wrappedCmd {
	n, a := sudoArgs(false, name, args...)
	return wrappedCmd{ctx: ctx, name: n, args: a}
}

// runStreamed runs a privileged command and sends each output line to ch,
// returning the command's final error (if any) instead of a stepDoneMsg, so
// composite steps can chain several of these before reporting done/failed.
func runStreamed(ch chan<- tea.Msg, name string, args ...string) error {
	n, a := sudoArgs(true, name, args...)
	lines := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		errCh <- DefaultRunner.Stream(context.Background(), lines, n, a...)
		close(lines)
	}()
	for line := range lines {
		ch <- stepOutputMsg(line)
	}
	return <-errCh
}

// wrapCmdErr returns nil if err is nil; otherwise leads with what the
// command actually printed - out is a CombinedOutput()/Output() result.
func wrapCmdErr(out []byte, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
}
