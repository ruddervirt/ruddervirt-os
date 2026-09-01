// SPDX-License-Identifier: GPL-3.0-only

package stabilizer

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/installsteps"
	"ruddervirt-setup/internal/status"
)

// This file ports the standalone stabilizer repo's scripts/adopt-aileron.sh
// into Go, using only kubectl (via the existing CommandRunner seam) - never
// a helm binary, none is shipped on the appliance. Background: stabilizer's
// Helm chart now bundles aileron as a subchart. A cluster that already has
// aileron installed as its own separate Helm release named "aileron"
// (exactly what ruddervirt-setup's own applyAileron, aileron_bridge.go,
// creates on every fresh install) can't just have the merged "stabilizer"
// chart installed on top - Helm refuses because aileron's resources are
// annotated meta.helm.sh/release-name: aileron, not stabilizer.
// Uninstall/reinstall is not an option either: aileron's CRDs are
// cluster-scoped and chart-templated, so dropping the release would
// cascade-delete every live VM. So this re-stamps Helm's ownership metadata
// on the live resources in place instead, without ever touching CRDs or
// running VMs.

// helmReleaseRecord is the JSON shape stored (Helm's own
// base64(gzip(json(...))) encoding) inside a Helm v3 release Secret's
// data["release"] field - only the field this package needs.
type helmReleaseRecord struct {
	Manifest string `json:"manifest"`
}

// decodeHelmReleaseManifest reverses Helm v3's release-storage encoding and
// returns the same text `helm get manifest` would print.
//
// rawReleaseField is expected to already have had the Kubernetes API's own
// base64 layer removed (see k8sSecretGetJSON.Data, a map[string][]byte -
// encoding/json auto-decodes that layer from `kubectl get secret -o
// json`). What's left is Helm's own encoding: base64(gzip(json(release))).
// This function undoes exactly that: base64-decode, gunzip,
// JSON-unmarshal, return .manifest.
//
// The highest-risk, fiddliest piece of the whole port - isolated here
// deliberately, with its own fixture-based tests (adopt_test.go) exercising
// a real captured Helm release secret, not just a hand-constructed one.
func decodeHelmReleaseManifest(rawReleaseField []byte) (string, error) {
	gzipBytes, err := base64.StdEncoding.DecodeString(string(rawReleaseField))
	if err != nil {
		return "", fmt.Errorf("decoding helm release secret: %w", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(gzipBytes))
	if err != nil {
		return "", fmt.Errorf("decompressing helm release secret: %w", err)
	}
	defer gz.Close()
	jsonBytes, err := io.ReadAll(gz)
	if err != nil {
		return "", fmt.Errorf("decompressing helm release secret: %w", err)
	}
	var rec helmReleaseRecord
	if err := json.Unmarshal(jsonBytes, &rec); err != nil {
		return "", fmt.Errorf("parsing helm release secret: %w", err)
	}
	return rec.Manifest, nil
}

// k8sSecretGetJSON is the minimal subset of `kubectl get secret -o json`'s
// shape this package needs - same hand-rolled-struct approach as
// k8sNodeList (k3s.go); this repo has no client-go/apimachinery dependency
// and shouldn't gain one for this. Data is map[string][]byte deliberately
// so encoding/json auto-decodes the Kubernetes API's own base64 layer on
// Secret.data, leaving only Helm's own encoding for
// decodeHelmReleaseManifest to undo.
type k8sSecretGetJSON struct {
	Metadata struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
	Data map[string][]byte `json:"data"`
}

// findDeployedHelmReleaseSecret returns the raw (k8s-API-base64-decoded)
// "release" field bytes of the single Secret in ns labeled
// owner=helm,name=<release>,status=deployed - Helm guarantees exactly one
// such Secret exists; older status=superseded revisions are excluded by
// the label selector. ok=false, err=nil means no such Secret exists -
// "release not installed" is an expected, common state, not a failure.
func findDeployedHelmReleaseSecret(kubectlBin, ns, release string) (raw []byte, ok bool, err error) {
	out, err := exec.RunPrivileged(kubectlBin, "get", "secret", "-n", ns,
		"-l", fmt.Sprintf("owner=helm,name=%s,status=deployed", release),
		"-o", "json").Output()
	if err != nil {
		return nil, false, fmt.Errorf("listing helm release secrets for %s: %w", release, err)
	}
	var list struct {
		Items []k8sSecretGetJSON `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, false, fmt.Errorf("parsing helm release secret list for %s: %w", release, err)
	}
	if len(list.Items) == 0 {
		return nil, false, nil
	}
	if len(list.Items) > 1 {
		return nil, false, fmt.Errorf("found %d deployed helm release secrets for %s in %s, expected at most 1", len(list.Items), release, ns)
	}
	data, ok := list.Items[0].Data["release"]
	if !ok {
		return nil, false, fmt.Errorf("helm release secret for %s has no 'release' key", release)
	}
	return data, true, nil
}

// fetchHelmReleaseManifest composes findDeployedHelmReleaseSecret and
// decodeHelmReleaseManifest - the Go equivalent of `helm get manifest
// <release> -n <ns>`.
func fetchHelmReleaseManifest(kubectlBin, ns, release string) (manifest string, ok bool, err error) {
	raw, ok, err := findDeployedHelmReleaseSecret(kubectlBin, ns, release)
	if err != nil || !ok {
		return "", ok, err
	}
	manifest, err = decodeHelmReleaseManifest(raw)
	if err != nil {
		return "", true, err
	}
	return manifest, true, nil
}

// manifestHasVncGateway reports whether manifest contains a resource
// literally named "<release>-vncgateway" (mirrors adopt-aileron.sh's grep
// '^\s*name:\s+aileron-vncgateway(\s|$)') - the load-bearing Service
// stabilizer's vncAuthProxy targets by name, confirming the release's shape
// actually matches a standalone Aileron install before anything is touched.
func manifestHasVncGateway(manifest, release string) bool {
	want := release + "-vncgateway"
	for _, line := range strings.Split(manifest, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "name:") {
			continue
		}
		if strings.TrimSpace(strings.TrimPrefix(trimmed, "name:")) == want {
			return true
		}
	}
	return false
}

// splitYAMLDocuments splits a "---"-separated multi-document manifest into
// its individual documents, dropping empty ones (leading/trailing
// separators, blank trailing document).
func splitYAMLDocuments(manifest string) []string {
	var docs []string
	for _, part := range strings.Split(manifest, "\n---") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		docs = append(docs, part)
	}
	return docs
}

// k8sObjectRef is the minimal identity parsed out of one YAML document,
// enough to `kubectl get`/operate on it.
type k8sObjectRef struct {
	APIVersion string
	Kind       string
	Name       string
	Namespace  string
}

// parseObjectRef extracts apiVersion/kind/metadata.name/metadata.namespace
// from one YAML document. Returns an error for a document that isn't a real
// object (blank, comment-only, or missing kind/name) - callers treat that
// as "skip this document", mirroring adopt-aileron.sh's own
// `grep -qE '^[[:space:]]*kind:' "$doc" || continue`.
func parseObjectRef(doc string) (k8sObjectRef, error) {
	var parsed struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		return k8sObjectRef{}, err
	}
	if parsed.Kind == "" || parsed.Metadata.Name == "" {
		return k8sObjectRef{}, fmt.Errorf("document has no kind/name")
	}
	return k8sObjectRef{
		APIVersion: parsed.APIVersion,
		Kind:       parsed.Kind,
		Name:       parsed.Metadata.Name,
		Namespace:  parsed.Metadata.Namespace,
	}, nil
}

// objectExists reports whether ref actually exists on the cluster. Uses
// CombinedOutput (not Output) so a NotFound response's text ends up in out,
// distinguishable from any other failure - same "abort loudly on anything
// but NotFound" requirement as adopt-aileron.sh's live-object filtering.
func objectExists(kubectlBin string, ref k8sObjectRef) (bool, error) {
	args := []string{"get", ref.Kind, ref.Name, "-o", "name"}
	if ref.Namespace != "" {
		args = append(args, "-n", ref.Namespace)
	}
	out, err := exec.RunPrivileged(kubectlBin, args...).CombinedOutput()
	if err == nil {
		return true, nil
	}
	if strings.Contains(strings.ToLower(string(out)), "not found") {
		return false, nil
	}
	return false, exec.WrapCmdErr(out, err)
}

// filterLiveManifest checks each of docs against the live cluster and
// returns only the ones that actually exist, re-joined into a single
// "---"-separated manifest. A NotFound response is the ONLY tolerated
// reason to drop a document (the chart can render optional resources this
// cluster never created, e.g. an optional kube-OVN Subnet) - any other
// kubectl error aborts the whole run loudly, per adopt-aileron.sh's
// non-silent-skip requirement.
func filterLiveManifest(ch chan<- installsteps.StepMsg, kubectlBin string, docs []string) (string, error) {
	ch <- installsteps.StepOutputMsg("Checking which rendered resources exist on the cluster...")
	var live []string
	var skipped []string
	for _, doc := range docs {
		ref, err := parseObjectRef(doc)
		if err != nil {
			continue // separator/comment-only artifact, not a real object
		}
		exists, err := objectExists(kubectlBin, ref)
		if err != nil {
			return "", fmt.Errorf("checking %s/%s: %w", ref.Kind, ref.Name, err)
		}
		if !exists {
			skipped = append(skipped, ref.Kind+"/"+ref.Name)
			continue
		}
		live = append(live, doc)
	}
	if len(skipped) > 0 {
		ch <- installsteps.StepOutputMsg(fmt.Sprintf("Skipping %d rendered resource(s) absent on this cluster (the merged install will create them fresh): %s", len(skipped), strings.Join(skipped, ", ")))
	}
	return strings.Join(live, "\n---"), nil
}

// restampHelmOwnership re-labels/annotates every object in liveManifest as
// belonging to newRelease/newNamespace. Deliberately does NOT pass
// -n <namespace> to either kubectl call - the manifest carries each
// object's own namespace (aileron owns at least one cross-namespace
// resource, a NetworkAttachmentDefinition in kube-system), and forcing -n
// would reject any object whose namespace differs. Idempotent (--overwrite).
func restampHelmOwnership(ch chan<- installsteps.StepMsg, kubectlBin, liveManifest, newRelease, newNamespace string) error {
	if strings.TrimSpace(liveManifest) == "" {
		ch <- installsteps.StepOutputMsg("No live resources to re-stamp")
		return nil
	}
	tmp, err := os.CreateTemp("", "adopt-live-manifest-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(liveManifest); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	ch <- installsteps.StepOutputMsg("Re-stamping meta.helm.sh/release-{name,namespace} annotations...")
	if err := exec.RunStreamed(ch, wrapStepOutput, kubectlBin, "annotate", "--overwrite", "-f", tmpPath,
		"meta.helm.sh/release-name="+newRelease,
		"meta.helm.sh/release-namespace="+newNamespace); err != nil {
		return err
	}
	ch <- installsteps.StepOutputMsg("Setting app.kubernetes.io/managed-by=Helm label...")
	return exec.RunStreamed(ch, wrapStepOutput, kubectlBin, "label", "--overwrite", "-f", tmpPath, "app.kubernetes.io/managed-by=Helm")
}

// deleteOldReleaseSecrets removes owner=helm,name=<release> Secrets in ns -
// the k8s resources they describe are untouched; this is what makes the
// orphaned HelmChart CR (retired next) actually orphaned instead of still
// tracked by a live release. --ignore-not-found, so idempotent.
func deleteOldReleaseSecrets(ch chan<- installsteps.StepMsg, kubectlBin, ns, release string) error {
	ch <- installsteps.StepOutputMsg(fmt.Sprintf("Deleting old '%s' release history secrets (resources untouched)...", release))
	return exec.RunStreamed(ch, wrapStepOutput, kubectlBin, "delete", "secret", "-n", ns, "-l", fmt.Sprintf("owner=helm,name=%s", release), "--ignore-not-found")
}

// helmChartCR is the minimal identity of one helm.cattle.io/v1 HelmChart
// object this package needs, plus the k3s "objectset.rio.cattle.io/owner-gvk"
// label value that decides retireHelmChartCR's two branches.
type helmChartCR struct {
	Name            string
	Namespace       string
	TargetNamespace string
	OwnerGVK        string
}

// findOrphanedHelmChartCRs lists every HelmChart CR named release with an
// effective spec.targetNamespace == targetNamespace (kubectl get helmchart
// --all-namespaces -o json, filtered client-side - the field-selector
// stabilizerChartPresent uses, aileron_bridge.go, only supports
// metadata.name, not spec.targetNamespace). An empty spec.targetNamespace
// defaults to the CR's own metadata.namespace, matching helm-controller's
// own behavior. applyAileron (aileron_bridge.go) creates exactly one of
// these (name "aileron", namespace "kube-system", targetNamespace
// "ruddervirt-system"), so this is expected to find it on a ruddervirt-os
// box. A cluster with no helmcharts CRD returns an empty result, not an
// error.
func findOrphanedHelmChartCRs(kubectlBin, release, targetNamespace string) ([]helmChartCR, error) {
	out, err := exec.RunPrivileged(kubectlBin, "get", "helmchart.helm.cattle.io", "--all-namespaces", "-o", "json").CombinedOutput()
	if err != nil {
		lower := strings.ToLower(string(out))
		if strings.Contains(lower, "not found") || strings.Contains(lower, "no matches for kind") || strings.Contains(lower, "doesn't have a resource type") {
			return nil, nil
		}
		return nil, exec.WrapCmdErr(out, err)
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name      string            `json:"name"`
				Namespace string            `json:"namespace"`
				Labels    map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				TargetNamespace string `json:"targetNamespace"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parsing helmchart list: %w", err)
	}
	var crs []helmChartCR
	for _, item := range list.Items {
		if item.Metadata.Name != release {
			continue
		}
		tns := item.Spec.TargetNamespace
		if tns == "" {
			tns = item.Metadata.Namespace
		}
		if tns != targetNamespace {
			continue
		}
		crs = append(crs, helmChartCR{
			Name:            item.Metadata.Name,
			Namespace:       item.Metadata.Namespace,
			TargetNamespace: tns,
			OwnerGVK:        item.Metadata.Labels["objectset.rio.cattle.io/owner-gvk"],
		})
	}
	return crs, nil
}

// retireHelmChartCR retires one orphaned HelmChart CR in the EXACT
// finalizer-drop-before-delete order adopt-aileron.sh's retire_helmchart()
// uses - load-bearing, must not be reordered: (1) patch the finalizers to
// null, (2) delete the HelmChart, (3) delete its install Job. Deleting the
// CR while its wrangler.cattle.io/on-helm-chart-remove finalizer is still
// attached makes helm-controller run a real `helm uninstall`, which -
// because Aileron's CRDs are chart-templated - would cascade-delete every
// live VirtualMachine's CRDs.
//
// If cr.OwnerGVK names a k3s/RKE2 packaged Addon (applied from a manifest
// file on the server node, which would just get recreated on restart),
// this instead emits the exact
// /var/lib/rancher/{k3s,rke2}/server/manifests/*aileron*.yaml removal
// instructions and returns nil without deleting anything.
func retireHelmChartCR(ch chan<- installsteps.StepMsg, kubectlBin string, cr helmChartCR) error {
	if strings.Contains(cr.OwnerGVK, "Addon") {
		ch <- installsteps.StepOutputMsg(fmt.Sprintf(
			"%s/%s is managed by a k3s/RKE2 packaged Addon (%s) - deleting it here would not stick. "+
				"Remove the manifest on EVERY server node, then re-run this: rm /var/lib/rancher/{k3s,rke2}/server/manifests/*aileron*.yaml",
			cr.Namespace, cr.Name, cr.OwnerGVK))
		return nil
	}
	ch <- installsteps.StepOutputMsg(fmt.Sprintf("Retiring orphaned HelmChart %s/%s (dropping finalizers, then deleting)...", cr.Namespace, cr.Name))
	if err := exec.RunStreamed(ch, wrapStepOutput, kubectlBin, "patch", "helmchart.helm.cattle.io", cr.Name, "-n", cr.Namespace,
		"--type=merge", "-p", `{"metadata":{"finalizers":null}}`); err != nil {
		return err
	}
	if err := exec.RunStreamed(ch, wrapStepOutput, kubectlBin, "delete", "helmchart.helm.cattle.io", cr.Name, "-n", cr.Namespace, "--ignore-not-found"); err != nil {
		return err
	}
	return exec.RunStreamed(ch, wrapStepOutput, kubectlBin, "delete", "job", "helm-install-"+cr.Name, "-n", cr.Namespace, "--ignore-not-found")
}

// deleteImmutableSelectorWorkloads deletes every Deployment/StatefulSet/
// DaemonSet in ns labeled
// app.kubernetes.io/instance=<standaloneAileronReleaseName> whose own
// spec.selector.matchLabels also pins that value - selectors are
// immutable, and this label flips to stabilizerHelmReleaseName under the
// merged release, so Helm can't patch it in place; those pods are deleted
// here and recreated by the merged install. Workloads without that
// selector label (e.g. an egress-bridge DaemonSet) are left running,
// patched in place by the subsequent stabilizer install.
func deleteImmutableSelectorWorkloads(ch chan<- installsteps.StepMsg, kubectlBin, ns string) error {
	out, err := exec.RunPrivileged(kubectlBin, "get", "deployments,statefulsets,daemonsets", "-n", ns,
		"-l", "app.kubernetes.io/instance="+standaloneAileronReleaseName, "-o", "json").CombinedOutput()
	if err != nil {
		return exec.WrapCmdErr(out, err)
	}
	var list struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Selector struct {
					MatchLabels map[string]string `json:"matchLabels"`
				} `json:"selector"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return fmt.Errorf("parsing workload list: %w", err)
	}
	for _, item := range list.Items {
		name := item.Metadata.Name
		kind := item.Kind
		if item.Spec.Selector.MatchLabels["app.kubernetes.io/instance"] == standaloneAileronReleaseName {
			ch <- installsteps.StepOutputMsg(fmt.Sprintf("%s/%s: selector instance %q -> %q (immutable) - deleting", kind, name, standaloneAileronReleaseName, stabilizerHelmReleaseName))
			if err := exec.RunStreamed(ch, wrapStepOutput, kubectlBin, "delete", "-n", ns, kind+"/"+name, "--ignore-not-found"); err != nil {
				return err
			}
		} else {
			ch <- installsteps.StepOutputMsg(fmt.Sprintf("%s/%s: selector unaffected - kept, patched in place", kind, name))
		}
	}
	return nil
}

// adoptAileronStep is the top-level ported orchestration, run as one of
// stabilizerSteps (stabilizer.go). Diverges from adopt-aileron.sh's
// hard-error stance in exactly one place: "no live aileron release AND no
// orphaned aileron HelmChart CR" is a normal no-op (fresh cluster, nothing
// to adopt) rather than an error, so applyStabilizerStep can proceed to a
// clean install. Every other precondition failure (release exists but its
// manifest lacks <release>-vncgateway; any non-NotFound kubectl error while
// filtering the live manifest) is a hard stepDoneMsg error, matching the
// script's abort-loudly stance. No interactive confirmation happens here -
// that already happened on the TUI's confirm screen.
func adoptAileronStep(cfg config.Config, ch chan<- installsteps.StepMsg) {
	const label = "Adopting standalone aileron release"
	const kubectlBin = "/usr/local/bin/kubectl"
	fail := func(err error) { ch <- installsteps.StepDoneMsg{Label: label, Err: err} }
	ns := StabilizerNamespace

	// Defense-in-depth: the wizard's entry point already refuses to start
	// unless aileron is installed and running (checkAileronReadyCmd), but
	// it's re-checked here in case aileron became unhealthy while the
	// operator was entering secrets - adopting against an unready aileron
	// would re-stamp ownership of resources that aren't healthy.
	// status.K3sServiceActive() first for the same reason as
	// checkAileronReadyCmd - see its doc comment (stabilizer.go).
	if !status.K3sServiceActive() || !status.AileronReady(kubectlBin) {
		fail(fmt.Errorf("aileron isn't installed and running - refusing to adopt"))
		return
	}

	manifest, ok, err := fetchHelmReleaseManifest(kubectlBin, ns, standaloneAileronReleaseName)
	if err != nil {
		fail(fmt.Errorf("reading %s release: %w", standaloneAileronReleaseName, err))
		return
	}
	if !ok {
		crs, err := findOrphanedHelmChartCRs(kubectlBin, standaloneAileronReleaseName, ns)
		if err != nil {
			fail(err)
			return
		}
		if len(crs) == 0 {
			ch <- installsteps.StepOutputMsg("No standalone aileron release found - nothing to adopt")
			ch <- installsteps.StepDoneMsg{Label: label}
			return
		}
		for _, cr := range crs {
			if err := retireHelmChartCR(ch, kubectlBin, cr); err != nil {
				fail(err)
				return
			}
		}
		ch <- installsteps.StepDoneMsg{Label: label}
		return
	}

	if !manifestHasVncGateway(manifest, standaloneAileronReleaseName) {
		fail(fmt.Errorf("%s release manifest has no %s-vncgateway resource - refusing to adopt an unexpected release shape", standaloneAileronReleaseName, standaloneAileronReleaseName))
		return
	}

	liveManifest, err := filterLiveManifest(ch, kubectlBin, splitYAMLDocuments(manifest))
	if err != nil {
		fail(err)
		return
	}

	if err := restampHelmOwnership(ch, kubectlBin, liveManifest, stabilizerHelmReleaseName, StabilizerNamespace); err != nil {
		fail(err)
		return
	}
	if err := deleteOldReleaseSecrets(ch, kubectlBin, ns, standaloneAileronReleaseName); err != nil {
		fail(err)
		return
	}

	crs, err := findOrphanedHelmChartCRs(kubectlBin, standaloneAileronReleaseName, ns)
	if err != nil {
		fail(err)
		return
	}
	for _, cr := range crs {
		if err := retireHelmChartCR(ch, kubectlBin, cr); err != nil {
			fail(err)
			return
		}
	}

	if err := deleteImmutableSelectorWorkloads(ch, kubectlBin, ns); err != nil {
		fail(err)
		return
	}
	ch <- installsteps.StepDoneMsg{Label: label}
}
