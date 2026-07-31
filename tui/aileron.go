package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// defaultAileronVersion is the fallback used before the live fetch
// completes (or if it fails), same role as defaultK3sVersion.
const defaultAileronVersion = "v0.0.25"

// fetchAileronVersions lists ruddervirt/aileron release tags from GitHub,
// newest first, for the Settings screen's version picker - same shape as
// fetchK3sVersions (k3s.go:88-144), but using the plain vMAJOR.MINOR.PATCH
// comparator (compareSemver, kubevirt.go) since Aileron's tags don't carry
// a k3s-style +BUILD suffix.
func fetchAileronVersions() ([]string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/ruddervirt/aileron/releases", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ruddervirt-setup")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s fetching aileron releases", resp.Status)
	}

	var releases []struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	var versions []string
	for _, r := range releases {
		if r.Draft || r.Prerelease || r.TagName == "" {
			continue
		}
		if _, ok := parseSemver(r.TagName); !ok {
			continue
		}
		versions = append(versions, r.TagName)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no aileron releases found")
	}

	sort.SliceStable(versions, func(i, j int) bool {
		cmp, ok := compareSemver(versions[i], versions[j])
		return ok && cmp > 0
	})

	return versions, nil
}

// applyAileron renders manifests/aileron-helmchart.yaml's
// __AILERON_VERSION__ placeholder (the chart version, i.e. the release tag
// stripped of its leading "v" - Helm chart versions must be bare semver)
// and applies the resulting HelmChart object - same
// read-template/substitute/write-to-tempfile/apply shape as applyKubeOvn
// (k3s.go:339-374). k3s's own always-on helm-controller then does the
// actual chart install as a Job inside the cluster; no helm binary is ever
// invoked here.
func applyAileron(ch chan<- tea.Msg, kubectlBin, version string) error {
	const templatePath = "/etc/ruddervirt/manifests/aileron-helmchart.yaml"
	const jobNamespace = "kube-system"
	const jobName = "job/helm-install-aileron"

	chartVersion := strings.TrimPrefix(version, "v")

	data, err := os.ReadFile(templatePath)
	if err != nil {
		return err
	}
	rendered := strings.ReplaceAll(string(data), "__AILERON_VERSION__", chartVersion)

	tmp, err := os.CreateTemp("", "aileron-helmchart-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(rendered); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := runStreamed(ch, kubectlBin, "apply", "-f", tmpPath); err != nil {
		return err
	}

	// The controller creates the install Job asynchronously - wait for it
	// to exist, then for it to finish, same two-phase pattern as
	// applyRookCeph's CRD/CephCluster wait (k3s.go:497-514).
	if err := pollUntil(ch, "Waiting for the aileron helm-install job", 60, 5*time.Second, func() bool {
		return runPrivileged(kubectlBin, "-n", jobNamespace, "get", jobName).Run() == nil
	}); err != nil {
		return err
	}
	ch <- stepOutputMsg("Waiting for the aileron helm-install job to complete...")
	return runStreamed(ch, kubectlBin, "-n", jobNamespace, "wait", "--for=condition=complete", jobName, "--timeout=600s")
}

// aileronVersionsFetchedMsg carries fetchAileronVersions' result back into
// Update - same reasoning as k3sVersionsFetchedMsg above.
type aileronVersionsFetchedMsg struct {
	versions []string
}

func fetchAileronVersionsCmd() tea.Cmd {
	return func() tea.Msg {
		versions, _ := fetchAileronVersions() // best-effort - cycling just no-ops if this fails
		return aileronVersionsFetchedMsg{versions: versions}
	}
}
