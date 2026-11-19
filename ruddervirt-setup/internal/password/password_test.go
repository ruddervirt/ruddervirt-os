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

func TestParseShadowHash(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			"default admin entry",
			"admin:$6$ruddervirt0$hVyBGZxUjDrV7kW.cEWoJxBLIWzCDxPDRjE3NgDEmeAMoNEpXoJa2fR6PGEKu5dnI78I22zK5z1cC.IEDOnAZ/:19700:0:99999:7:::\n",
			"$6$ruddervirt0$hVyBGZxUjDrV7kW.cEWoJxBLIWzCDxPDRjE3NgDEmeAMoNEpXoJa2fR6PGEKu5dnI78I22zK5z1cC.IEDOnAZ/",
			true,
		},
		{"changed password", "admin:$6$othersalt$abcdef:19700:0:99999:7:::", "$6$othersalt$abcdef", true},
		{"locked account", "admin:!:19700:0:99999:7:::", "!", true},
		// `passwd -d admin` leaves this - a real entry meaning "no password",
		// which CheckAdminPassword has to see rather than choke on.
		{"empty hash field", "admin::19700:0:99999:7:::", "", true},
		{"no colon", "admin", "", false},
		{"empty line", "", "", false},
	}
	for _, c := range cases {
		got, err := parseShadowHash(c.in)
		if (err == nil) != c.ok {
			t.Errorf("%s: parseShadowHash(%q) err = %v, want ok = %v", c.name, c.in, err, c.ok)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("%s: parseShadowHash(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

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

func TestCheckAdminPassword(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		want  AdminPasswordState
	}{
		{"well-known default hash", "admin:" + defaultAdminPasswordHash + ":19000:0:99999:7:::\n", AdminPasswordDefault},
		{"operator's own password", "admin:$6$somethingelse$abc:19000:0:99999:7:::\n", AdminPasswordOK},
		// Locked admin (SSH-key-only node) is not something to force a change
		// on - there's no password for an attacker to guess.
		{"locked account", "admin:!:19000:0:99999:7:::\n", AdminPasswordOK},
		{"starred account", "admin:*:19000:0:99999:7:::\n", AdminPasswordOK},
		// Regression: this used to come back as "unexpected shadow entry" and
		// hard-block the setup flow instead of prompting for a new password.
		{"no password at all", "admin::20775:0:99999:7:::\n", AdminPasswordUnset},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
				return exectest.Outcome{Out: []byte(c.entry)}
			}}
			var got AdminPasswordState
			var err error
			exectest.WithFakeRunner(r, func() { got, err = CheckAdminPassword() })
			if err != nil {
				t.Fatalf("CheckAdminPassword() err = %v, want nil", err)
			}
			if got != c.want {
				t.Errorf("CheckAdminPassword() = %v, want %v", got, c.want)
			}
			if got.NeedsChange() != (c.want != AdminPasswordOK) {
				t.Errorf("NeedsChange() = %v, want %v", got.NeedsChange(), c.want != AdminPasswordOK)
			}
		})
	}

	t.Run("grep failure is an error", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		var err error
		exectest.WithFakeRunner(r, func() { _, err = CheckAdminPassword() })
		if err == nil {
			t.Fatal("CheckAdminPassword() err = nil, want an error")
		}
	})
}

func TestRootIsLocked(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		want  bool
	}{
		{"passwd -l lock", "root:!:19000:0:99999:7:::\n", true},
		{"passwd -l over a hash", "root:!$6$abc$def:19000:0:99999:7:::\n", true},
		// Fedora CoreOS's own default for an account never given a password.
		{"never set", "root:*:19000:0:99999:7:::\n", true},
		// What images before this change baked in: root shared admin's
		// well-known default, so root was directly loginable.
		{"well-known default hash", "root:" + defaultAdminPasswordHash + ":19000:0:99999:7:::\n", false},
		{"some other hash", "root:$6$somethingelse$abc:19000:0:99999:7:::\n", false},
		// Empty field means "no password needed", the opposite of locked.
		{"no password at all", "root::19000:0:99999:7:::\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
				return exectest.Outcome{Out: []byte(c.entry)}
			}}
			var got bool
			var err error
			exectest.WithFakeRunner(r, func() { got, err = RootIsLocked() })
			if err != nil {
				t.Fatalf("RootIsLocked() err = %v, want nil", err)
			}
			if got != c.want {
				t.Errorf("RootIsLocked() = %v, want %v", got, c.want)
			}
		})
	}

	t.Run("grep failure is an error", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Err: exectest.ErrFake}
		}}
		var err error
		exectest.WithFakeRunner(r, func() { _, err = RootIsLocked() })
		if err == nil {
			t.Fatal("RootIsLocked() err = nil, want an error")
		}
	})
}

func TestLockRootAccount(t *testing.T) {
	t.Run("locks root via passwd -l", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		var err error
		exectest.WithFakeRunner(r, func() { err = LockRootAccount() })
		if err != nil {
			t.Fatalf("LockRootAccount() err = %v, want nil", err)
		}
		found := false
		for _, c := range r.Calls {
			if exectest.CmdContains("", strings.Fields(c), "passwd", "-l", rootUsername) {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a passwd -l root call, calls = %v", r.Calls)
		}
	})

	t.Run("failure is wrapped with its output", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			return exectest.Outcome{Out: []byte("passwd: Permission denied"), Err: exectest.ErrFake}
		}}
		var err error
		exectest.WithFakeRunner(r, func() { err = LockRootAccount() })
		if err == nil {
			t.Fatal("LockRootAccount() err = nil, want an error")
		}
		if !strings.Contains(err.Error(), "Permission denied") {
			t.Errorf("LockRootAccount() err = %q, want it to mention the command's output", err.Error())
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

func TestAdminCanLogIn(t *testing.T) {
	cases := []struct {
		name    string
		entry   string
		keysErr error
		want    bool
	}{
		{"own password, no keys", "admin:$6$somethingelse$abc:19000:0:99999:7:::\n", exectest.ErrFake, true},
		{"default password, no keys", "admin:" + defaultAdminPasswordHash + ":19000:0:99999:7:::\n", exectest.ErrFake, true},
		// passwd -l admin: no password works, so a key is the only way in.
		{"locked, no keys", "admin:!:19000:0:99999:7:::\n", exectest.ErrFake, false},
		{"locked, with a key", "admin:!:19000:0:99999:7:::\n", nil, true},
		{"starred, no keys", "admin:*:19000:0:99999:7:::\n", exectest.ErrFake, false},
		// passwd -d admin: passwordless login still gets you in, but it is
		// not a credential - without a key there is nothing to fall back on
		// once root is locked, which is what the caller cares about.
		{"no password at all, no keys", "admin::19000:0:99999:7:::\n", exectest.ErrFake, false},
		{"no password at all, with a key", "admin::19000:0:99999:7:::\n", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
				line := strings.Join(args, " ")
				switch {
				case strings.Contains(line, "authorized_keys"):
					return exectest.Outcome{Err: c.keysErr}
				case strings.Contains(line, "getent"):
					return exectest.Outcome{Out: []byte("admin:x:1000:1000::/var/home/admin:/bin/bash\n")}
				}
				return exectest.Outcome{Out: []byte(c.entry)}
			}}
			var got bool
			var err error
			exectest.WithFakeRunner(r, func() { got, err = AdminCanLogIn() })
			if err != nil {
				t.Fatalf("AdminCanLogIn() err = %v, want nil", err)
			}
			if got != c.want {
				t.Errorf("AdminCanLogIn() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestAdminHomeDirFallsBackWhenGetentFails pins the fallback, since getting
// the home directory wrong would silently mean "no keys found" and block a
// root lock that should have gone ahead.
func TestAdminHomeDirFallsBackWhenGetentFails(t *testing.T) {
	r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		return exectest.Outcome{Err: exectest.ErrFake}
	}}
	var got string
	exectest.WithFakeRunner(r, func() { got = adminHomeDir() })
	if got != "/var/home/admin" {
		t.Errorf("adminHomeDir() = %q, want the /var/home/admin fallback", got)
	}

	r = &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
		return exectest.Outcome{Out: []byte("admin:x:1000:1000::/home/elsewhere:/bin/bash\n")}
	}}
	exectest.WithFakeRunner(r, func() { got = adminHomeDir() })
	if got != "/home/elsewhere" {
		t.Errorf("adminHomeDir() = %q, want the home field from getent", got)
	}
}
