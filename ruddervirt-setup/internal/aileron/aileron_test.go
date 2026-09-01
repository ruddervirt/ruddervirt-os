// SPDX-License-Identifier: GPL-3.0-only

package aileron

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"ruddervirt-setup/internal/exec/exectest"
	"ruddervirt-setup/internal/network"
	"ruddervirt-setup/internal/versions"
)

func TestAileronUIURL(t *testing.T) {
	t.Run("disabled returns empty", func(t *testing.T) {
		got := AileronUIURL(false, network.NetworkConfig{Addressing: "static", StaticIP: "10.0.0.5"})
		if got != "" {
			t.Errorf("AileronUIURL(disabled) = %q, want empty", got)
		}
	})
	t.Run("unresolvable address returns empty", func(t *testing.T) {
		got := AileronUIURL(true, network.NetworkConfig{Addressing: "static"})
		if got != "" {
			t.Errorf("AileronUIURL(no static IP) = %q, want empty", got)
		}
	})
	t.Run("enabled with a resolvable static IP returns the NodePort URL", func(t *testing.T) {
		got := AileronUIURL(true, network.NetworkConfig{Addressing: "static", StaticIP: "10.0.0.5"})
		want := "http://10.0.0.5:30806"
		if got != want {
			t.Errorf("AileronUIURL(static) = %q, want %q", got, want)
		}
	})
}

func TestStabilizerChartPresent(t *testing.T) {
	t.Run("no cached sudo ticket short-circuits to false without a kubectl call", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "sudo", "-n", "true") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			t.Errorf("unexpected command: %s %v", name, args)
			return exectest.Outcome{}
		}}
		var got bool
		exectest.WithFakeRunner(r, func() { got = StabilizerChartPresent() })
		if got {
			t.Error("StabilizerChartPresent() = true, want false when there's no cached sudo ticket")
		}
	})

	t.Run("kubectl returns a matching HelmChart", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "sudo", "-n", "true") {
				return exectest.Outcome{}
			}
			if exectest.CmdContains(name, args, "get", "helmchart", "metadata.name=stabilizer") {
				return exectest.Outcome{Out: []byte("helmchart.k3s.cattle.io/stabilizer\n")}
			}
			t.Errorf("unexpected command: %s %v", name, args)
			return exectest.Outcome{}
		}}
		var got bool
		exectest.WithFakeRunner(r, func() { got = StabilizerChartPresent() })
		if !got {
			t.Error("StabilizerChartPresent() = false, want true when kubectl returns a match")
		}
	})

	t.Run("kubectl returns nothing", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "sudo", "-n", "true") {
				return exectest.Outcome{}
			}
			return exectest.Outcome{Out: []byte("")}
		}}
		var got bool
		exectest.WithFakeRunner(r, func() { got = StabilizerChartPresent() })
		if got {
			t.Error("StabilizerChartPresent() = true, want false when kubectl returns no rows")
		}
	})

	t.Run("kubectl call fails", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "sudo", "-n", "true") {
				return exectest.Outcome{}
			}
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		var got bool
		exectest.WithFakeRunner(r, func() { got = StabilizerChartPresent() })
		if got {
			t.Error("StabilizerChartPresent() = true, want false when the kubectl call itself errors")
		}
	})
}

// fakeHTTPDoer is a minimal versions.HTTPDoer test double, local to this
// package's tests since versions_test.go's own is unexported to package versions.
type fakeHTTPDoer struct {
	respond func(req *http.Request) (*http.Response, error)
}

func (f *fakeHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return f.respond(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body))}
}

func withFakeHTTPDoer(t *testing.T, fake *fakeHTTPDoer) {
	t.Helper()
	orig := versions.DefaultHTTPClient
	versions.DefaultHTTPClient = fake
	t.Cleanup(func() { versions.DefaultHTTPClient = orig })
}

func TestFetchAileronVersions(t *testing.T) {
	t.Run("filters drafts/prereleases/non-semver and sorts newest first", func(t *testing.T) {
		withFakeHTTPDoer(t, &fakeHTTPDoer{respond: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`[
				{"tag_name": "v1.0.0"},
				{"tag_name": "v2.1.0"},
				{"tag_name": "v2.0.0", "draft": true},
				{"tag_name": "v3.0.0", "prerelease": true},
				{"tag_name": "not-a-version"}
			]`), nil
		}})
		got, err := FetchAileronVersions()
		if err != nil {
			t.Fatalf("FetchAileronVersions err = %v, want nil", err)
		}
		want := []string{"v2.1.0", "v1.0.0"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("FetchAileronVersions = %v, want %v", got, want)
		}
	})

	t.Run("no matching releases is an error", func(t *testing.T) {
		withFakeHTTPDoer(t, &fakeHTTPDoer{respond: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`[{"tag_name": "not-a-version"}]`), nil
		}})
		if _, err := FetchAileronVersions(); err == nil {
			t.Fatal("FetchAileronVersions() err = nil, want an error when nothing matches")
		}
	})
}
