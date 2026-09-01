// SPDX-License-Identifier: GPL-3.0-only

package secrets

import (
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strings"

	"ruddervirt-setup/internal/exec"
)

// SecretManifestYAML renders a `kind: Secret` object with every value in
// data already base64-encoded into the `data:` field (never `stringData:`),
// so arbitrary multi-line secret content (e.g. a Nebula config.yml) can
// never break the manifest's own YAML structure. Keys are sorted for
// deterministic output; name/namespace are always trusted package
// constants, never operator input.
func SecretManifestYAML(name, namespace string, data map[string][]byte) string {
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

// ApplySecretManifest writes manifestYAML to a tempfile (os.CreateTemp's
// default 0600 tightened explicitly, defense-in-depth), applies it with
// `kubectl apply -f`, and removes the tempfile after, success or failure.
// Safe to stream via exec.RunStreamed - `kubectl apply -f` only ever echoes
// "secret/<name> created", never the file's content - but this function
// must NEVER pass manifestYAML itself to ch; any change here must preserve
// that. wrap is the caller's tea.Msg wrapper - this package has none of
// its own.
func ApplySecretManifest(ch chan<- exec.StepMsg, wrap func(line string) exec.StepMsg, kubectlBin, manifestYAML string) error {
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

	return exec.RunStreamed(ch, wrap, kubectlBin, "apply", "-f", tmpPath)
}
