// SPDX-License-Identifier: GPL-3.0-only

package settings

import "testing"

func TestParseStabilizerSettingValue_Bool(t *testing.T) {
	d, _ := StabilizerSettingByKey("watchdog_enabled")
	cases := []struct {
		raw     string
		want    any
		wantErr bool
	}{
		{"true", true, false},
		{"false", false, false},
		{"True", true, false},
		{"FALSE", false, false},
		{"1", nil, true},
		{"yes", nil, true},
		{"", nil, true},
	}
	for _, c := range cases {
		got, err := ParseStabilizerSettingValue(d, c.raw)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseStabilizerSettingValue(bool, %q) err = %v, wantErr %v", c.raw, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("ParseStabilizerSettingValue(bool, %q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestParseStabilizerSettingValue_Int(t *testing.T) {
	d, _ := StabilizerSettingByKey("build_max_cpu") // min 0, max 1024, unlimited 0
	cases := []struct {
		raw     string
		want    any
		wantErr bool
	}{
		{"16", 16, false},
		{"0", 0, false},
		{"1024", 1024, false},
		{"1025", nil, true},  // above max
		{"-1", nil, true},    // below min
		{"99999", nil, true}, // the task's explicit out-of-bounds example
		{"lots", nil, true},
		{"16.5", nil, true},
		{"", nil, true},
		{"unlimited", 0, false}, // explicit affordance, resolves to the unlimited sentinel
	}
	for _, c := range cases {
		got, err := ParseStabilizerSettingValue(d, c.raw)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseStabilizerSettingValue(int, %q) err = %v, wantErr %v", c.raw, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("ParseStabilizerSettingValue(int, %q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestParseStabilizerSettingValue_IntNoUnlimited(t *testing.T) {
	// watchdog_vm_timeout_minutes: min 1, no "unlimited" - 0 must be
	// rejected (min bound), and the "unlimited" keyword must error since
	// the setting declares none.
	d, _ := StabilizerSettingByKey("watchdog_vm_timeout_minutes")
	if _, err := ParseStabilizerSettingValue(d, "0"); err == nil {
		t.Error("want an error for watchdog_vm_timeout_minutes=0 (below min:1)")
	}
	if _, err := ParseStabilizerSettingValue(d, "unlimited"); err == nil {
		t.Error("want an error requesting \"unlimited\" for a setting with no unlimited value")
	}
	got, err := ParseStabilizerSettingValue(d, "60")
	if err != nil || got != 60 {
		t.Errorf("ParseStabilizerSettingValue(60) = %v, %v, want 60, nil", got, err)
	}
}

func TestParseStabilizerSettingValue_Quantity(t *testing.T) {
	d, _ := StabilizerSettingByKey("build_max_memory") // unlimited ""
	cases := []struct {
		raw     string
		want    any
		wantErr bool
	}{
		{"16Gi", "16Gi", false},
		{"500Mi", "500Mi", false},
		{"2Ti", "2Ti", false},
		{"8", "8", false},
		{"1.5Gi", "1.5Gi", false},
		{"", "", false},          // documented unlimited spelling
		{"unlimited", "", false}, // explicit affordance
		{"16 Gi", nil, true},     // the task's explicit "don't be clever" example: no space
		{"lots", nil, true},      // the task's explicit example: not a quantity at all
		{"16GB", nil, true},      // not a Kubernetes suffix
		{"16gi", nil, true},      // wrong case
	}
	for _, c := range cases {
		got, err := ParseStabilizerSettingValue(d, c.raw)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseStabilizerSettingValue(quantity, %q) err = %v, wantErr %v", c.raw, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("ParseStabilizerSettingValue(quantity, %q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestParseStabilizerSettingValue_QuantityTooLong(t *testing.T) {
	d, _ := StabilizerSettingByKey("build_max_memory")
	huge := ""
	for i := 0; i < 40; i++ {
		huge += "9"
	}
	if _, err := ParseStabilizerSettingValue(d, huge); err == nil {
		t.Error("want an error for an oversized quantity string")
	}
}

func TestStabilizerSettingValuesEqual(t *testing.T) {
	memDef, _ := StabilizerSettingByKey("build_max_memory")
	if !StabilizerSettingValuesEqual(memDef, "1Gi", "1024Mi") {
		t.Error("1Gi and 1024Mi should compare equal (same quantity, different spelling)")
	}
	if StabilizerSettingValuesEqual(memDef, "1Gi", "2Gi") {
		t.Error("1Gi and 2Gi should not compare equal")
	}
	if !StabilizerSettingValuesEqual(memDef, "", "") {
		t.Error("empty (unlimited) should equal empty")
	}
	if StabilizerSettingValuesEqual(memDef, "", "1Gi") {
		t.Error("empty (unlimited) should not equal a bounded quantity")
	}

	cpuDef, _ := StabilizerSettingByKey("build_max_cpu")
	if !StabilizerSettingValuesEqual(cpuDef, 16, 16) {
		t.Error("16 should equal 16")
	}
	if StabilizerSettingValuesEqual(cpuDef, 16, 8) {
		t.Error("16 should not equal 8")
	}
}

func TestFormatStabilizerSettingValue(t *testing.T) {
	cpuDef, _ := StabilizerSettingByKey("build_max_cpu")
	if got := FormatStabilizerSettingValue(cpuDef, 0); got != "0 (unlimited)" {
		t.Errorf("FormatStabilizerSettingValue(0) = %q, want annotated as unlimited", got)
	}
	if got := FormatStabilizerSettingValue(cpuDef, 16); got != "16" {
		t.Errorf("FormatStabilizerSettingValue(16) = %q, want \"16\" (no annotation)", got)
	}

	memDef, _ := StabilizerSettingByKey("build_max_memory")
	if got := FormatStabilizerSettingValue(memDef, ""); got != `"" (unlimited)` {
		t.Errorf("FormatStabilizerSettingValue(\"\") = %q, want annotated as unlimited", got)
	}

	watchdogDef, _ := StabilizerSettingByKey("watchdog_vm_timeout_minutes")
	if got := FormatStabilizerSettingValue(watchdogDef, 0); got != "0" {
		t.Errorf("FormatStabilizerSettingValue(0) for a setting with no unlimited = %q, want unannotated \"0\"", got)
	}
}

func TestCoerceJSONValue(t *testing.T) {
	cpuDef, _ := StabilizerSettingByKey("build_max_cpu")
	if v, ok := CoerceJSONValue(cpuDef, float64(16)); !ok || v != 16 {
		t.Errorf("CoerceJSONValue(16.0) = %v, %v, want 16, true", v, ok)
	}
	if _, ok := CoerceJSONValue(cpuDef, float64(16.5)); ok {
		t.Error("want ok=false for a non-whole float64 int setting")
	}
	if _, ok := CoerceJSONValue(cpuDef, "16"); ok {
		t.Error("want ok=false for a string where an int is expected")
	}

	boolDef, _ := StabilizerSettingByKey("watchdog_enabled")
	if v, ok := CoerceJSONValue(boolDef, true); !ok || v != true {
		t.Errorf("CoerceJSONValue(true) = %v, %v, want true, true", v, ok)
	}

	memDef, _ := StabilizerSettingByKey("build_max_memory")
	if v, ok := CoerceJSONValue(memDef, "16Gi"); !ok || v != "16Gi" {
		t.Errorf("CoerceJSONValue(16Gi) = %v, %v, want 16Gi, true", v, ok)
	}
}
