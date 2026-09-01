// SPDX-License-Identifier: GPL-3.0-only

package versions

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HTTPDoer is the seam between this package's GitHub-release fetchers and
// the OS's actual HTTP transport, so tests can inject a fake instead of
// hitting the network.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// DefaultHTTPClient is the HTTPDoer FetchGitHubReleases/
// FetchLatestGitHubRelease use unless a test swaps it out.
var DefaultHTTPClient HTTPDoer = &http.Client{Timeout: 5 * time.Second}

// GitHubRelease is the subset of GitHub's release API response shape every
// caller in this codebase actually reads: the tag name, plus the draft/
// prerelease flags used to filter a release list down to real, published
// releases.
type GitHubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// fetchGitHubJSON GETs url with the headers every GitHub API caller needs,
// and decodes the JSON body into out. errDesc shapes the unexpected-status
// error message so it stays specific to what was being fetched.
func fetchGitHubJSON(url, errDesc string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ruddervirt-setup")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := DefaultHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s fetching %s", resp.Status, errDesc)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// FetchGitHubReleases lists every release GitHub returns for repo
// ("owner/name"), in GitHub's own order (publish-date, not version-sorted -
// callers needing version order re-sort themselves). errDesc feeds the
// unexpected-status error message.
func FetchGitHubReleases(repo, errDesc string) ([]GitHubRelease, error) {
	var releases []GitHubRelease
	if err := fetchGitHubJSON("https://api.github.com/repos/"+repo+"/releases", errDesc, &releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// FetchLatestGitHubRelease fetches repo's single latest non-draft,
// non-prerelease release via GitHub's /releases/latest endpoint - no
// list/sort/filter needed, GitHub guarantees at most one result. errDesc
// feeds the unexpected-status error message.
func FetchLatestGitHubRelease(repo, errDesc string) (GitHubRelease, error) {
	var rel GitHubRelease
	if err := fetchGitHubJSON("https://api.github.com/repos/"+repo+"/releases/latest", errDesc, &rel); err != nil {
		return GitHubRelease{}, err
	}
	return rel, nil
}
