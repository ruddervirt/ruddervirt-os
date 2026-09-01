// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"ruddervirt-setup/internal/selfupdate"
	"ruddervirt-setup/internal/versions"
)

// selfUpdateAvailableMsg is a background, passive check for whether a newer
// ruddervirt-setup release exists - purely for the Update screen's
// "available" icon (see updateRowHasUpgrade, view.go). Unlike
// checkForUpdateCmd/updateCheckMsg below (which drives the interactive
// checking -> confirm flow), this never changes m.current, so it's safe to
// fire unconditionally from Init() - best-effort, same shape as
// fetchAileronVersionsCmd/fetchK3sVersionsCmd.
type selfUpdateAvailableMsg struct {
	available bool
}

func checkSelfUpdateAvailableCmd() tea.Cmd {
	return func() tea.Msg {
		rel, err := selfupdate.FetchLatestSetupRelease()
		if err != nil {
			return selfUpdateAvailableMsg{}
		}
		cmp, ok := versions.CompareSemver(rel.TagName, versions.Version)
		return selfUpdateAvailableMsg{available: ok && cmp > 0}
	}
}

// updateCheckMsg reports the result of checkForUpdateCmd back to Update().
type updateCheckMsg struct {
	latestVersion string
	binaryURL     string
	checksumHex   string
	alreadyLatest bool
	err           error
}

// checkForUpdateCmd hits GitHub for the latest ruddervirt-setup release,
// compares it against the running binary's version, and (if newer) fetches
// the checksum file so it's ready to hand straight to selfupdate.UpdateSteps
// without another round-trip after the user confirms.
func checkForUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		rel, err := selfupdate.FetchLatestSetupRelease()
		if err != nil {
			return updateCheckMsg{err: err}
		}
		if cmp, ok := versions.CompareSemver(rel.TagName, versions.Version); ok && cmp <= 0 {
			return updateCheckMsg{latestVersion: rel.TagName, alreadyLatest: true}
		}
		checksumHex, err := selfupdate.FetchChecksumHex(selfupdate.SetupChecksumURL(rel.TagName))
		if err != nil {
			return updateCheckMsg{err: err}
		}
		return updateCheckMsg{
			latestVersion: rel.TagName,
			binaryURL:     selfupdate.SetupBinaryURL(rel.TagName),
			checksumHex:   checksumHex,
		}
	}
}
