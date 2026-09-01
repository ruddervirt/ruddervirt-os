// SPDX-License-Identifier: GPL-3.0-only

package hostname

import (
	"strings"
	"testing"

	"ruddervirt-setup/internal/exec/exectest"
)

func TestParseHostname(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "simple label", in: "ruddervirt", want: "ruddervirt"},
		{name: "trims whitespace", in: "  ruddervirt  ", want: "ruddervirt"},
		{name: "dotted labels", in: "node1.cluster.local", want: "node1.cluster.local"},
		{name: "digits and hyphens", in: "node-01", want: "node-01"},
		{name: "empty", in: "", wantErr: true},
		{name: "only whitespace", in: "   ", wantErr: true},
		{name: "leading hyphen", in: "-node", wantErr: true},
		{name: "trailing hyphen", in: "node-", wantErr: true},
		{name: "empty label", in: "node..local", wantErr: true},
		{name: "invalid character", in: "node_01", wantErr: true},
		{name: "too long", in: strings.Repeat("a", 254), wantErr: true},
		{
			name: "exactly 253 chars across multiple labels ok",
			in:   strings.Repeat("a", 63) + "." + strings.Repeat("a", 63) + "." + strings.Repeat("a", 63) + "." + strings.Repeat("a", 61),
			want: strings.Repeat("a", 63) + "." + strings.Repeat("a", 63) + "." + strings.Repeat("a", 63) + "." + strings.Repeat("a", 61),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHostname(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseHostname(%q) err = nil, want an error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseHostname(%q) err = %v, want nil", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseHostname(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSetHostname(t *testing.T) {
	t.Run("hostnamectl success needs no fallback", func(t *testing.T) {
		r := &exectest.FakeRunner{}
		var err error
		exectest.WithFakeRunner(r, func() { err = SetHostname("new-host") })
		if err != nil {
			t.Fatalf("SetHostname err = %v, want nil", err)
		}
		if len(r.Calls) != 1 || !strings.Contains(r.Calls[0], "hostnamectl set-hostname new-host") {
			t.Errorf("Calls = %v, want a single hostnamectl set-hostname call", r.Calls)
		}
	})

	t.Run("hostnamectl failure falls back to tee plus best-effort hostname", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "hostnamectl") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		var err error
		exectest.WithFakeRunner(r, func() { err = SetHostname("fallback-host") })
		if err != nil {
			t.Fatalf("SetHostname err = %v, want nil (tee fallback should succeed)", err)
		}
		var sawTee, sawHostname bool
		for _, c := range r.Calls {
			fields := strings.Fields(c)
			if len(fields) < 2 {
				continue
			}
			// fields[1] is the wrapped command's path (fields[0] is "sudo");
			// exact equality since "/usr/bin/hostname" is a substring of
			// "/usr/bin/hostnamectl" and CmdContains/HasField would conflate
			// the two.
			switch fields[1] {
			case "/usr/bin/tee":
				sawTee = true
			case "/usr/bin/hostname":
				sawHostname = true
			}
		}
		if !sawTee {
			t.Errorf("Calls = %v, want a tee /etc/hostname call after hostnamectl fails", r.Calls)
		}
		if !sawHostname {
			t.Errorf("Calls = %v, want a best-effort classic hostname call", r.Calls)
		}
	})

	t.Run("tee failure is reported, best-effort hostname call is not required", func(t *testing.T) {
		r := &exectest.FakeRunner{Respond: func(name string, args []string) exectest.Outcome {
			if exectest.CmdContains(name, args, "hostnamectl") || exectest.CmdContains(name, args, "tee") {
				return exectest.Outcome{Err: exectest.ErrFake}
			}
			return exectest.Outcome{}
		}}
		var err error
		exectest.WithFakeRunner(r, func() { err = SetHostname("bad-host") })
		if err == nil {
			t.Fatal("SetHostname err = nil, want an error when both hostnamectl and tee fail")
		}
	})
}
