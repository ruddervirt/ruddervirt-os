// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"ruddervirt-setup/internal/config"
	execpkg "ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/stabilizer/settings"
	"ruddervirt-setup/internal/tui"
)

func main() {
	// Non-interactive subcommand dispatch, checked first: `ruddervirt-setup
	// settings ...` (see internal/stabilizer/settings/cli.go) runs directly
	// over SSH, bypassing ruddervirt-shell.sh's TUI-launch branch entirely -
	// no TUI, no RUDDERVIRT_SHELL, no self-update loop needed for it.
	if len(os.Args) > 1 && os.Args[1] == "settings" {
		os.Exit(settings.RunSettingsCLI(os.Args[2:]))
	}

	// RUDDERVIRT_SHELL tells ruddervirt-shell.sh not to re-launch this menu
	// when a login shell starts inside one of the runShell() sessions below.
	os.Setenv("RUDDERVIRT_SHELL", "1")

	opts := tui.ProgramOptions()
	firstRun := true
	for {
		im := initialModel()
		if !firstRun && (im.current == screenPasswordCheck || im.current == screenHostnameChange) {
			// initialModel forces these flows whenever still outstanding, which
			// is right on true first launch but would also force the operator
			// straight back into them every time this loop rebuilds a model
			// after k9s/shell (deferring, not completing, one of these leaves it
			// outstanding). Land on the menu instead - "configure"/"update"
			// still re-check and re-prompt as needed.
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
		case fm.update.Installed:
			// main() ignores os.Args, so re-execing with the same argv is safe -
			// hands control straight to the new binary instead of making the
			// admin log back in to pick it up.
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
// (not syscall.Exec) so exiting it returns control to main and loops back to
// the menu instead of ending the session.
func runShell() {
	fmt.Println("\nYou are exiting to a bash shell. Type \"exit\" or press ctrl+d to return to the menu.")
	cmd := exec.Command("/bin/bash", "-l")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// A non-zero exit (e.g. a failed last command) isn't an error for us -
	// return to the menu either way.
	_ = cmd.Run()
}

// runK9s launches k9s as a child process (same pattern as runShell), with an
// explicit --kubeconfig since k9s, unlike `k3s kubectl`, has no built-in
// default pointing at /etc/rancher/k3s/k3s.yaml (and KUBECONFIG wouldn't
// survive runPrivileged's sudo wrapping anyway).
//
// -A (all namespaces) fixes k9s "showing up blank": its default namespace is
// whatever the kubeconfig context points at (usually "default"), which
// normally has nothing running - everything lives in
// kube-system/kubevirt/rook-ceph/etc. ensureK9sViewsConfig pairs with it,
// sorting the list by namespace instead of k9s's default order.
func runK9s() {
	ensureK9sViewsConfig()
	fmt.Println("\nLaunching k9s. Press ctrl+c or type \":quit\" to return to the menu.")
	// Built directly, not via runPrivileged/DefaultRunner: k9s needs a live
	// *exec.Cmd for interactive TTY passthrough, which CommandRunner's
	// fakeable, buffered-output shape doesn't support.
	name, args := execpkg.SudoArgs(true, "k9s", "--kubeconfig", "/etc/rancher/k3s/k3s.yaml", "-A")
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

// k9sViewsConfigPath is k9s's per-resource view/sort config file
// ($XDG_CONFIG_HOME/k9s/views.yaml). runK9s launches k9s as root (via sudo,
// which resets HOME), so this is root's config dir, not the admin user's.
const k9sViewsConfigPath = "/root/.config/k9s/views.yaml"

// k9sViewsConfig sorts the pod view - what k9s lands on at launch - by
// namespace. Schema: https://github.com/derailed/k9s/blob/master/internal/config/views.go.
const k9sViewsConfig = `views:
  v1/pods:
    sortColumn: NAMESPACE:asc
`

// ensureK9sViewsConfig writes k9sViewsConfig only the first time k9s
// launches, so a later hand-customized views.yaml is never overwritten.
func ensureK9sViewsConfig() {
	if execpkg.RunPrivileged("/usr/bin/test", "-f", k9sViewsConfigPath).Run() == nil {
		return
	}
	_ = config.WritePrivileged(k9sViewsConfigPath, []byte(k9sViewsConfig))
}
