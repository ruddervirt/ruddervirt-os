// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// Non-interactive subcommand dispatch, checked before anything else -
	// `ruddervirt-setup settings ...` (see stabilizer_settings_cli.go) is
	// meant to be run directly over SSH (`ssh admin@host ruddervirt-setup
	// settings --set build_max_cpu=16`), which reaches here without ever
	// going through ruddervirt-shell.sh's interactive-login TUI-launch
	// branch (that script's own `-c` branch execs a given command
	// directly). Nothing below this needs to run for it - no TUI, no
	// RUDDERVIRT_SHELL, no self-update loop.
	if len(os.Args) > 1 && os.Args[1] == "settings" {
		os.Exit(runSettingsCLI(os.Args[2:]))
	}

	// RUDDERVIRT_SHELL tells ruddervirt-shell.sh not to re-launch this menu
	// when a login shell starts inside one of the runShell() sessions below.
	os.Setenv("RUDDERVIRT_SHELL", "1")

	opts := programOptions()
	firstRun := true
	for {
		im := initialModel()
		if !firstRun && (im.current == screenPasswordCheck || im.current == screenHostnameChange) {
			// initialModel forces the password-change/hostname-declare
			// flows whenever they're still outstanding - right for this
			// process's true first launch, but this loop also rebuilds a
			// fresh model every time control returns here after handing
			// off to k9s/shell/etc. Without this override, deferring one
			// (Ctrl+S on the password screen; Esc back to the menu, then
			// k9s/shell, on the hostname screen - it has no skip, see
			// hostname.go) instead of actually completing it would force
			// the operator straight back through it every single time they
			// used k9s or shell, since it would still be outstanding on the
			// very next loop iteration. Land on the menu instead -
			// selecting "configure"/"update" still re-checks and re-prompts
			// as needed, and neither can ever reach the install pipeline
			// without the hostname actually being declared first.
			im.current = screenMenu
		}
		firstRun = false

		p := tea.NewProgram(im, opts...)
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
//
// -A (all namespaces) is what actually fixes k9s "showing up blank": its
// default namespace is whatever the kubeconfig context points at (usually
// "default"), which on this appliance normally has nothing running in it -
// everything lives in kube-system/kubevirt/rook-ceph/etc. ensureK9sViewsConfig
// pairs with it, sorting the resulting list by namespace instead of k9s's
// own default order.
func runK9s() {
	ensureK9sViewsConfig()
	fmt.Println("\nLaunching k9s. Press ctrl+c or type \":quit\" to return to the menu.")
	// Built directly (not via runPrivileged/DefaultRunner) since k9s needs a
	// live *exec.Cmd for interactive TTY passthrough - not something
	// CommandRunner's fakeable, buffered-output shape supports, nor
	// something this ever needs to be faked in a test.
	name, args := sudoArgs(true, "k9s", "--kubeconfig", "/etc/rancher/k3s/k3s.yaml", "-A")
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

// k9sViewsConfigPath is k9s's per-resource view/sort config file
// ($XDG_CONFIG_HOME/k9s/views.yaml). runK9s always launches k9s as root
// (via sudo, which resets HOME to the target user's home by default), so
// this is root's config dir, not the invoking admin user's.
const k9sViewsConfigPath = "/root/.config/k9s/views.yaml"

// k9sViewsConfig sorts the pod view - what k9s lands on at launch - by
// namespace. See the schema at
// https://github.com/derailed/k9s/blob/master/internal/config/views.go.
const k9sViewsConfig = `views:
  v1/pods:
    sortColumn: NAMESPACE:asc
`

// ensureK9sViewsConfig writes k9sViewsConfig the first time k9s is
// launched, and never after - so an operator who later customizes their
// own views.yaml never has it silently overwritten.
func ensureK9sViewsConfig() {
	if runPrivileged("/usr/bin/test", "-f", k9sViewsConfigPath).Run() == nil {
		return
	}
	_ = writePrivileged(k9sViewsConfigPath, []byte(k9sViewsConfig))
}
