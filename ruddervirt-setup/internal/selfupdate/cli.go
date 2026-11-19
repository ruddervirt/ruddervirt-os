// SPDX-License-Identifier: GPL-3.0-only

package selfupdate

import (
	"flag"
	"fmt"
	"io"
	"os"

	"ruddervirt-setup/internal/versions"
)

// UpdateFlag is the top-level flag main.go dispatches on, before the TUI
// starts (same idea as the `settings` subcommand): a non-interactive
// self-update for automation such as Ansible, e.g.
//
//	ruddervirt-setup --update-ruddervirt-setup --confirm
const UpdateFlag = "--update-ruddervirt-setup"

const updateUsage = `Usage:
  ruddervirt-setup --update-ruddervirt-setup
      Check for a newer ruddervirt-setup release and report it. Changes
      nothing (a dry run, safe for Ansible check mode).

  ruddervirt-setup --update-ruddervirt-setup --confirm
      Download, verify (SHA256) and install the latest release into
      ` + InstalledBinaryPath + `. Needs root or passwordless sudo.

Output (stdout, one line) and exit codes, for scripting:
  "ruddervirt-setup is up to date (vX)"            exit 0, nothing changed
  "ruddervirt-setup update available: vX -> vY"    exit 0, dry run only
  "ruddervirt-setup updated: vX -> vY"             exit 0, binary replaced
  errors go to stderr                              exit 1 (2 for bad flags)

Ansible: changed_when: "'ruddervirt-setup updated:' in result.stdout"
`

// cliDeps are RunUpdateCLI's network/install seams, swapped out by tests.
type cliDeps struct {
	current        string
	fetchLatest    func() (GHRelease, error)
	fetchChecksum  func(url string) (string, error)
	install        func(binaryURL, checksumHex string) error
	stdout, stderr io.Writer
}

func defaultCLIDeps() cliDeps {
	return cliDeps{
		current:       versions.Version,
		fetchLatest:   FetchLatestSetupRelease,
		fetchChecksum: FetchChecksumHex,
		install:       Install,
		stdout:        os.Stdout,
		stderr:        os.Stderr,
	}
}

// RunUpdateCLI is main.go's entry point for `ruddervirt-setup
// --update-ruddervirt-setup [--confirm]`; args excludes the UpdateFlag
// itself. Returns a process exit code.
func RunUpdateCLI(args []string) int {
	return runUpdateCLI(args, defaultCLIDeps())
}

func runUpdateCLI(args []string, d cliDeps) int {
	fs := flag.NewFlagSet("update-ruddervirt-setup", flag.ContinueOnError)
	fs.SetOutput(d.stderr)
	var confirm bool
	fs.BoolVar(&confirm, "confirm", false, "actually install the update (default: dry run)")
	fs.Usage = func() { fmt.Fprint(d.stderr, updateUsage) }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(d.stderr, "Error: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return 2
	}

	rel, err := d.fetchLatest()
	if err != nil {
		fmt.Fprintln(d.stderr, "Error: checking for the latest release:", err)
		return 1
	}
	// Same rule as the TUI's checkForUpdateCmd: an unparseable current
	// version (e.g. a dev build) always counts as out of date.
	if cmp, ok := versions.CompareSemver(rel.TagName, d.current); ok && cmp <= 0 {
		fmt.Fprintf(d.stdout, "ruddervirt-setup is up to date (%s)\n", d.current)
		return 0
	}
	if !confirm {
		fmt.Fprintf(d.stdout, "ruddervirt-setup update available: %s -> %s (re-run with --confirm to install)\n", d.current, rel.TagName)
		return 0
	}

	checksumHex, err := d.fetchChecksum(SetupChecksumURL(rel.TagName))
	if err != nil {
		fmt.Fprintln(d.stderr, "Error: fetching checksum:", err)
		return 1
	}
	if err := d.install(SetupBinaryURL(rel.TagName), checksumHex); err != nil {
		fmt.Fprintln(d.stderr, "Error: installing update:", err)
		return 1
	}
	fmt.Fprintf(d.stdout, "ruddervirt-setup updated: %s -> %s\n", d.current, rel.TagName)
	return 0
}
