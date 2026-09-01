// SPDX-License-Identifier: GPL-3.0-only

package stabilizer

import (
	"encoding/json"
	"strings"
	"testing"

	"ruddervirt-setup/internal/stabilizer/settings"
)

func TestValidateStabilizerTargetVersion(t *testing.T) {
	cases := []struct {
		name    string
		version string
		wantErr bool
		wantMsg string // substring, only checked if non-empty
	}{
		{"plain semver", "1.2.3", false, ""},
		{"zero version", "0.0.0", false, ""},
		{"v-prefixed is rejected with a specific message", "v1.2.3", true, "plain semver"},
		{"prerelease suffix rejected", "1.2.3-rc1", true, "invalid chart version"},
		{"build metadata rejected", "1.2.3+build5", true, "invalid chart version"},
		{"leading zero rejected", "1.02.3", true, "invalid chart version"},
		{"too long rejected", strings.Repeat("1", 33), true, "never exceed"},
		{"empty rejected", "", true, "invalid chart version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateStabilizerTargetVersion(c.version)
			if c.wantErr && err == nil {
				t.Fatalf("err = nil, want an error for %q", c.version)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("err = %v, want nil for %q", err, c.version)
			}
			if c.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), c.wantMsg)) {
				t.Errorf("err = %v, want it to contain %q", err, c.wantMsg)
			}
		})
	}
}

func TestCheckStabilizerVersionTransition(t *testing.T) {
	t.Run("same-major upgrade always allowed", func(t *testing.T) {
		if err := checkStabilizerVersionTransition("1.2.3", "1.3.0", false, false); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("cross-major refused even with downgrade flags true (it's an upgrade, not a downgrade)", func(t *testing.T) {
		err := checkStabilizerVersionTransition("1.9.0", "2.0.0", true, true)
		if err == nil || !strings.Contains(err.Error(), "cross-major") {
			t.Errorf("err = %v, want a cross-major refusal", err)
		}
	})

	t.Run("cross-major downgrade also refused as cross-major", func(t *testing.T) {
		err := checkStabilizerVersionTransition("2.0.0", "1.9.0", true, true)
		if err == nil || !strings.Contains(err.Error(), "cross-major") {
			t.Errorf("err = %v, want a cross-major refusal", err)
		}
	})

	t.Run("same-major downgrade refused unless both flags allow it", func(t *testing.T) {
		if err := checkStabilizerVersionTransition("1.5.0", "1.4.0", false, true); err == nil {
			t.Error("want a refusal when the request itself didn't opt in")
		}
		if err := checkStabilizerVersionTransition("1.5.0", "1.4.0", true, false); err == nil {
			t.Error("want a refusal when the cluster doesn't permit downgrades")
		}
		if err := checkStabilizerVersionTransition("1.5.0", "1.4.0", true, true); err != nil {
			t.Errorf("err = %v, want nil when both the request and cluster allow it", err)
		}
	})

	t.Run("unparseable current version skips every check", func(t *testing.T) {
		if err := checkStabilizerVersionTransition("", "1.0.0", false, false); err != nil {
			t.Errorf("err = %v, want nil (nothing to compare against)", err)
		}
		if err := checkStabilizerVersionTransition("dev-abc123", "1.0.0", false, false); err != nil {
			t.Errorf("err = %v, want nil (nothing to compare against)", err)
		}
	})

	t.Run("equal version is a no-op, not a downgrade", func(t *testing.T) {
		if err := checkStabilizerVersionTransition("1.2.3", "1.2.3", false, false); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})
}

func TestClassifyStabilizerImagePins(t *testing.T) {
	t.Run("absent pins are ignored", func(t *testing.T) {
		cleared, err := classifyStabilizerImagePins(map[string]any{}, "1.2.3")
		if err != nil || len(cleared) != 0 {
			t.Errorf("cleared=%v err=%v, want no pins found", cleared, err)
		}
	})

	t.Run("a redundant release pin (matches current version) is masked silently", func(t *testing.T) {
		effective := map[string]any{
			"aileron": map[string]any{"image": map[string]any{"tag": "1.2.3"}},
		}
		cleared, err := classifyStabilizerImagePins(effective, "1.2.3")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(cleared) != 1 || cleared[0] != "aileron.image.tag" {
			t.Errorf("cleared = %v, want [aileron.image.tag]", cleared)
		}
	})

	t.Run("a release pin with a v-prefix is also recognized", func(t *testing.T) {
		effective := map[string]any{
			"image": map[string]any{"tag": "v1.2.3"},
		}
		cleared, err := classifyStabilizerImagePins(effective, "1.2.3")
		if err != nil || len(cleared) != 1 {
			t.Errorf("cleared=%v err=%v, want [image.tag]", cleared, err)
		}
	})

	t.Run("a genuine dev override refuses the change", func(t *testing.T) {
		effective := map[string]any{
			"aileron": map[string]any{"image": map[string]any{"tag": "my-dev-build"}},
		}
		_, err := classifyStabilizerImagePins(effective, "1.2.3")
		if err == nil || !strings.Contains(err.Error(), "aileron.image.tag") {
			t.Errorf("err = %v, want a refusal naming aileron.image.tag", err)
		}
	})

	t.Run("pinImageRef kind matches on a full image ref's trailing tag", func(t *testing.T) {
		effective := map[string]any{
			"aileron": map[string]any{"grading": map[string]any{"graderImage": "ghcr.io/ruddervirt/grader:1.2.3"}},
		}
		cleared, err := classifyStabilizerImagePins(effective, "1.2.3")
		if err != nil || len(cleared) != 1 || cleared[0] != "aileron.grading.graderImage" {
			t.Errorf("cleared=%v err=%v, want [aileron.grading.graderImage]", cleared, err)
		}
	})

	t.Run("empty-string pin values are ignored, not treated as dev overrides", func(t *testing.T) {
		effective := map[string]any{
			"vncAuthProxy": map[string]any{"image": map[string]any{"tag": ""}},
		}
		cleared, err := classifyStabilizerImagePins(effective, "1.2.3")
		if err != nil || len(cleared) != 0 {
			t.Errorf("cleared=%v err=%v, want nothing (empty is already cleared)", cleared, err)
		}
	})
}

func TestStabilizerVersionPickerOptions(t *testing.T) {
	tags := []string{"v1.4.0", "v1.3.0", "v1.2.3", "v1.2.0", "v0.9.0"}

	t.Run("filters to at-or-above the current declared version, preserving order", func(t *testing.T) {
		got := StabilizerVersionPickerOptions(tags, "1.2.3")
		want := []string{"v1.4.0", "v1.3.0", "v1.2.3"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("empty current version offers everything (nothing to compare against)", func(t *testing.T) {
		got := StabilizerVersionPickerOptions(tags, "")
		if len(got) != len(tags) {
			t.Errorf("got %v, want all %d tags", got, len(tags))
		}
	})

	t.Run("an unparseable current version is treated as no floor (defensive, shouldn't happen in practice)", func(t *testing.T) {
		got := StabilizerVersionPickerOptions(tags, "not-semver")
		if len(got) != len(tags) {
			t.Errorf("got %v, want all %d tags (comparison skipped)", got, len(tags))
		}
	})

	t.Run("no eligible tags returns nil, not a panic", func(t *testing.T) {
		got := StabilizerVersionPickerOptions(tags, "9.9.9")
		if len(got) != 0 {
			t.Errorf("got %v, want none", got)
		}
	})
}

func TestPreflightStabilizerVersionUpgrade(t *testing.T) {
	base := func() *settings.StabilizerSettingsState {
		return &settings.StabilizerSettingsState{
			SelfUpgradeEnabled: true,
			DeclaredChart:      "oci://ghcr.io/ruddervirt/charts/stabilizer",
			AllowedChart:       "oci://ghcr.io/ruddervirt/charts/stabilizer",
			HelmChartName:      "stabilizer",
		}
	}

	t.Run("passes when every guard is satisfied", func(t *testing.T) {
		if err := preflightStabilizerVersionUpgrade(base()); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("refuses when self-upgrade is disabled", func(t *testing.T) {
		s := base()
		s.SelfUpgradeEnabled = false
		if err := preflightStabilizerVersionUpgrade(s); err == nil {
			t.Error("want a refusal")
		}
	})

	t.Run("refuses while a release operation is already active", func(t *testing.T) {
		s := base()
		s.JobActive = true
		if err := preflightStabilizerVersionUpgrade(s); err == nil {
			t.Error("want a refusal")
		}
	})

	t.Run("refuses a release installed from a local chartContent", func(t *testing.T) {
		s := base()
		s.HasLocalChartContent = true
		if err := preflightStabilizerVersionUpgrade(s); err == nil {
			t.Error("want a refusal")
		}
	})

	t.Run("refuses when the release's chart doesn't match the agent's allowedChart", func(t *testing.T) {
		s := base()
		s.DeclaredChart = "oci://ghcr.io/someone-else/charts/stabilizer"
		if err := preflightStabilizerVersionUpgrade(s); err == nil {
			t.Error("want a refusal")
		}
	})

	t.Run("an empty allowedChart (agent doesn't declare one) skips that check", func(t *testing.T) {
		s := base()
		s.AllowedChart = ""
		s.DeclaredChart = "anything"
		if err := preflightStabilizerVersionUpgrade(s); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})
}

func TestPlanStabilizerVersionUpgrade(t *testing.T) {
	base := func() *settings.StabilizerSettingsState {
		return &settings.StabilizerSettingsState{
			SelfUpgradeEnabled: true,
			DeclaredVersion:    "1.2.3",
			DeclaredValues:     map[string]any{},
			HelmChartName:      "stabilizer",
			HelmChartNamespace: "kube-system",
		}
	}

	t.Run("happy path: patch sets spec.version and marshals as real JSON types", func(t *testing.T) {
		patch, cleared, err := PlanStabilizerVersionUpgrade(base(), "1.3.0")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(cleared) != 0 {
			t.Errorf("cleared = %v, want none (no pins present)", cleared)
		}
		var decoded struct {
			Spec struct {
				Version string         `json:"version"`
				Values  map[string]any `json:"values"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(patch, &decoded); err != nil {
			t.Fatalf("patch isn't valid JSON: %v\n%s", err, patch)
		}
		if decoded.Spec.Version != "1.3.0" {
			t.Errorf("spec.version = %q, want 1.3.0", decoded.Spec.Version)
		}
	})

	t.Run("redundant image pins are masked into the same patch", func(t *testing.T) {
		s := base()
		s.DeclaredValues = map[string]any{"aileron": map[string]any{"image": map[string]any{"tag": "1.2.3"}}}
		patch, cleared, err := PlanStabilizerVersionUpgrade(s, "1.3.0")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(cleared) != 1 || cleared[0] != "aileron.image.tag" {
			t.Errorf("cleared = %v, want [aileron.image.tag]", cleared)
		}
		var decoded struct {
			Spec struct {
				Values map[string]any `json:"values"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(patch, &decoded); err != nil {
			t.Fatalf("patch isn't valid JSON: %v", err)
		}
		tag, ok := settings.GetByPath(decoded.Spec.Values, "aileron.image.tag")
		if !ok || tag != "" {
			t.Errorf("aileron.image.tag in patch = %v (ok=%v), want masked to empty string", tag, ok)
		}
	})

	t.Run("refuses via preflight before ever validating the target version", func(t *testing.T) {
		s := base()
		s.SelfUpgradeEnabled = false
		if _, _, err := PlanStabilizerVersionUpgrade(s, "not-even-semver"); err == nil ||
			strings.Contains(err.Error(), "invalid chart version") {
			t.Errorf("err = %v, want the preflight refusal, not a version-shape error", err)
		}
	})

	t.Run("propagates an invalid target version", func(t *testing.T) {
		if _, _, err := PlanStabilizerVersionUpgrade(base(), "v1.3.0"); err == nil {
			t.Error("want an error for a v-prefixed version")
		}
	})

	t.Run("propagates a cross-major refusal", func(t *testing.T) {
		if _, _, err := PlanStabilizerVersionUpgrade(base(), "2.0.0"); err == nil || !strings.Contains(err.Error(), "cross-major") {
			t.Errorf("err = %v, want a cross-major refusal", err)
		}
	})

	t.Run("propagates a dev image-pin refusal", func(t *testing.T) {
		s := base()
		s.DeclaredValues = map[string]any{"aileron": map[string]any{"image": map[string]any{"tag": "my-dev-build"}}}
		if _, _, err := PlanStabilizerVersionUpgrade(s, "1.3.0"); err == nil {
			t.Error("want a refusal naming the dev pin")
		}
	})

	t.Run("same version with nothing to clear is refused as a no-op", func(t *testing.T) {
		if _, _, err := PlanStabilizerVersionUpgrade(base(), "1.2.3"); err == nil || !strings.Contains(err.Error(), "already at") {
			t.Errorf("err = %v, want an \"already at\" no-op refusal", err)
		}
	})

	t.Run("same version WITH a redundant pin to clear is still a real change", func(t *testing.T) {
		s := base()
		s.DeclaredValues = map[string]any{"image": map[string]any{"tag": "1.2.3"}}
		patch, cleared, err := PlanStabilizerVersionUpgrade(s, "1.2.3")
		if err != nil {
			t.Fatalf("err = %v, want nil (pin-only change is not a no-op)", err)
		}
		if len(cleared) != 1 {
			t.Errorf("cleared = %v, want one pin cleared", cleared)
		}
		if len(patch) == 0 {
			t.Error("want a non-empty patch")
		}
	})
}
