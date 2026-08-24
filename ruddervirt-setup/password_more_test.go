// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestSetAdminPassword(t *testing.T) {
	t.Run("pipes user:password on stdin to chpasswd", func(t *testing.T) {
		var gotStdin string
		r := &fakeRunner{}
		// setAdminPassword uses CombinedOutput() with Stdin set, which
		// routes through CombinedOutputWithStdin - capture it directly
		// rather than via fakeRunner's generic respond, since the stdin
		// content itself is what this test needs to see.
		orig := DefaultRunner
		DefaultRunner = stdinCapturingRunner{fakeRunner: r, capture: &gotStdin}
		defer func() { DefaultRunner = orig }()

		if err := setAdminPassword("hunter2"); err != nil {
			t.Fatalf("setAdminPassword() err = %v, want nil", err)
		}
		if gotStdin != "admin:hunter2\n" {
			t.Errorf("chpasswd stdin = %q, want %q", gotStdin, "admin:hunter2\n")
		}
		found := false
		for _, c := range r.calls {
			if strings.Contains(c, "chpasswd") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a chpasswd call, calls = %v", r.calls)
		}
	})

	t.Run("chpasswd failure is wrapped with its output", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte("chpasswd: some failure"), err: errFake}
		}}
		var err error
		withFakeRunner(r, func() { err = setAdminPassword("hunter2") })
		if err == nil {
			t.Fatal("setAdminPassword() err = nil, want an error")
		}
		if !strings.Contains(err.Error(), "some failure") {
			t.Errorf("setAdminPassword() err = %q, want it to mention the command's output", err.Error())
		}
	})
}

func TestRemoveCredentialsBanner(t *testing.T) {
	r := &fakeRunner{}
	var err error
	withFakeRunner(r, func() { err = removeCredentialsBanner() })
	if err != nil {
		t.Fatalf("removeCredentialsBanner() err = %v, want nil", err)
	}
	found := false
	for _, c := range r.calls {
		if cmdContains("", strings.Fields(c), "rm", "-f", credentialsBannerPath) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an rm -f call for the banner, calls = %v", r.calls)
	}
}

func TestAdminPasswordIsDefault(t *testing.T) {
	t.Run("matches the well-known default hash", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte("admin:" + defaultAdminPasswordHash + ":19000:0:99999:7:::\n")}
		}}
		var isDefault bool
		var err error
		withFakeRunner(r, func() { isDefault, err = adminPasswordIsDefault() })
		if err != nil {
			t.Fatalf("adminPasswordIsDefault() err = %v, want nil", err)
		}
		if !isDefault {
			t.Error("adminPasswordIsDefault() = false, want true")
		}
	})

	t.Run("changed password is not the default", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{out: []byte("admin:$6$somethingelse$abc:19000:0:99999:7:::\n")}
		}}
		var isDefault bool
		var err error
		withFakeRunner(r, func() { isDefault, err = adminPasswordIsDefault() })
		if err != nil {
			t.Fatalf("adminPasswordIsDefault() err = %v, want nil", err)
		}
		if isDefault {
			t.Error("adminPasswordIsDefault() = true, want false")
		}
	})

	t.Run("grep failure is an error", func(t *testing.T) {
		r := &fakeRunner{respond: func(name string, args []string) commandOutcome {
			return commandOutcome{err: errFake}
		}}
		var err error
		withFakeRunner(r, func() { _, err = adminPasswordIsDefault() })
		if err == nil {
			t.Fatal("adminPasswordIsDefault() err = nil, want an error")
		}
	})
}

// stdinCapturingRunner wraps a fakeRunner to additionally capture the
// content piped to CombinedOutputWithStdin, since fakeRunner's generic
// respond function only sees name/args, not stdin.
type stdinCapturingRunner struct {
	*fakeRunner
	capture *string
}

func (s stdinCapturingRunner) CombinedOutputWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, err
	}
	*s.capture = string(data)
	return s.fakeRunner.CombinedOutputWithStdin(ctx, stdin, name, args...)
}
