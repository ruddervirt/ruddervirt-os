package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// RUDDERVIRT_SHELL tells ruddervirt-shell.sh not to re-launch this menu
	// when a login shell starts inside one of the runShell() sessions below.
	os.Setenv("RUDDERVIRT_SHELL", "1")

	for {
		p := tea.NewProgram(initialModel())
		m, err := p.Run()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		switch fm := m.(model); {
		case fm.shellMode:
			runShell()
		case fm.k9sMode:
			runK9s()
		case fm.updateInstalled:
			// main() ignores os.Args entirely, so re-execing with the same
			// argv is safe - this hands control straight to the new binary
			// instead of making the admin log back in to pick it up.
			exe := "/usr/local/bin/ruddervirt-setup"
			fmt.Println("\nUpdate installed. Restarting into the new version...")
			if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
				fmt.Printf("Could not restart %s: %v\n", exe, err)
				fmt.Println("The update was installed; it will take effect next login.")
			}
		default:
			return
		}
	}
}

// runShell drops into an interactive bash login shell as a child process
// (not syscall.Exec) so that exiting it - "exit" or Ctrl+D - returns control
// to main, which loops back into the menu instead of ending the session.
func runShell() {
	fmt.Println("\nYou are exiting to a bash shell. Type \"exit\" or press ctrl+d to return to the menu.")
	cmd := exec.Command("/bin/bash", "-l")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// bash exiting non-zero (e.g. a failed last command) isn't an error for
	// us - just return to the menu either way.
	_ = cmd.Run()
}

// runK9s launches k9s as a child process (same pattern as runShell), with
// an explicit --kubeconfig since k9s - unlike `k3s kubectl` - has no
// built-in default pointing at /etc/rancher/k3s/k3s.yaml, and setting
// KUBECONFIG here wouldn't survive runPrivileged's sudo wrapping anyway.
func runK9s() {
	fmt.Println("\nLaunching k9s. Press ctrl+c or type \":quit\" to return to the menu.")
	// Built directly (not via runPrivileged/DefaultRunner) since k9s needs a
	// live *exec.Cmd for interactive TTY passthrough - not something
	// CommandRunner's fakeable, buffered-output shape supports, nor
	// something this ever needs to be faked in a test.
	name, args := sudoArgs(true, "k9s", "--kubeconfig", "/etc/rancher/k3s/k3s.yaml")
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}
