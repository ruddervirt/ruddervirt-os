// SPDX-License-Identifier: GPL-3.0-only

// Package selfupdate holds ruddervirt-setup's own self-update logic: check
// GitHub for a newer release, download+verify the replacement binary, and
// install it. Named selfupdate to disambiguate from internal/osupdate (the
// OS image's update flow) and app.go's Bubble Tea Update() router - an
// unrelated "update" in the Elm-architecture sense.
package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/k3s"
	"ruddervirt-setup/internal/versions"
)

// ruddervirtRepo is the GitHub repo self-updates are pulled from. Hardcoded
// (never user-supplied) so the update flow only ever talks to a single,
// known-good origin over HTTPS.
const ruddervirtRepo = "ruddervirt/ruddervirt-os"

// releaseBinariesBranch holds the ruddervirt-setup binaries, one version
// folder per release (release.yml pushes here after each tag build). Kept
// off the public Releases page (which only shows the installer ISO) so end
// users aren't confused by a binary they never need to touch directly.
const releaseBinariesBranch = "release-binaries"

type GHRelease struct {
	TagName string `json:"tag_name"`
}

// SetupBinaryURL returns the raw.githubusercontent.com URL for the
// ruddervirt-setup binary published for the given release tag.
func SetupBinaryURL(version string) string {
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/ruddervirt-setup", ruddervirtRepo, releaseBinariesBranch, version)
}

// SetupChecksumURL is SetupBinaryURL's sibling for the accompanying
// ruddervirt-setup.sha256 file.
func SetupChecksumURL(version string) string {
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/ruddervirt-setup.sha256", ruddervirtRepo, releaseBinariesBranch, version)
}

// FetchLatestSetupRelease returns the latest non-draft, non-prerelease
// GitHub release of ruddervirtRepo via /releases/latest (GitHub guarantees a
// single such release, no list/sort/filter needed). The fetch itself is
// internal/versions.FetchLatestGitHubRelease, shared with internal/aileron.
func FetchLatestSetupRelease() (GHRelease, error) {
	rel, err := versions.FetchLatestGitHubRelease(ruddervirtRepo, "latest release")
	if err != nil {
		return GHRelease{}, err
	}
	if rel.TagName == "" {
		return GHRelease{}, fmt.Errorf("latest release has no tag name")
	}
	return GHRelease{TagName: rel.TagName}, nil
}

// This repo's "vMAJOR.MINOR.PATCH" tags parse/compare identically to
// internal/versions.ParseSemver/CompareSemver, so callers use
// versions.CompareSemver directly (see package main's update.go) instead of
// a separate comparator here.

// FetchChecksumHex downloads the small ruddervirt-setup.sha256 file and
// returns the hex digest from its first whitespace-separated field
// (standard `sha256sum` output format: "<hex>  <filename>").
func FetchChecksumHex(url string) (string, error) {
	tmp := filepath.Join(os.TempDir(), "ruddervirt-setup.sha256.tmp")
	defer os.Remove(tmp)
	if err := k3s.DownloadFile(url, tmp); err != nil {
		return "", err
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum file")
	}
	return fields[0], nil
}

// sha256File returns the lowercase hex SHA256 digest of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// downloadVerifiedToPrivilegedPath is internal/k3s's downloadToPrivilegedPath
// sibling for callers that must verify integrity before installing into a
// root-owned path: downloads to a tmp file, computes its SHA256, and compares
// (case-insensitively) against expectedHex - a mismatch removes the tmp file
// and errors before any privileged mv, so a corrupted/tampered download can
// never reach destPath. Same single-flow safety assumption as
// downloadToPrivilegedPath: fine since this only runs as a one-shot,
// user-triggered action, never concurrently.
func downloadVerifiedToPrivilegedPath(url, expectedHex, destPath string, mode os.FileMode) error {
	tmpPath := filepath.Join(os.TempDir(), filepath.Base(destPath))
	if err := k3s.DownloadFile(url, tmpPath); err != nil {
		return err
	}
	got, err := sha256File(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	want := strings.ToLower(strings.TrimSpace(expectedHex))
	if got != want {
		os.Remove(tmpPath)
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if out, err := exec.RunPrivileged("/usr/bin/mv", tmpPath, destPath).CombinedOutput(); err != nil {
		return exec.WrapCmdErr(out, err)
	}
	return nil
}

// pendingUpdate* carry the confirmed update's details into UpdateSteps' Run
// func, whose signature (cfg config.Config, ch chan<- installsteps.StepMsg)
// has no room for them. Set via SetPending right before LaunchStep, never
// read concurrently. Deliberately unexported - never thread these through
// config.Config, which would persist them to disk.
var (
	pendingUpdateVersion   string
	pendingUpdateBinaryURL string
	pendingUpdateChecksum  string
)

// SetPending records the wizard-confirmed update details UpdateSteps' Run
// func downloads and installs - called once, right before launching
// UpdateSteps, by package main's app.go.
func SetPending(version, binaryURL, checksumHex string) {
	pendingUpdateVersion = version
	pendingUpdateBinaryURL = binaryURL
	pendingUpdateChecksum = checksumHex
}

const UpdateStepLabel = "Downloading and verifying ruddervirt-setup"

var UpdateSteps = []installsteps.Step{
	{
		Label: UpdateStepLabel,
		Run: func(cfg config.Config, ch chan<- installsteps.StepMsg) {
			ch <- installsteps.StepOutputMsg(fmt.Sprintf("Downloading ruddervirt-setup %s...", pendingUpdateVersion))
			if err := downloadVerifiedToPrivilegedPath(
				pendingUpdateBinaryURL, pendingUpdateChecksum,
				"/usr/local/bin/ruddervirt-setup", 0755,
			); err != nil {
				ch <- installsteps.StepDoneMsg{Label: UpdateStepLabel, Err: err}
				return
			}
			ch <- installsteps.StepOutputMsg("Checksum verified; ruddervirt-setup updated")
			ch <- installsteps.StepDoneMsg{Label: UpdateStepLabel}
		},
	},
}
