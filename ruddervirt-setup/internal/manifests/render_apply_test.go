// SPDX-License-Identifier: GPL-3.0-only

package manifests

import (
	"errors"
	"os"
	"strings"
	"testing"

	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/exec/exectest"
)

// TestRenderSubstituteApplySuccess exercises the substitute/tempfile/apply
// half of the shape shared by applyKubeOvn/ApplyAileron/applyStabilizer/
// ApplyMultus, using kube-ovn.yaml's actual placeholder set (5
// substitutions) as a realistic stand-in for all four.
func TestRenderSubstituteApplySuccess(t *testing.T) {
	const kubectlBin = "/usr/local/bin/kubectl"
	const template = `apiVersion: helm.cattle.io/v1
kind: HelmChart
spec:
  version: __KUBE_OVN_VERSION__
  valuesContent: |
    podCIDR: __POD_CIDR__
    podGateway: __POD_GATEWAY__
    svcCIDR: __SVC_CIDR__
    masterNodes: [__MASTER_NODES__]
`
	var appliedContent string

	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		if !exectest.CmdContains(name, args, kubectlBin, "apply", "-f") {
			t.Errorf("unexpected command: %s %v", name, args)
			return exectest.Outcome{}
		}
		// Tempfile path is the last arg - read it now since renderSubstituteApply
		// removes it once this call returns.
		tmpPath := args[len(args)-1]
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			t.Fatalf("reading rendered tempfile %q: %v", tmpPath, err)
		}
		appliedContent = string(data)
		return exectest.Outcome{}
	}}

	ch := make(chan exec.StepMsg, 100)
	var err error
	exectest.WithFakeRunner(r, func() {
		err = renderSubstituteApply(ch, testWrap, kubectlBin, "kube-ovn-test", []byte(template), []Placeholder{
			{Token: "__KUBE_OVN_VERSION__", Value: "v1.13.0"},
			{Token: "__POD_CIDR__", Value: "10.16.0.0/16"},
			{Token: "__POD_GATEWAY__", Value: "10.16.0.1"},
			{Token: "__SVC_CIDR__", Value: "10.96.0.0/12"},
			{Token: "__MASTER_NODES__", Value: `"192.168.1.10"`},
		})
	})
	if err != nil {
		t.Fatalf("renderSubstituteApply err = %v, want nil", err)
	}
	if appliedContent == "" {
		t.Fatal("kubectl apply was never invoked with the rendered tempfile")
	}
	for _, want := range []string{"v1.13.0", "10.16.0.0/16", "10.16.0.1", "10.96.0.0/12", `"192.168.1.10"`} {
		if !strings.Contains(appliedContent, want) {
			t.Errorf("rendered manifest missing substituted value %q; content:\n%s", want, appliedContent)
		}
	}
	for _, placeholder := range []string{"__KUBE_OVN_VERSION__", "__POD_CIDR__", "__POD_GATEWAY__", "__SVC_CIDR__", "__MASTER_NODES__"} {
		if strings.Contains(appliedContent, placeholder) {
			t.Errorf("rendered manifest still contains unsubstituted placeholder %q", placeholder)
		}
	}
}

// TestRenderSubstituteApplyNoPlaceholders confirms an empty/nil placeholder
// list renders the template unmodified without panicking.
func TestRenderSubstituteApplyNoPlaceholders(t *testing.T) {
	const kubectlBin = "/usr/local/bin/kubectl"
	const template = "spec:\n  version: __MULTUS_VERSION__\n"
	var appliedContent string

	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		tmpPath := args[len(args)-1]
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			t.Fatalf("reading rendered tempfile %q: %v", tmpPath, err)
		}
		appliedContent = string(data)
		return exectest.Outcome{}
	}}

	ch := make(chan exec.StepMsg, 100)
	var err error
	exectest.WithFakeRunner(r, func() {
		err = renderSubstituteApply(ch, testWrap, kubectlBin, "multus-test", []byte(template), nil)
	})
	if err != nil {
		t.Fatalf("renderSubstituteApply err = %v, want nil", err)
	}
	if appliedContent != template {
		t.Errorf("rendered content = %q, want unmodified template %q", appliedContent, template)
	}
}

// TestRenderSubstituteApplyKubectlFailure confirms a kubectl apply failure
// propagates as renderSubstituteApply's own error.
func TestRenderSubstituteApplyKubectlFailure(t *testing.T) {
	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		return exectest.Outcome{Err: exectest.ErrFake}
	}}
	ch := make(chan exec.StepMsg, 100)
	var err error
	exectest.WithFakeRunner(r, func() {
		err = renderSubstituteApply(ch, testWrap, "/usr/local/bin/kubectl", "multus-test", []byte("spec:\n  version: __MULTUS_VERSION__\n"), []Placeholder{
			{Token: "__MULTUS_VERSION__", Value: "v4.2.2"},
		})
	})
	if !errors.Is(err, exectest.ErrFake) {
		t.Errorf("renderSubstituteApply err = %v, want %v", err, exectest.ErrFake)
	}
}

// TestRenderAndApplyUnknownManifest confirms a missing embedded manifest
// fails before ever invoking kubectl, preserving WriteManifestFile's own
// guarantee since RenderAndApply calls it first.
func TestRenderAndApplyUnknownManifest(t *testing.T) {
	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		t.Errorf("unexpected command for an unknown manifest file: %s %v", name, args)
		return exectest.Outcome{}
	}}
	ch := make(chan exec.StepMsg, 100)
	var err error
	exectest.WithFakeRunner(r, func() {
		err = RenderAndApply(ch, testWrap, testWritePrivileged, "/usr/local/bin/kubectl", "made-up.yaml", "made-up", nil)
	})
	if err == nil {
		t.Fatal("RenderAndApply(made-up.yaml) err = nil, want an error")
	}
}
