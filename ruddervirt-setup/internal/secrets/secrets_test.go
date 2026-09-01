// SPDX-License-Identifier: GPL-3.0-only

package secrets

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/exec/exectest"
)

// testMsg is a stand-in exec.StepMsg value used only by these tests.
type testMsg string

func testWrap(line string) exec.StepMsg { return testMsg(line) }

// drainTestStrings collects every testMsg sent to ch without blocking the
// caller.
func drainTestStrings(ch chan exec.StepMsg) []string {
	var out []string
	for {
		select {
		case msg := <-ch:
			if s, ok := msg.(testMsg); ok {
				out = append(out, string(s))
			}
		default:
			return out
		}
	}
}

func TestSecretManifestYAML(t *testing.T) {
	data := map[string][]byte{
		"user":     []byte("alice"),
		"password": []byte("s3cr3t\nwith-newline"),
	}
	rendered := SecretManifestYAML("my-secret", "my-ns", data)

	var parsed struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct{ Name, Namespace string }
		Type       string
		Data       map[string]string
	}
	if err := yaml.Unmarshal([]byte(rendered), &parsed); err != nil {
		t.Fatalf("SecretManifestYAML produced invalid YAML: %v\n%s", err, rendered)
	}
	if parsed.APIVersion != "v1" || parsed.Kind != "Secret" {
		t.Errorf("apiVersion/kind = %q/%q, want v1/Secret", parsed.APIVersion, parsed.Kind)
	}
	if parsed.Metadata.Name != "my-secret" || parsed.Metadata.Namespace != "my-ns" {
		t.Errorf("metadata = %+v, want name=my-secret namespace=my-ns", parsed.Metadata)
	}
	for k, want := range data {
		got, ok := parsed.Data[k]
		if !ok {
			t.Fatalf("data missing key %q", k)
		}
		decoded, err := base64.StdEncoding.DecodeString(got)
		if err != nil {
			t.Fatalf("data[%q] isn't valid base64: %v", k, err)
		}
		if string(decoded) != string(want) {
			t.Errorf("data[%q] round-trip = %q, want %q", k, decoded, want)
		}
	}
}

func TestApplySecretManifest(t *testing.T) {
	t.Run("applies via kubectl and cleans up the tempfile", func(t *testing.T) {
		var seenPath string
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "kubectl", "apply", "-f") {
				seenPath = args[len(args)-1]
				if _, err := os.Stat(seenPath); err != nil {
					t.Errorf("kubectl apply -f ran with a tempfile that doesn't exist: %v", err)
				}
			}
			return exectest.Outcome{Out: []byte("secret/my-secret created")}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = ApplySecretManifest(ch, testWrap, "/usr/local/bin/kubectl", "irrelevant: content") })
		if err != nil {
			t.Fatalf("ApplySecretManifest err = %v, want nil", err)
		}
		if seenPath == "" {
			t.Fatal("kubectl apply -f was never called")
		}
		if _, statErr := os.Stat(seenPath); statErr == nil {
			t.Errorf("tempfile %s still exists after ApplySecretManifest returned", seenPath)
		}
	})

	t.Run("never leaks secret content into streamed output", func(t *testing.T) {
		const marker = "uniquely-tagged-secret-value-xyzzy"
		manifest := SecretManifestYAML("stabilizer-nats-auth", "ruddervirt-system", map[string][]byte{
			"password": []byte(marker),
		})
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("secret/stabilizer-nats-auth created")}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = ApplySecretManifest(ch, testWrap, "/usr/local/bin/kubectl", manifest) })
		if err != nil {
			t.Fatalf("ApplySecretManifest err = %v, want nil", err)
		}
		for _, line := range drainTestStrings(ch) {
			if strings.Contains(line, marker) {
				t.Fatalf("streamed output leaked secret content: %q", line)
			}
		}
	})

	t.Run("propagates kubectl failure", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Err: exectest.ErrFake, Out: []byte("some error")}
		}}
		ch := make(chan exec.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { err = ApplySecretManifest(ch, testWrap, "/usr/local/bin/kubectl", "content") })
		if err == nil {
			t.Fatal("ApplySecretManifest err = nil, want an error")
		}
	})
}
