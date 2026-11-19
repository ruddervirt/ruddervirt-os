// SPDX-License-Identifier: GPL-3.0-only

package selfupdate

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type fakeCLI struct {
	latest      string
	latestErr   error
	checksumErr error
	installErr  error
	installed   []string
}

func (f *fakeCLI) deps(current string, stdout, stderr *bytes.Buffer) cliDeps {
	return cliDeps{
		current: current,
		fetchLatest: func() (GHRelease, error) {
			return GHRelease{TagName: f.latest}, f.latestErr
		},
		fetchChecksum: func(string) (string, error) { return "abc", f.checksumErr },
		install: func(url, sum string) error {
			f.installed = append(f.installed, url+"#"+sum)
			return f.installErr
		},
		stdout: stdout,
		stderr: stderr,
	}
}

func TestRunUpdateCLI(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name        string
		args        []string
		current     string
		fake        fakeCLI
		wantCode    int
		wantStdout  string
		wantInstall bool
	}{
		{name: "up to date", args: []string{"--confirm"}, current: "v1.2.0", fake: fakeCLI{latest: "v1.2.0"}, wantStdout: "is up to date (v1.2.0)"},
		{name: "newer local", args: []string{"--confirm"}, current: "v1.3.0", fake: fakeCLI{latest: "v1.2.0"}, wantStdout: "is up to date"},
		{name: "dry run", current: "v1.1.0", fake: fakeCLI{latest: "v1.2.0"}, wantStdout: "update available: v1.1.0 -> v1.2.0"},
		{name: "confirm installs", args: []string{"--confirm"}, current: "v1.1.0", fake: fakeCLI{latest: "v1.2.0"}, wantStdout: "updated: v1.1.0 -> v1.2.0", wantInstall: true},
		{name: "dev build updates", args: []string{"--confirm"}, current: "dev", fake: fakeCLI{latest: "v1.2.0"}, wantStdout: "updated: dev -> v1.2.0", wantInstall: true},
		{name: "fetch error", args: []string{"--confirm"}, current: "v1.1.0", fake: fakeCLI{latestErr: boom}, wantCode: 1},
		{name: "checksum error", args: []string{"--confirm"}, current: "v1.1.0", fake: fakeCLI{latest: "v1.2.0", checksumErr: boom}, wantCode: 1},
		{name: "install error", args: []string{"--confirm"}, current: "v1.1.0", fake: fakeCLI{latest: "v1.2.0", installErr: boom}, wantCode: 1, wantInstall: true},
		{name: "bad flag", args: []string{"--nope"}, current: "v1.1.0", wantCode: 2},
		{name: "stray arg", args: []string{"now"}, current: "v1.1.0", wantCode: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runUpdateCLI(tt.args, tt.fake.deps(tt.current, &stdout, &stderr))
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if got := len(tt.fake.installed) > 0; got != tt.wantInstall {
				t.Errorf("installed = %v, want %v", tt.fake.installed, tt.wantInstall)
			}
			if tt.wantInstall && tt.fake.installed[0] != SetupBinaryURL("v1.2.0")+"#abc" {
				t.Errorf("install called with %q", tt.fake.installed[0])
			}
		})
	}
}
