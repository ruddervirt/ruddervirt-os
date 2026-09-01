// SPDX-License-Identifier: GPL-3.0-only

package versions

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeHTTPDoer is a minimal HTTPDoer test double. Respond computes the
// outcome for the request; a nil Respond is a test bug, so it panics
// rather than returning a zero Response.
type fakeHTTPDoer struct {
	lastReq *http.Request
	Respond func(req *http.Request) (*http.Response, error)
}

func (f *fakeHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	f.lastReq = req
	if f.Respond == nil {
		panic("fakeHTTPDoer: Respond not set")
	}
	return f.Respond(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// withFakeHTTPDoer swaps DefaultHTTPClient for fake, restoring the original
// on cleanup.
func withFakeHTTPDoer(t *testing.T, fake *fakeHTTPDoer) {
	t.Helper()
	orig := DefaultHTTPClient
	DefaultHTTPClient = fake
	t.Cleanup(func() { DefaultHTTPClient = orig })
}

func TestFetchGitHubReleasesSuccess(t *testing.T) {
	fake := &fakeHTTPDoer{
		Respond: func(req *http.Request) (*http.Response, error) {
			if got, want := req.URL.String(), "https://api.github.com/repos/ruddervirt/aileron/releases"; got != want {
				t.Errorf("request URL = %q, want %q", got, want)
			}
			if got, want := req.Header.Get("User-Agent"), "ruddervirt-setup"; got != want {
				t.Errorf("User-Agent = %q, want %q", got, want)
			}
			if got, want := req.Header.Get("Accept"), "application/vnd.github+json"; got != want {
				t.Errorf("Accept = %q, want %q", got, want)
			}
			return jsonResponse(http.StatusOK, `[
				{"tag_name": "v1.2.0", "draft": false, "prerelease": false},
				{"tag_name": "v1.3.0-rc1", "draft": false, "prerelease": true},
				{"tag_name": "v1.1.0", "draft": true, "prerelease": false}
			]`), nil
		},
	}
	withFakeHTTPDoer(t, fake)

	releases, err := FetchGitHubReleases("ruddervirt/aileron", "aileron releases")
	if err != nil {
		t.Fatalf("FetchGitHubReleases err = %v, want nil", err)
	}
	if len(releases) != 3 {
		t.Fatalf("FetchGitHubReleases returned %d releases, want 3", len(releases))
	}
	if releases[0].TagName != "v1.2.0" || releases[0].Draft || releases[0].Prerelease {
		t.Errorf("releases[0] = %+v, want tag v1.2.0, draft=false, prerelease=false", releases[0])
	}
	if !releases[1].Prerelease {
		t.Errorf("releases[1].Prerelease = false, want true")
	}
	if !releases[2].Draft {
		t.Errorf("releases[2].Draft = false, want true")
	}
}

func TestFetchGitHubReleasesUnexpectedStatus(t *testing.T) {
	fake := &fakeHTTPDoer{
		Respond: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusForbidden, ""), nil
		},
	}
	withFakeHTTPDoer(t, fake)

	_, err := FetchGitHubReleases("k3s-io/k3s", "k3s releases")
	if err == nil {
		t.Fatal("FetchGitHubReleases err = nil, want an error for non-200 status")
	}
	if !strings.Contains(err.Error(), "k3s releases") {
		t.Errorf("error %q does not mention errDesc %q", err.Error(), "k3s releases")
	}
}

func TestFetchGitHubReleasesMalformedJSON(t *testing.T) {
	fake := &fakeHTTPDoer{
		Respond: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `not valid json`), nil
		},
	}
	withFakeHTTPDoer(t, fake)

	_, err := FetchGitHubReleases("ruddervirt/aileron", "aileron releases")
	if err == nil {
		t.Fatal("FetchGitHubReleases err = nil, want a JSON decode error")
	}
}

func TestFetchGitHubReleasesTransportError(t *testing.T) {
	wantErr := errors.New("network unreachable")
	fake := &fakeHTTPDoer{
		Respond: func(req *http.Request) (*http.Response, error) {
			return nil, wantErr
		},
	}
	withFakeHTTPDoer(t, fake)

	_, err := FetchGitHubReleases("ruddervirt/aileron", "aileron releases")
	if !errors.Is(err, wantErr) {
		t.Errorf("FetchGitHubReleases err = %v, want %v", err, wantErr)
	}
}

func TestFetchLatestGitHubReleaseSuccess(t *testing.T) {
	fake := &fakeHTTPDoer{
		Respond: func(req *http.Request) (*http.Response, error) {
			if got, want := req.URL.String(), "https://api.github.com/repos/ruddervirt/ruddervirt-os/releases/latest"; got != want {
				t.Errorf("request URL = %q, want %q", got, want)
			}
			return jsonResponse(http.StatusOK, `{"tag_name": "v1.4.2", "draft": false, "prerelease": false}`), nil
		},
	}
	withFakeHTTPDoer(t, fake)

	rel, err := FetchLatestGitHubRelease("ruddervirt/ruddervirt-os", "latest release")
	if err != nil {
		t.Fatalf("FetchLatestGitHubRelease err = %v, want nil", err)
	}
	if rel.TagName != "v1.4.2" {
		t.Errorf("rel.TagName = %q, want %q", rel.TagName, "v1.4.2")
	}
}

func TestFetchLatestGitHubReleaseUnexpectedStatus(t *testing.T) {
	fake := &fakeHTTPDoer{
		Respond: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusNotFound, ""), nil
		},
	}
	withFakeHTTPDoer(t, fake)

	_, err := FetchLatestGitHubRelease("ruddervirt/ruddervirt-os", "latest release")
	if err == nil {
		t.Fatal("FetchLatestGitHubRelease err = nil, want an error for non-200 status")
	}
	if !strings.Contains(err.Error(), "latest release") {
		t.Errorf("error %q does not mention errDesc %q", err.Error(), "latest release")
	}
}
