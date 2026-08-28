// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// secretManifestYAML renders a `kind: Secret` object with every value in
// data already base64-encoded into the `data:` field (never `stringData:`)
// - encoding it explicitly here, rather than relying on the apiserver's own
// stringData conversion, means the exact bytes written to the tempfile are
// already exactly what this function computed, and arbitrary multi-line
// secret content (e.g. a Nebula config.yml) can never break the manifest's
// own YAML structure. Keys are sorted for deterministic output (useful for
// tests); name/namespace are always trusted package constants, never
// operator input.
func secretManifestYAML(name, namespace string, data map[string][]byte) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\n  namespace: %s\ntype: Opaque\ndata:\n", name, namespace)
	for _, k := range keys {
		fmt.Fprintf(&b, "  %s: %s\n", k, base64.StdEncoding.EncodeToString(data[k]))
	}
	return b.String()
}

// applySecretManifest writes manifestYAML to a tempfile - os.CreateTemp
// already creates it 0600 (readable only by the user that ran
// ruddervirt-setup and, since `kubectl apply` below runs under sudo, by
// root), tightened explicitly for defense-in-depth - applies it with
// `kubectl apply -f`, and removes the tempfile immediately after, success or
// failure. Safe to stream via runStreamed for the apply itself - `kubectl
// apply -f <tmp>` only ever echoes "secret/<name> created" or similar, never
// the file's content - but this function must NEVER pass manifestYAML
// itself to ch, and any change here must preserve that.
func applySecretManifest(ch chan<- tea.Msg, kubectlBin, manifestYAML string) error {
	tmp, err := os.CreateTemp("", "ruddervirt-setup-secret-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(manifestYAML); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		return err
	}

	return runStreamed(ch, kubectlBin, "apply", "-f", tmpPath)
}
