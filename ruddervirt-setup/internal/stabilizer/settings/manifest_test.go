// SPDX-License-Identifier: GPL-3.0-only

package settings

import "testing"

func TestStabilizerSettingDefsLoaded(t *testing.T) {
	if len(StabilizerSettingDefs) != 11 {
		t.Fatalf("got %d settings, want 11 (the vendored stabilizer-settings.yaml)", len(StabilizerSettingDefs))
	}

	want := []struct {
		key          string
		path         string
		env          string
		typ          StabilizerSettingType
		component    string
		hasUnlimited bool
	}{
		{"ollama_enabled", "ollama.enabled", "SETTING_OLLAMA_ENABLED", StabilizerSettingBool, "stabilizer", false},
		{"aileron_ui_enabled", "aileron.aileronUI.enabled", "SETTING_AILERON_UI_ENABLED", StabilizerSettingBool, "aileron", false},
		{"build_max_cpu", "aileron.buildLimits.maxCPU", "SETTING_BUILD_MAX_CPU", StabilizerSettingInt, "aileron", true},
		{"build_max_memory", "aileron.buildLimits.maxMemory", "SETTING_BUILD_MAX_MEMORY", StabilizerSettingQuantity, "aileron", true},
		{"build_max_disk_size", "aileron.buildLimits.maxDiskSize", "SETTING_BUILD_MAX_DISK_SIZE", StabilizerSettingQuantity, "aileron", true},
		{"build_max_disk_count", "aileron.buildLimits.maxDiskCount", "SETTING_BUILD_MAX_DISK_COUNT", StabilizerSettingInt, "aileron", true},
		{"build_max_vm_count", "aileron.buildLimits.maxVMCount", "SETTING_BUILD_MAX_VM_COUNT", StabilizerSettingInt, "aileron", true},
		{"grading_max_concurrent", "aileron.grading.maxConcurrent", "SETTING_GRADING_MAX_CONCURRENT", StabilizerSettingInt, "aileron", true},
		{"watchdog_enabled", "watchdog.enabled", "SETTING_WATCHDOG_ENABLED", StabilizerSettingBool, "stabilizer", false},
		{"watchdog_vm_timeout_minutes", "watchdog.vmTimeoutMinutes", "SETTING_WATCHDOG_VM_TIMEOUT_MINUTES", StabilizerSettingInt, "stabilizer", false},
		{"watchdog_max_vm_runtime_minutes", "watchdog.maxVMRuntimeMinutes", "SETTING_WATCHDOG_MAX_VM_RUNTIME_MINUTES", StabilizerSettingInt, "stabilizer", false},
	}

	for _, w := range want {
		d, ok := StabilizerSettingByKey(w.key)
		if !ok {
			t.Errorf("missing setting %q", w.key)
			continue
		}
		if d.Path != w.path {
			t.Errorf("%s: Path = %q, want %q", w.key, d.Path, w.path)
		}
		if d.Env != w.env {
			t.Errorf("%s: Env = %q, want %q", w.key, d.Env, w.env)
		}
		if d.Type != w.typ {
			t.Errorf("%s: Type = %q, want %q", w.key, d.Type, w.typ)
		}
		if d.Component != w.component {
			t.Errorf("%s: Component = %q, want %q", w.key, d.Component, w.component)
		}
		if d.HasUnlimited() != w.hasUnlimited {
			t.Errorf("%s: hasUnlimited() = %v, want %v", w.key, d.HasUnlimited(), w.hasUnlimited)
		}
		if d.Summary == "" {
			t.Errorf("%s: Summary is empty", w.key)
		}
	}

	// The two watchdog timeouts have min: 1, not 0 - the agent reads them
	// with a positive-only parse, so a 0 would silently resolve to the
	// chart default and the reported value would disagree with the resource.
	for _, key := range []string{"watchdog_vm_timeout_minutes", "watchdog_max_vm_runtime_minutes"} {
		d, _ := StabilizerSettingByKey(key)
		if d.Min != 1 {
			t.Errorf("%s: Min = %d, want 1", key, d.Min)
		}
	}
}

func TestGetByPath(t *testing.T) {
	values := map[string]any{
		"aileron": map[string]any{
			"buildLimits": map[string]any{
				"maxCPU": float64(16),
			},
		},
		"watchdog": map[string]any{
			"enabled": true,
		},
	}

	t.Run("present nested value", func(t *testing.T) {
		v, ok := GetByPath(values, "aileron.buildLimits.maxCPU")
		if !ok || v != float64(16) {
			t.Errorf("GetByPath = %v, %v, want 16, true", v, ok)
		}
	})

	t.Run("present top-level value", func(t *testing.T) {
		v, ok := GetByPath(values, "watchdog.enabled")
		if !ok || v != true {
			t.Errorf("GetByPath = %v, %v, want true, true", v, ok)
		}
	})

	t.Run("absent path segment reports not-ok, not a panic", func(t *testing.T) {
		if _, ok := GetByPath(values, "aileron.buildLimits.maxMemory"); ok {
			t.Error("want ok=false for an absent leaf")
		}
		if _, ok := GetByPath(values, "nats.url"); ok {
			t.Error("want ok=false for an entirely absent top-level key")
		}
	})

	t.Run("path through a non-map value reports not-ok", func(t *testing.T) {
		if _, ok := GetByPath(values, "watchdog.enabled.nope"); ok {
			t.Error("want ok=false when a path segment isn't itself a map")
		}
	})
}

func TestSetByPath(t *testing.T) {
	dst := map[string]any{}
	SetByPath(dst, "aileron.buildLimits.maxCPU", 16)
	SetByPath(dst, "aileron.buildLimits.maxMemory", "16Gi")
	SetByPath(dst, "watchdog.enabled", false)

	v, ok := GetByPath(dst, "aileron.buildLimits.maxCPU")
	if !ok || v != 16 {
		t.Errorf("maxCPU = %v, %v, want 16, true", v, ok)
	}
	v, ok = GetByPath(dst, "aileron.buildLimits.maxMemory")
	if !ok || v != "16Gi" {
		t.Errorf("maxMemory = %v, %v, want 16Gi, true - setting a sibling path must not clobber maxCPU's map", v, ok)
	}
	v, ok = GetByPath(dst, "watchdog.enabled")
	if !ok || v != false {
		t.Errorf("watchdog.enabled = %v, %v, want false, true", v, ok)
	}
}
