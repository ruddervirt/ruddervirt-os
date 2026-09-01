// SPDX-License-Identifier: GPL-3.0-only

package password

import (
	"context"
	"io"
	"strings"
	"testing"

	"ruddervirt-setup/internal/exec"
	"ruddervirt-setup/internal/exec/exectest"
)

func TestSetAdminPassword(t *testing.T) {
	t.Run("pipes user:password on stdin to chpasswd", func(t *testing.T) {
		var gotStdin string
		r := &exectest.FakeRunner{}
		// SetAdminPassword's CombinedOutput() with Stdin set routes through
		// CombinedOutputWithStdin, so capture stdin directly rather than via
		// FakeRunner's generic Respond.
		orig := exec.DefaultRunner
		exec.DefaultRunner = stdinCapturingRunner{FakeRunner: r, capture: &gotStdin}
		defer func() { exec.DefaultRunner = orig }()

		if err := SetAdminPassword("hunter2"); err != nil {
			t.Fatalf("SetAdminPassword() err = %v, want nil", err)
		}
		if gotStdin != "admin:hunter2\n" {
			t.Errorf("chpasswd stdin = %q, want %q", gotStdin, "admin:hunter2\n")
		}
		found := false
		for _, c := range r.Calls {
			if strings.Contains(c, "chpasswd") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a chpasswd call, calls = %v", r.Calls)
		}
	})

	t.Run("chpasswd failure is wrapped with its output", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("chpasswd: some failure"), Err: exectest.ErrFake}
		}}
		var err error
		exectest.WithFakeRunner(r, func() { err = SetAdminPassword("hunter2") })
		if err == nil {
			t.Fatal("SetAdminPassword() err = nil, want an error")
		}
		if !strings.Contains(err.Error(), "some failure") {
			t.Errorf("SetAdminPassword() err = %q, want it to mention the command's output", err.Error())
		}
	})
}

func TestRemoveCredentialsBanner(t *testing.T) {
	r := &exectest.FakeRunner{}
	var err error
	exectest.WithFakeRunner(r, func() { err = RemoveCredentialsBanner() })
	if err != nil {
		t.Fatalf("RemoveCredentialsBanner() err = %v, want nil", err)
	}
	found := false
	for _, c := range r.Calls {
		if exectest.CmdContains("", strings.Fields(c), "rm", "-f", credentialsBannerPath) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an rm -f call for the banner, calls = %v", r.Calls)
	}
}

func TestAdminPasswordIsDefault(t *testing.T) {
	t.Run("matches the well-known default hash", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("admin:" + defaultAdminPasswordHash + ":19000:0:99999:7:::\n")}
		}}
		var isDefault bool
		var err error
		exectest.WithFakeRunner(r, func() { isDefault, err = AdminPasswordIsDefault() })
		if err != nil {
			t.Fatalf("AdminPasswordIsDefault() err = %v, want nil", err)
		}
		if !isDefault {
			t.Error("AdminPasswordIsDefault() = false, want true")
		}
	})

	t.Run("changed password is not the default", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("admin:$6$somethingelse$abc:19000:0:99999:7:::\n")}
		}}
		var isDefault bool
		var err error
		exectest.WithFakeRunner(r, func() { isDefault, err = AdminPasswordIsDefault() })
		if err != nil {
			t.Fatalf("AdminPasswordIsDefault() err = %v, want nil", err)
		}
		if isDefault {
			t.Error("AdminPasswordIsDefault() = true, want false")
		}
	})

	t.Run("grep failure is an error", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		var err error
		exectest.WithFakeRunner(r, func() { _, err = AdminPasswordIsDefault() })
		if err == nil {
			t.Fatal("AdminPasswordIsDefault() err = nil, want an error")
		}
	})
}

// stdinCapturingRunner wraps a FakeRunner to also capture the content piped
// to CombinedOutputWithStdin, which Respond (name/args only) can't see.
type stdinCapturingRunner struct {
	*exectest.FakeRunner
	capture *string
}

func (s stdinCapturingRunner) CombinedOutputWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, err
	}
	*s.capture = string(data)
	return s.FakeRunner.CombinedOutputWithStdin(ctx, stdin, name, args...)
}
