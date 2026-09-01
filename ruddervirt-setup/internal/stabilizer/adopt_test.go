// SPDX-License-Identifier: GPL-3.0-only

package stabilizer

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ruddervirt-setup/internal/config"
	"ruddervirt-setup/internal/exec/exectest"
	"ruddervirt-setup/internal/installsteps"
)

// encodeHelmReleaseSecretData reproduces Helm v3's own release-storage
// encoding (json -> gzip -> base64) for a fixture manifest - the exact
// input shape decodeHelmReleaseManifest expects once the Kubernetes API's
// own base64 layer has already been stripped (see k8sSecretGetJSON.Data).
func encodeHelmReleaseSecretData(t *testing.T, manifest string) []byte {
	t.Helper()
	j, err := json.Marshal(helmReleaseRecord{Manifest: manifest})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(j); err != nil {
		t.Fatalf("gzip fixture: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return []byte(base64.StdEncoding.EncodeToString(buf.Bytes()))
}

func TestDecodeHelmReleaseManifest(t *testing.T) {
	t.Run("round-trips a synthetic release record", func(t *testing.T) {
		want := "apiVersion: v1\nkind: Service\nmetadata:\n  name: aileron-vncgateway\n"
		got, err := decodeHelmReleaseManifest(encodeHelmReleaseSecretData(t, want))
		if err != nil {
			t.Fatalf("decodeHelmReleaseManifest err = %v, want nil", err)
		}
		if got != want {
			t.Errorf("decodeHelmReleaseManifest = %q, want %q", got, want)
		}
	})

	t.Run("invalid base64 errors", func(t *testing.T) {
		if _, err := decodeHelmReleaseManifest([]byte("not-base64!!!")); err == nil {
			t.Fatal("want an error for invalid base64")
		}
	})

	t.Run("valid base64 but not gzip errors", func(t *testing.T) {
		notGzip := []byte(base64.StdEncoding.EncodeToString([]byte("plain text, not gzip")))
		if _, err := decodeHelmReleaseManifest(notGzip); err == nil {
			t.Fatal("want an error for non-gzip content")
		}
	})
}

func TestManifestHasVncGateway(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		release  string
		want     bool
	}{
		{"present", "apiVersion: v1\nkind: Service\nmetadata:\n  name: aileron-vncgateway\n", "aileron", true},
		{"present with trailing content on the line", "  name: aileron-vncgateway  \n", "aileron", true},
		{"absent", "apiVersion: v1\nkind: Service\nmetadata:\n  name: aileron-manager\n", "aileron", false},
		{"different release prefix doesn't match", "  name: aileron-vncgateway\n", "stabilizer", false},
		{"substring isn't a match", "  name: aileron-vncgateway-extra\n", "aileron", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := manifestHasVncGateway(c.manifest, c.release); got != c.want {
				t.Errorf("manifestHasVncGateway(%q) = %v, want %v", c.manifest, got, c.want)
			}
		})
	}
}

func TestSplitYAMLDocuments(t *testing.T) {
	manifest := "---\n# Source: a\nkind: ConfigMap\nmetadata:\n  name: a\n---\n# Source: b\nkind: Service\nmetadata:\n  name: b\n---\n"
	docs := splitYAMLDocuments(manifest)
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2: %#v", len(docs), docs)
	}
	if !strings.Contains(docs[0], "name: a") || !strings.Contains(docs[1], "name: b") {
		t.Errorf("docs = %#v, want to contain name: a and name: b respectively", docs)
	}
}

func TestParseObjectRef(t *testing.T) {
	t.Run("parses a real object", func(t *testing.T) {
		doc := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: aileron-manager\n  namespace: ruddervirt-system\n"
		ref, err := parseObjectRef(doc)
		if err != nil {
			t.Fatalf("parseObjectRef err = %v, want nil", err)
		}
		want := k8sObjectRef{APIVersion: "apps/v1", Kind: "Deployment", Name: "aileron-manager", Namespace: "ruddervirt-system"}
		if ref != want {
			t.Errorf("parseObjectRef = %+v, want %+v", ref, want)
		}
	})

	t.Run("cluster-scoped object has empty namespace", func(t *testing.T) {
		doc := "apiVersion: k8s.cni.cncf.io/v1\nkind: NetworkAttachmentDefinition\nmetadata:\n  name: egress-external\n  namespace: kube-system\n"
		ref, err := parseObjectRef(doc)
		if err != nil {
			t.Fatalf("parseObjectRef err = %v", err)
		}
		if ref.Namespace != "kube-system" {
			t.Errorf("namespace = %q, want kube-system (cross-namespace object)", ref.Namespace)
		}
	})

	t.Run("blank/comment-only document errors", func(t *testing.T) {
		if _, err := parseObjectRef("# just a comment\n"); err == nil {
			t.Fatal("want an error for a document with no kind/name")
		}
	})
}

func TestFilterLiveManifest(t *testing.T) {
	docA := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: exists\n  namespace: ns\n"
	docB := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: absent\n  namespace: ns\n"

	t.Run("NotFound drops the doc, live objects are kept", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			line := strings.Join(args, " ")
			if strings.Contains(line, "absent") {
				return exectest.Outcome{Out: []byte(`Error from server (NotFound): configmaps "absent" not found`), Err: exectest.ErrFake}
			}
			return exectest.Outcome{Out: []byte("configmap/exists")}
		}}
		ch := make(chan installsteps.StepMsg, 100)
		var live string
		var err error
		exectest.WithFakeRunner(r, func() { live, err = filterLiveManifest(ch, "/usr/local/bin/kubectl", []string{docA, docB}) })
		if err != nil {
			t.Fatalf("filterLiveManifest err = %v, want nil", err)
		}
		if !strings.Contains(live, "name: exists") {
			t.Errorf("live manifest = %q, want it to contain the existing object", live)
		}
		if strings.Contains(live, "name: absent") {
			t.Errorf("live manifest = %q, want the absent object dropped", live)
		}
	})

	t.Run("a non-NotFound error aborts the whole run", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("Error from server: internal error"), Err: exectest.ErrFake}
		}}
		ch := make(chan installsteps.StepMsg, 100)
		var err error
		exectest.WithFakeRunner(r, func() { _, err = filterLiveManifest(ch, "/usr/local/bin/kubectl", []string{docA}) })
		if err == nil {
			t.Fatal("want an error for a non-NotFound failure")
		}
	})
}

func TestRestampHelmOwnership(t *testing.T) {
	live := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n  namespace: ns\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: b\n  namespace: kube-system\n"
	var calls []string
	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		calls = append(calls, strings.Join(append([]string{name}, args...), " "))
		return exectest.Outcome{}
	}}
	ch := make(chan installsteps.StepMsg, 100)
	var err error
	exectest.WithFakeRunner(r, func() {
		err = restampHelmOwnership(ch, "/usr/local/bin/kubectl", live, "stabilizer", "ruddervirt-system")
	})
	if err != nil {
		t.Fatalf("restampHelmOwnership err = %v, want nil", err)
	}
	if len(calls) != 2 {
		t.Fatalf("got %d kubectl calls, want 2 (annotate, label): %#v", len(calls), calls)
	}
	for _, c := range calls {
		if strings.Contains(c, " -n ") || strings.Contains(c, "--namespace") {
			t.Errorf("call %q must never pass -n/--namespace - the manifest carries each object's own namespace", c)
		}
	}
	if !strings.Contains(calls[0], "annotate") || !strings.Contains(calls[0], "meta.helm.sh/release-name=stabilizer") {
		t.Errorf("first call = %q, want an annotate with the new release name", calls[0])
	}
	if !strings.Contains(calls[1], "label") || !strings.Contains(calls[1], "app.kubernetes.io/managed-by=Helm") {
		t.Errorf("second call = %q, want a label call", calls[1])
	}
}

func TestFindOrphanedHelmChartCRs(t *testing.T) {
	listJSON := `{"items":[
		{"metadata":{"name":"aileron","namespace":"kube-system","labels":{}},"spec":{"targetNamespace":"ruddervirt-system"}},
		{"metadata":{"name":"aileron","namespace":"kube-system","labels":{"objectset.rio.cattle.io/owner-gvk":"k3s.cattle.io/v1, Kind=Addon"}},"spec":{"targetNamespace":"other-ns"}},
		{"metadata":{"name":"unrelated","namespace":"kube-system","labels":{}},"spec":{"targetNamespace":"ruddervirt-system"}}
	]}`
	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		return exectest.Outcome{Out: []byte(listJSON)}
	}}
	var crs []helmChartCR
	var err error
	exectest.WithFakeRunner(r, func() { crs, err = findOrphanedHelmChartCRs("/usr/local/bin/kubectl", "aileron", "ruddervirt-system") })
	if err != nil {
		t.Fatalf("findOrphanedHelmChartCRs err = %v, want nil", err)
	}
	if len(crs) != 1 {
		t.Fatalf("got %d CRs, want exactly 1 (matching name AND targetNamespace): %#v", len(crs), crs)
	}
	if crs[0].TargetNamespace != "ruddervirt-system" {
		t.Errorf("TargetNamespace = %q, want ruddervirt-system", crs[0].TargetNamespace)
	}
}

func TestRetireHelmChartCR(t *testing.T) {
	t.Run("drops finalizers before deleting, in order, then deletes the job", func(t *testing.T) {
		var calls []string
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			calls = append(calls, strings.Join(args, " "))
			return exectest.Outcome{}
		}}
		ch := make(chan installsteps.StepMsg, 100)
		cr := helmChartCR{Name: "aileron", Namespace: "kube-system"}
		var err error
		exectest.WithFakeRunner(r, func() { err = retireHelmChartCR(ch, "/usr/local/bin/kubectl", cr) })
		if err != nil {
			t.Fatalf("retireHelmChartCR err = %v, want nil", err)
		}
		if len(calls) != 3 {
			t.Fatalf("got %d calls, want 3 (patch, delete helmchart, delete job): %#v", len(calls), calls)
		}
		if !strings.Contains(calls[0], "patch") || !strings.Contains(calls[0], "finalizers") {
			t.Errorf("call[0] = %q, want the finalizer patch FIRST", calls[0])
		}
		if !strings.Contains(calls[1], "delete") || !strings.Contains(calls[1], "helmchart") {
			t.Errorf("call[1] = %q, want delete helmchart SECOND", calls[1])
		}
		if !strings.Contains(calls[2], "delete") || !strings.Contains(calls[2], "job") || !strings.Contains(calls[2], "helm-install-aileron") {
			t.Errorf("call[2] = %q, want delete job helm-install-aileron THIRD", calls[2])
		}
	})

	t.Run("Addon-owned CR is left alone with instructions instead of deleted", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			t.Errorf("unexpected kubectl call for an Addon-owned CR: %s %v", name, args)
			return exectest.Outcome{}
		}}
		ch := make(chan installsteps.StepMsg, 100)
		cr := helmChartCR{Name: "aileron", Namespace: "kube-system", OwnerGVK: "k3s.cattle.io/v1, Kind=Addon"}
		var err error
		exectest.WithFakeRunner(r, func() { err = retireHelmChartCR(ch, "/usr/local/bin/kubectl", cr) })
		if err != nil {
			t.Fatalf("retireHelmChartCR err = %v, want nil", err)
		}
		lines := strings.Join(drainStrings(ch), "\n")
		if !strings.Contains(lines, "manifests") {
			t.Errorf("output = %q, want instructions to remove the server manifest file", lines)
		}
	})
}

func TestDeleteImmutableSelectorWorkloads(t *testing.T) {
	listJSON := `{"items":[
		{"kind":"Deployment","metadata":{"name":"aileron-manager"},"spec":{"selector":{"matchLabels":{"app.kubernetes.io/instance":"aileron"}}}},
		{"kind":"DaemonSet","metadata":{"name":"egress-bridge"},"spec":{"selector":{"matchLabels":{"app":"egress-bridge"}}}}
	]}`
	var deleted []string
	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		if exectest.CmdContains(name, args, "get", "deployments,statefulsets,daemonsets") {
			return exectest.Outcome{Out: []byte(listJSON)}
		}
		if exectest.CmdContains(name, args, "delete") {
			deleted = append(deleted, strings.Join(args, " "))
		}
		return exectest.Outcome{}
	}}
	ch := make(chan installsteps.StepMsg, 100)
	var err error
	exectest.WithFakeRunner(r, func() { err = deleteImmutableSelectorWorkloads(ch, "/usr/local/bin/kubectl", "ruddervirt-system") })
	if err != nil {
		t.Fatalf("deleteImmutableSelectorWorkloads err = %v, want nil", err)
	}
	if len(deleted) != 1 || !strings.Contains(deleted[0], "Deployment/aileron-manager") {
		t.Fatalf("deleted = %#v, want exactly Deployment/aileron-manager deleted", deleted)
	}
}

func TestAdoptAileronStep(t *testing.T) {
	const ns = StabilizerNamespace

	emptySecretList := `{"items":[]}`
	emptyHelmChartList := `{"items":[]}`

	t.Run("no release and no orphaned CR is a clean no-op", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case exectest.CmdContains(name, args, "systemctl", "is-active", "k3s.service"):
				return exectest.Outcome{} // k3s is running
			case exectest.CmdContains(name, args, "wait", "deployment.apps/aileron"):
				return exectest.Outcome{} // aileron is ready
			case exectest.CmdContains(name, args, "get", "secret"):
				return exectest.Outcome{Out: []byte(emptySecretList)}
			case exectest.CmdContains(name, args, "get", "helmchart"):
				return exectest.Outcome{Out: []byte(emptyHelmChartList)}
			}
			t.Errorf("unexpected call: %s %v", name, args)
			return exectest.Outcome{}
		}}
		ch := make(chan installsteps.StepMsg, 100)
		exectest.WithFakeRunner(r, func() { adoptAileronStep(config.Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.Err != nil {
			t.Fatalf("adoptAileronStep err = %v, want nil (no-op)", done.Err)
		}
	})

	t.Run("manifest missing vncgateway is a hard error", func(t *testing.T) {
		manifest := "apiVersion: v1\nkind: Deployment\nmetadata:\n  name: aileron-manager\n  namespace: " + ns + "\n"
		secretData := encodeHelmReleaseSecretData(t, manifest)
		secretList := fmt.Sprintf(`{"items":[{"metadata":{"name":"aileron","namespace":%q,"labels":{}},"data":{"release":%q}}]}`, ns, base64.StdEncoding.EncodeToString(secretData))
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case exectest.CmdContains(name, args, "systemctl", "is-active", "k3s.service"):
				return exectest.Outcome{} // k3s is running
			case exectest.CmdContains(name, args, "wait", "deployment.apps/aileron"):
				return exectest.Outcome{} // aileron is ready
			case exectest.CmdContains(name, args, "get", "secret"):
				return exectest.Outcome{Out: []byte(secretList)}
			}
			t.Errorf("unexpected call: %s %v", name, args)
			return exectest.Outcome{}
		}}
		ch := make(chan installsteps.StepMsg, 100)
		exectest.WithFakeRunner(r, func() { adoptAileronStep(config.Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.Err == nil {
			t.Fatal("adoptAileronStep err = nil, want an error (manifest lacks vncgateway)")
		}
	})

	t.Run("full happy-path adoption runs the whole sequence", func(t *testing.T) {
		manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: aileron-vncgateway\n  namespace: " + ns + "\n"
		secretData := encodeHelmReleaseSecretData(t, manifest)
		secretList := fmt.Sprintf(`{"items":[{"metadata":{"name":"aileron","namespace":%q,"labels":{}},"data":{"release":%q}}]}`, ns, base64.StdEncoding.EncodeToString(secretData))
		var sawAnnotate, sawDeleteSecrets, sawDeleteWorkloads bool
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			switch {
			case exectest.CmdContains(name, args, "systemctl", "is-active", "k3s.service"):
				return exectest.Outcome{} // k3s is running
			case exectest.CmdContains(name, args, "wait", "deployment.apps/aileron"):
				return exectest.Outcome{} // aileron is ready
			case exectest.CmdContains(name, args, "get", "secret", "status=deployed"):
				return exectest.Outcome{Out: []byte(secretList)}
			case exectest.CmdContains(name, args, "get", "Deployment", "aileron-vncgateway"):
				return exectest.Outcome{Out: []byte("deployment.apps/aileron-vncgateway")}
			case exectest.CmdContains(name, args, "annotate"):
				sawAnnotate = true
				return exectest.Outcome{}
			case exectest.CmdContains(name, args, "delete", "secret"):
				sawDeleteSecrets = true
				return exectest.Outcome{}
			case exectest.CmdContains(name, args, "get", "helmchart"):
				return exectest.Outcome{Out: []byte(emptyHelmChartList)}
			case exectest.CmdContains(name, args, "get", "deployments,statefulsets,daemonsets"):
				sawDeleteWorkloads = true
				return exectest.Outcome{Out: []byte(`{"items":[]}`)}
			case exectest.CmdContains(name, args, "label"):
				return exectest.Outcome{}
			}
			t.Errorf("unexpected call: %s %v", name, args)
			return exectest.Outcome{}
		}}
		ch := make(chan installsteps.StepMsg, 100)
		exectest.WithFakeRunner(r, func() { adoptAileronStep(config.Config{}, ch) })
		done := lastStepDone(t, ch)
		if done.Err != nil {
			t.Fatalf("adoptAileronStep err = %v, want nil", done.Err)
		}
		if !sawAnnotate || !sawDeleteSecrets || !sawDeleteWorkloads {
			t.Errorf("full sequence not observed: annotate=%v deleteSecrets=%v deleteWorkloads=%v", sawAnnotate, sawDeleteSecrets, sawDeleteWorkloads)
		}
	})
}

// drainStrings collects every installsteps.StepOutputMsg already buffered
// on ch without blocking the caller (ch stays open). Mirrors package
// main's k3s_bridge_test.go drainStrings, kept as an independent copy since
// this package's tests can't import package main.
func drainStrings(ch chan installsteps.StepMsg) []string {
	var out []string
	for {
		select {
		case msg := <-ch:
			if s, ok := msg.(installsteps.StepOutputMsg); ok {
				out = append(out, string(s))
			}
		default:
			return out
		}
	}
}

// lastStepDone drains ch and returns the final installsteps.StepDoneMsg,
// failing the test if none arrived.
func lastStepDone(t *testing.T, ch chan installsteps.StepMsg) installsteps.StepDoneMsg {
	t.Helper()
	var last *installsteps.StepDoneMsg
	for {
		select {
		case msg := <-ch:
			if d, ok := msg.(installsteps.StepDoneMsg); ok {
				m := d
				last = &m
			}
		default:
			if last == nil {
				t.Fatal("no installsteps.StepDoneMsg was sent")
			}
			return *last
		}
	}
}
