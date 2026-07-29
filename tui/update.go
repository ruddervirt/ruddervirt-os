package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ruddervirtRepo is the GitHub repo self-updates are pulled from. Hardcoded
// (never user-supplied) so the update flow only ever talks to a single,
// known-good origin over HTTPS.
const ruddervirtRepo = "ruddervirt/ruddervirt-os"

const setupBinaryAssetName = "ruddervirt-setup"
const setupChecksumAssetName = "ruddervirt-setup.sha256"

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

// fetchLatestSetupRelease returns the latest non-draft, non-prerelease
// GitHub release of ruddervirtRepo. Mirrors fetchK3sVersions' HTTP setup
// (k3s.go) but uses /releases/latest, which GitHub itself guarantees is a
// single such release - no list/sort/filter needed here.
func fetchLatestSetupRelease() (ghRelease, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+ruddervirtRepo+"/releases/latest", nil)
	if err != nil {
		return ghRelease{}, err
	}
	req.Header.Set("User-Agent", "ruddervirt-setup")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return ghRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ghRelease{}, fmt.Errorf("unexpected status %s fetching latest release", resp.Status)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return ghRelease{}, err
	}
	if rel.TagName == "" {
		return ghRelease{}, fmt.Errorf("latest release has no tag name")
	}
	return rel, nil
}

// findAsset returns the first asset in assets named name.
func findAsset(assets []ghAsset, name string) (ghAsset, bool) {
	for _, a := range assets {
		if a.Name == name {
			return a, true
		}
	}
	return ghAsset{}, false
}

var setupVersionPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// parseSetupVersion parses this repo's plain "vMAJOR.MINOR.PATCH" release
// tags. ok is false for anything else, notably the local dev build's
// literal "dev" - unlike k3s's tags (parseK3sVersion, k3s.go), there's no
// "+k3sN" build suffix or "-rcN" prerelease convention here.
func parseSetupVersion(v string) (major, minor, patch int, ok bool) {
	m := setupVersionPattern.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	patch, _ = strconv.Atoi(m[3])
	return major, minor, patch, true
}

// compareSetupVersions returns >0 if a > b, 0 if equal, <0 if a < b. ok is
// false if either side doesn't parse as vMAJOR.MINOR.PATCH (e.g. the local
// dev build's "dev" string) - callers should treat that as "always offer
// the update", since a dev build is never actually equal to a real release.
func compareSetupVersions(a, b string) (cmp int, ok bool) {
	aMajor, aMinor, aPatch, aOK := parseSetupVersion(a)
	bMajor, bMinor, bPatch, bOK := parseSetupVersion(b)
	if !aOK || !bOK {
		return 0, false
	}
	if aMajor != bMajor {
		return aMajor - bMajor, true
	}
	if aMinor != bMinor {
		return aMinor - bMinor, true
	}
	return aPatch - bPatch, true
}

// fetchChecksumHex downloads the small ruddervirt-setup.sha256 release
// asset and returns the hex digest from its first whitespace-separated
// field (standard `sha256sum` output format: "<hex>  <filename>").
func fetchChecksumHex(url string) (string, error) {
	tmp := filepath.Join(os.TempDir(), "ruddervirt-setup.sha256.tmp")
	defer os.Remove(tmp)
	if err := downloadFile(url, tmp); err != nil {
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

// downloadVerifiedToPrivilegedPath is downloadToPrivilegedPath's (k3s.go)
// sibling for callers that must verify integrity before installing into a
// root-owned path. Downloads to a local tmp file, computes its SHA256, and
// compares (case-insensitively) against expectedHex - a mismatch removes
// the tmp file and returns an error before any privileged mv, so a
// corrupted or tampered download can never reach destPath. Same
// sequential-steps safety assumption as downloadToPrivilegedPath: fine here
// too, since this only ever runs as a single one-shot user-triggered
// action, never concurrently.
func downloadVerifiedToPrivilegedPath(url, expectedHex, destPath string, mode os.FileMode) error {
	tmpPath := filepath.Join(os.TempDir(), filepath.Base(destPath))
	if err := downloadFile(url, tmpPath); err != nil {
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
	if out, err := runPrivileged("/usr/bin/mv", tmpPath, destPath).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// pendingUpdate* carry the checked-and-confirmed update's details into
// updateSteps' run func, which (like every installStep) only accepts
// (cfg Config, ch chan<- tea.Msg) - set immediately before launchStep is
// called for the update flow and never read concurrently, matching
// downloadToPrivilegedPath's own single-flow safety assumption.
var (
	pendingUpdateVersion   string
	pendingUpdateBinaryURL string
	pendingUpdateChecksum  string
)

const updateStepLabel = "Downloading and verifying ruddervirt-setup"

var updateSteps = []installStep{
	{
		label: updateStepLabel,
		run: func(cfg Config, ch chan<- tea.Msg) {
			ch <- stepOutputMsg(fmt.Sprintf("Downloading ruddervirt-setup %s...", pendingUpdateVersion))
			if err := downloadVerifiedToPrivilegedPath(
				pendingUpdateBinaryURL, pendingUpdateChecksum,
				"/usr/local/bin/ruddervirt-setup", 0755,
			); err != nil {
				ch <- stepDoneMsg{label: updateStepLabel, err: err}
				return
			}
			ch <- stepOutputMsg("Checksum verified; ruddervirt-setup updated")
			ch <- stepDoneMsg{label: updateStepLabel}
		},
	},
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
// the checksum asset so it's ready to hand straight to updateSteps without
// another round-trip after the user confirms.
func checkForUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		rel, err := fetchLatestSetupRelease()
		if err != nil {
			return updateCheckMsg{err: err}
		}
		bin, ok := findAsset(rel.Assets, setupBinaryAssetName)
		if !ok {
			return updateCheckMsg{err: fmt.Errorf("release %s has no %s asset", rel.TagName, setupBinaryAssetName)}
		}
		sumAsset, ok := findAsset(rel.Assets, setupChecksumAssetName)
		if !ok {
			return updateCheckMsg{err: fmt.Errorf("release %s has no %s asset", rel.TagName, setupChecksumAssetName)}
		}
		if cmp, ok := compareSetupVersions(rel.TagName, version); ok && cmp <= 0 {
			return updateCheckMsg{latestVersion: rel.TagName, alreadyLatest: true}
		}
		checksumHex, err := fetchChecksumHex(sumAsset.BrowserDownloadURL)
		if err != nil {
			return updateCheckMsg{err: err}
		}
		return updateCheckMsg{
			latestVersion: rel.TagName,
			binaryURL:     bin.BrowserDownloadURL,
			checksumHex:   checksumHex,
		}
	}
}
