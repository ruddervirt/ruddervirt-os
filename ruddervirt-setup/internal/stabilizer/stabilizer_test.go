// SPDX-License-Identifier: GPL-3.0-only

package stabilizer

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ruddervirt-setup/internal/exec/exectest"
)

func TestResolveNebulaConfig(t *testing.T) {
	t.Run("local path", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yml")
		want := "pki:\n  ca: x\n"
		if err := os.WriteFile(path, []byte(want), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveNebulaConfig(path)
		if err != nil {
			t.Fatalf("ResolveNebulaConfig err = %v, want nil", err)
		}
		if got != want {
			t.Errorf("ResolveNebulaConfig = %q, want %q", got, want)
		}
	})

	t.Run("missing local path errors", func(t *testing.T) {
		if _, err := ResolveNebulaConfig("/nonexistent/path/config.yml"); err == nil {
			t.Fatal("want an error for a missing file")
		}
	})

	t.Run("URL 200", func(t *testing.T) {
		want := "pki:\n  ca: from-url\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(want))
		}))
		defer srv.Close()
		got, err := ResolveNebulaConfig(srv.URL)
		if err != nil {
			t.Fatalf("ResolveNebulaConfig err = %v, want nil", err)
		}
		if got != want {
			t.Errorf("ResolveNebulaConfig = %q, want %q", got, want)
		}
	})

	t.Run("URL non-2xx errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		if _, err := ResolveNebulaConfig(srv.URL); err == nil {
			t.Fatal("want an error for a non-2xx response")
		}
	})
}

func TestValidateNebulaConfig(t *testing.T) {
	valid := "pki:\n  ca: |\n    -----BEGIN CA-----\n  cert: |\n    -----BEGIN CERT-----\n  key: |\n    -----BEGIN KEY-----\n"
	if err := ValidateNebulaConfig(valid); err != nil {
		t.Errorf("ValidateNebulaConfig(valid) err = %v, want nil", err)
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
			if err := ValidateNebulaConfig(c.content); err == nil {
				t.Errorf("ValidateNebulaConfig(%q) err = nil, want an error", c.content)
			}
		})
	}
}

// realSampleNebulaSecretManifest is a real, representative example of what
// ruddervirt hands out for the Nebula mesh identity - a whole `kind: Secret`
// manifest with the Nebula config base64-encoded under data["config.yml"],
// not a bare config file. PKI bodies are placeholder text
// (<REDACTED-BASE64-PEM-BODY>), not real key material.
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
		got, err := ExtractNebulaConfig(realSampleNebulaSecretManifest)
		if err != nil {
			t.Fatalf("ExtractNebulaConfig err = %v, want nil", err)
		}
		if !strings.Contains(got, "pki:") {
			t.Fatalf("extracted content doesn't look like a nebula config:\n%s", got)
		}
		if err := ValidateNebulaConfig(got); err != nil {
			t.Errorf("ValidateNebulaConfig(extracted) err = %v, want nil - this is exactly the \"too strict\" bug: validating the outer Secret envelope instead of the unwrapped config", err)
		}
	})

	t.Run("a bare nebula config passes through unchanged", func(t *testing.T) {
		bare := "pki:\n  ca: x\n  cert: y\n  key: z\n"
		got, err := ExtractNebulaConfig(bare)
		if err != nil {
			t.Fatalf("ExtractNebulaConfig err = %v, want nil", err)
		}
		if got != bare {
			t.Errorf("ExtractNebulaConfig(bare config) = %q, want it unchanged: %q", got, bare)
		}
	})

	t.Run("unwraps stringData (unencoded)", func(t *testing.T) {
		manifest := "kind: Secret\nstringData:\n  config.yml: |\n    pki:\n      ca: x\n      cert: y\n      key: z\n"
		got, err := ExtractNebulaConfig(manifest)
		if err != nil {
			t.Fatalf("ExtractNebulaConfig err = %v, want nil", err)
		}
		if err := ValidateNebulaConfig(got); err != nil {
			t.Errorf("ValidateNebulaConfig(extracted stringData) err = %v, want nil", err)
		}
	})

	t.Run("falls back to a single unambiguous data entry under another key name", func(t *testing.T) {
		manifest := "kind: Secret\ndata:\n  nebula.yml: cGtpOgogIGNhOiB4CiAgY2VydDogeQogIGtleTogeg==\n" // base64 of "pki:\n  ca: x\n  cert: y\n  key: z\n"
		got, err := ExtractNebulaConfig(manifest)
		if err != nil {
			t.Fatalf("ExtractNebulaConfig err = %v, want nil", err)
		}
		if err := ValidateNebulaConfig(got); err != nil {
			t.Errorf("ValidateNebulaConfig err = %v, want nil", err)
		}
	})

	t.Run("Secret with no config.yml and multiple ambiguous entries errors clearly", func(t *testing.T) {
		manifest := "kind: Secret\ndata:\n  a.yml: eA==\n  b.yml: eQ==\n"
		if _, err := ExtractNebulaConfig(manifest); err == nil {
			t.Error("want an error for an ambiguous Secret with no config.yml key")
		}
	})

	t.Run("invalid base64 in data errors", func(t *testing.T) {
		manifest := "kind: Secret\ndata:\n  config.yml: \"not-valid-base64!!!\"\n"
		if _, err := ExtractNebulaConfig(manifest); err == nil {
			t.Error("want an error for invalid base64")
		}
	})
}

func TestNatsAuthSecretManifest(t *testing.T) {
	rendered := natsAuthSecretManifest("alice", "s3cr3t")
	if !strings.Contains(rendered, "name: "+NatsAuthSecretName) {
		t.Errorf("manifest missing secret name: %s", rendered)
	}
	if !strings.Contains(rendered, "namespace: "+StabilizerNamespace) {
		t.Errorf("manifest missing namespace: %s", rendered)
	}
}

func TestNebulaSecretManifest(t *testing.T) {
	rendered := nebulaSecretManifest("pki:\n  ca: x\n")
	if !strings.Contains(rendered, "name: "+NebulaSecretName) {
		t.Errorf("manifest missing secret name: %s", rendered)
	}
	if !strings.Contains(rendered, "config.yml:") {
		t.Errorf("manifest missing config.yml key: %s", rendered)
	}
}

func TestSetAndClearPendingSecrets(t *testing.T) {
	SetPendingSecrets("user", "pass", "config")
	if pendingStabilizerNatsUser != "user" || pendingStabilizerNatsPassword != "pass" || pendingStabilizerNebulaConfig != "config" {
		t.Errorf("SetPendingSecrets left state: user=%q password=%q nebula=%q",
			pendingStabilizerNatsUser, pendingStabilizerNatsPassword, pendingStabilizerNebulaConfig)
	}
	ClearPendingSecrets()
	if pendingStabilizerNatsUser != "" || pendingStabilizerNatsPassword != "" || pendingStabilizerNebulaConfig != "" {
		t.Errorf("ClearPendingSecrets left state: user=%q password=%q nebula=%q",
			pendingStabilizerNatsUser, pendingStabilizerNatsPassword, pendingStabilizerNebulaConfig)
	}
}

func TestStandaloneAileronReleasePresent(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte(`{"items":[{"metadata":{"name":"aileron","namespace":"ns","labels":{}},"data":{"release":""}}]}`)}
		}}
		var got bool
		exectest.WithFakeRunner(r, func() { got = StandaloneAileronReleasePresent("/usr/local/bin/kubectl") })
		if !got {
			t.Error("StandaloneAileronReleasePresent = false, want true")
		}
	})

	t.Run("absent", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte(`{"items":[]}`)}
		}}
		var got bool
		exectest.WithFakeRunner(r, func() { got = StandaloneAileronReleasePresent("/usr/local/bin/kubectl") })
		if got {
			t.Error("StandaloneAileronReleasePresent = true, want false")
		}
	})
}

func TestNonEmptyField(t *testing.T) {
	if _, err := NonEmptyField("zone", "  "); err == nil {
		t.Error("want an error for a blank value")
	}
	got, err := NonEmptyField("zone", "  east1  ")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != "east1" {
		t.Errorf("got %q, want trimmed east1", got)
	}
}

// Compile-time sanity: AdoptSteps must exist and never be nil - the "must
// never be appended into package main's global installSteps" check lives in
// stabilizer_bridge_test.go (TestAdoptStepsIsSeparateFromInstallSteps),
// since this package can't import package main.
func TestAdoptStepsNonEmpty(t *testing.T) {
	if len(AdoptSteps) == 0 {
		t.Fatal("AdoptSteps is empty")
	}
}
