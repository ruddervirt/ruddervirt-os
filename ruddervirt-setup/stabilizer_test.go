// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestResolveNebulaConfig(t *testing.T) {
	t.Run("local path", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yml")
		want := "pki:\n  ca: x\n"
		if err := os.WriteFile(path, []byte(want), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := resolveNebulaConfig(path)
		if err != nil {
			t.Fatalf("resolveNebulaConfig err = %v, want nil", err)
		}
		if got != want {
			t.Errorf("resolveNebulaConfig = %q, want %q", got, want)
		}
	})

	t.Run("missing local path errors", func(t *testing.T) {
		if _, err := resolveNebulaConfig("/nonexistent/path/config.yml"); err == nil {
			t.Fatal("want an error for a missing file")
		}
	})

	t.Run("URL 200", func(t *testing.T) {
		want := "pki:\n  ca: from-url\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(want))
		}))
		defer srv.Close()
		got, err := resolveNebulaConfig(srv.URL)
		if err != nil {
			t.Fatalf("resolveNebulaConfig err = %v, want nil", err)
		}
		if got != want {
			t.Errorf("resolveNebulaConfig = %q, want %q", got, want)
		}
	})

	t.Run("URL non-2xx errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		if _, err := resolveNebulaConfig(srv.URL); err == nil {
			t.Fatal("want an error for a non-2xx response")
		}
	})
}

func TestValidateNebulaConfig(t *testing.T) {
	valid := "pki:\n  ca: |\n    -----BEGIN CA-----\n  cert: |\n    -----BEGIN CERT-----\n  key: |\n    -----BEGIN KEY-----\n"
	if err := validateNebulaConfig(valid); err != nil {
		t.Errorf("validateNebulaConfig(valid) err = %v, want nil", err)
	}

	cases := []struct {
		name    string
		content string
	}{
		{"not YAML", "not: [valid: yaml"},
		{"missing pki entirely", "foo: bar\n"},
		{"missing key", "pki:\n  ca: x\n  cert: y\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validateNebulaConfig(c.content); err == nil {
				t.Errorf("validateNebulaConfig(%q) err = nil, want an error", c.content)
			}
		})
	}
}

// realSampleNebulaSecretManifest is a real, representative example of what
// ruddervirt actually hands out for the Nebula mesh identity - a whole
// `kind: Secret` manifest with the actual Nebula config base64-encoded
// under data["config.yml"] - not a bare Nebula config file. The PKI bodies
// are placeholder text (<REDACTED-BASE64-PEM-BODY>), not real key material.
const realSampleNebulaSecretManifest = `apiVersion: v1
kind: Secret
metadata:
  name: stabilizer-nebula
  namespace: ruddervirt-system
type: Opaque
data:
  config.yml: IyBTZWxmLWNvbnRhaW5lZCBjb25maWcgZm9yIGV4YW1wbGUtaG9zdCDigJQgQ0EvY2VydC9rZXkgaW5saW5lZCBhcyBQRU0uCnBraToKICBjYTogfAogICAgLS0tLS1CRUdJTiBORUJVTEEgQ0VSVElGSUNBVEUtLS0tLQogICAgPFJFREFDVEVELUJBU0U2NC1QRU0tQk9EWT4KICAgIC0tLS0tRU5EIE5FQlVMQSBDRVJUSUZJQ0FURS0tLS0tCiAgY2VydDogfAogICAgLS0tLS1CRUdJTiBORUJVTEEgQ0VSVElGSUNBVEUtLS0tLQogICAgPFJFREFDVEVELUJBU0U2NC1QRU0tQk9EWT4KICAgIC0tLS0tRU5EIE5FQlVMQSBDRVJUSUZJQ0FURS0tLS0tCiAga2V5OiB8CiAgICAtLS0tLUJFR0lOIE5FQlVMQSBYMjU1MTkgUFJJVkFURSBLRVktLS0tLQogICAgPFJFREFDVEVELUJBU0U2NC1QRU0tQk9EWT4KICAgIC0tLS0tRU5EIE5FQlVMQSBYMjU1MTkgUFJJVkFURSBLRVktLS0tLQpzdGF0aWNfaG9zdF9tYXA6CiAgIjE3Mi4xNi4xMDAuMSI6IFsiMjAzLjAuMTEzLjEwOjQyNDIiXQpsaWdodGhvdXNlOgogIGFtX2xpZ2h0aG91c2U6IGZhbHNlCiAgaW50ZXJ2YWw6IDYwCiAgaG9zdHM6CiAgICAtICIxNzIuMTYuMTAwLjEiCnJlbGF5OgogIHJlbGF5czoKICAgIC0gIjE3Mi4xNi4xMDAuMSIKICBhbV9yZWxheTogZmFsc2UKICB1c2VfcmVsYXlzOiB0cnVlCmxpc3RlbjoKICBob3N0OiAwLjAuMC4wCiAgIyBQZXItaG9zdCBvdmVycmlkZSB2aWEgYGxpc3Rlbl9wb3J0YCBpbiBpbnZlbnRvcnkgKDAgPSBlcGhlbWVyYWwsIG91dGJvdW5kLW9ubHkpLgogIHBvcnQ6IDQyNDIKcHVuY2h5OgogIHB1bmNoOiB0cnVlCiAgcmVzcG9uZDogdHJ1ZQogIGRlbGF5OiAxcwpjaXBoZXI6IGFlcwp0dW46CiAgZGlzYWJsZWQ6IGZhbHNlCiAgZGV2OiBuZWJ1bGExCiAgZHJvcF9sb2NhbF9icm9hZGNhc3Q6IGZhbHNlCiAgZHJvcF9tdWx0aWNhc3Q6IGZhbHNlCiAgdHhfcXVldWU6IDUwMAogIG10dTogMTMwMAogIHJvdXRlczoKICB1bnNhZmVfcm91dGVzOgpsb2dnaW5nOgogIGxldmVsOiBpbmZvCiAgZm9ybWF0OiB0ZXh0CmZpcmV3YWxsOgogIGNvbm50cmFjazoKICAgIHRjcF90aW1lb3V0OiAxMm0KICAgIHVkcF90aW1lb3V0OiAzbQogICAgZGVmYXVsdF90aW1lb3V0OiAxMG0KICBvdXRib3VuZDoKICAgIC0gcG9ydDogYW55CiAgICAgIHByb3RvOiBhbnkKICAgICAgaG9zdDogYW55CiAgaW5ib3VuZDoKICAgIC0gcG9ydDogYW55CiAgICAgIHByb3RvOiBhbnkKICAgICAgaG9zdDogYW55Cg==
`

func TestExtractNebulaConfig(t *testing.T) {
	t.Run("unwraps a real whole Secret manifest (data, base64)", func(t *testing.T) {
		got, err := extractNebulaConfig(realSampleNebulaSecretManifest)
		if err != nil {
			t.Fatalf("extractNebulaConfig err = %v, want nil", err)
		}
		if !strings.Contains(got, "pki:") {
			t.Fatalf("extracted content doesn't look like a nebula config:\n%s", got)
		}
		if err := validateNebulaConfig(got); err != nil {
			t.Errorf("validateNebulaConfig(extracted) err = %v, want nil - this is exactly the \"too strict\" bug: validating the outer Secret envelope instead of the unwrapped config", err)
		}
	})

	t.Run("a bare nebula config passes through unchanged", func(t *testing.T) {
		bare := "pki:\n  ca: x\n  cert: y\n  key: z\n"
		got, err := extractNebulaConfig(bare)
		if err != nil {
			t.Fatalf("extractNebulaConfig err = %v, want nil", err)
		}
		if got != bare {
			t.Errorf("extractNebulaConfig(bare config) = %q, want it unchanged: %q", got, bare)
		}
	})

	t.Run("unwraps stringData (unencoded)", func(t *testing.T) {
		manifest := "kind: Secret\nstringData:\n  config.yml: |\n    pki:\n      ca: x\n      cert: y\n      key: z\n"
		got, err := extractNebulaConfig(manifest)
		if err != nil {
			t.Fatalf("extractNebulaConfig err = %v, want nil", err)
		}
		if err := validateNebulaConfig(got); err != nil {
			t.Errorf("validateNebulaConfig(extracted stringData) err = %v, want nil", err)
		}
	})

	t.Run("falls back to a single unambiguous data entry under another key name", func(t *testing.T) {
		manifest := "kind: Secret\ndata:\n  nebula.yml: cGtpOgogIGNhOiB4CiAgY2VydDogeQogIGtleTogeg==\n" // base64 of "pki:\n  ca: x\n  cert: y\n  key: z\n"
		got, err := extractNebulaConfig(manifest)
		if err != nil {
			t.Fatalf("extractNebulaConfig err = %v, want nil", err)
		}
		if err := validateNebulaConfig(got); err != nil {
			t.Errorf("validateNebulaConfig err = %v, want nil", err)
		}
	})

	t.Run("Secret with no config.yml and multiple ambiguous entries errors clearly", func(t *testing.T) {
		manifest := "kind: Secret\ndata:\n  a.yml: eA==\n  b.yml: eQ==\n"
		if _, err := extractNebulaConfig(manifest); err == nil {
			t.Error("want an error for an ambiguous Secret with no config.yml key")
		}
	})

	t.Run("invalid base64 in data errors", func(t *testing.T) {
		manifest := "kind: Secret\ndata:\n  config.yml: \"not-valid-base64!!!\"\n"
		if _, err := extractNebulaConfig(manifest); err == nil {
			t.Error("want an error for invalid base64")
		}
	})
}

func TestNatsAuthSecretManifest(t *testing.T) {
	rendered := natsAuthSecretManifest("alice", "s3cr3t")
	if !strings.Contains(rendered, "name: "+natsAuthSecretName) {
		t.Errorf("manifest missing secret name: %s", rendered)
	}
	if !strings.Contains(rendered, "namespace: "+stabilizerNamespace) {
		t.Errorf("manifest missing namespace: %s", rendered)
	}
}

func TestNebulaSecretManifest(t *testing.T) {
	rendered := nebulaSecretManifest("pki:\n  ca: x\n")
	if !strings.Contains(rendered, "name: "+nebulaSecretName) {
		t.Errorf("manifest missing secret name: %s", rendered)
	}
	if !strings.Contains(rendered, "config.yml:") {
		t.Errorf("manifest missing config.yml key: %s", rendered)
	}
}

func TestClearPendingStabilizerSecrets(t *testing.T) {
	pendingStabilizerNatsUser = "user"
	pendingStabilizerNatsPassword = "pass"
	pendingStabilizerNebulaConfig = "config"
	clearPendingStabilizerSecrets()
	if pendingStabilizerNatsUser != "" || pendingStabilizerNatsPassword != "" || pendingStabilizerNebulaConfig != "" {
		t.Errorf("clearPendingStabilizerSecrets left state: user=%q password=%q nebula=%q",
			pendingStabilizerNatsUser, pendingStabilizerNatsPassword, pendingStabilizerNebulaConfig)
	}
}

func TestStandaloneAileronReleasePresent(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte(`{"items":[{"metadata":{"name":"aileron","namespace":"ns","labels":{}},"data":{"release":""}}]}`)}
		}}
		var got bool
		withFakeRunner(r, func() { got = standaloneAileronReleasePresent("/usr/local/bin/kubectl") })
		if !got {
			t.Error("standaloneAileronReleasePresent = false, want true")
		}
	})

	t.Run("absent", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte(`{"items":[]}`)}
		}}
		var got bool
		withFakeRunner(r, func() { got = standaloneAileronReleasePresent("/usr/local/bin/kubectl") })
		if got {
			t.Error("standaloneAileronReleasePresent = true, want false")
		}
	})
}

func TestCheckAileronReadyCmd(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			switch {
			case cmdContains(name, args, "systemctl", "is-active", "k3s.service"):
				return commandOutcome{}
			case cmdContains(name, args, "wait", "deployment.apps/aileron"):
				return commandOutcome{}
			}
			t.Errorf("unexpected command: %s %v", name, args)
			return commandOutcome{}
		}}
		var msg tea.Msg
		withFakeRunner(r, func() { msg = checkAileronReadyCmd("/usr/local/bin/kubectl")() })
		got, ok := msg.(aileronReadyCheckMsg)
		if !ok || !got.ready {
			t.Errorf("checkAileronReadyCmd() = %#v, want aileronReadyCheckMsg{ready: true}", msg)
		}
	})

	t.Run("not ready", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{err: errFake}
		}}
		var msg tea.Msg
		withFakeRunner(r, func() { msg = checkAileronReadyCmd("/usr/local/bin/kubectl")() })
		got, ok := msg.(aileronReadyCheckMsg)
		if !ok || got.ready {
			t.Errorf("checkAileronReadyCmd() = %#v, want aileronReadyCheckMsg{ready: false}", msg)
		}
	})

	// Regression test: before k3s is actually installed, /usr/local/bin/k3s
	// is only a placeholder text file with no `#!` shebang (server.bu).
	// POSIX shell's ENOEXEC fallback silently reinterprets that as a no-op
	// shell script, so kubectl (which execs through it) reports a false
	// SUCCESS - a bare `wait deployment.apps/aileron` call alone would
	// wrongly report "ready" on a node that was never installed at all.
	// k3sServiceActive() must be checked first to catch this.
	t.Run("kubectl false-succeeds via the k3s placeholder script but k3s.service isn't active", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			if cmdContains(name, args, "systemctl", "is-active", "k3s.service") {
				return commandOutcome{err: errFake} // real environment: not active
			}
			// Simulates the placeholder-script trap: any kubectl call
			// "succeeds" with no real effect.
			return commandOutcome{}
		}}
		var msg tea.Msg
		withFakeRunner(r, func() { msg = checkAileronReadyCmd("/usr/local/bin/kubectl")() })
		got, ok := msg.(aileronReadyCheckMsg)
		if !ok || got.ready {
			t.Errorf("checkAileronReadyCmd() = %#v, want ready=false when k3s.service isn't active, even though kubectl itself reports success", msg)
		}
	})
}

func TestStabilizerNonEmptyField(t *testing.T) {
	if _, err := stabilizerNonEmptyField("zone", "  "); err == nil {
		t.Error("want an error for a blank value")
	}
	got, err := stabilizerNonEmptyField("zone", "  east1  ")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != "east1" {
		t.Errorf("got %q, want trimmed east1", got)
	}
}

// Compile-time sanity: stabilizerSteps must exist and never be nil, and
// must be a distinct slice from installSteps - it must never accidentally
// get appended to the global fresh-install pipeline.
func TestStabilizerStepsIsSeparateFromInstallSteps(t *testing.T) {
	if len(stabilizerSteps) == 0 {
		t.Fatal("stabilizerSteps is empty")
	}
	for _, s := range installSteps {
		for _, ss := range stabilizerSteps {
			if s.label == ss.label {
				t.Errorf("stabilizerSteps step %q also appears in the global installSteps pipeline", ss.label)
			}
		}
	}
}
