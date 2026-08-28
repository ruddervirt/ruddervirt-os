// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	_ "embed"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file, stabilizer_settings_cli.go, and stabilizer_settings_validate.go
// implement `ruddervirt-setup settings` - a non-interactive CLI subcommand
// (dispatched in main.go before the TUI ever starts) that reads and changes
// stabilizer's configurable settings directly on the HelmChart resource,
// without needing helm or kubectl expertise.
//
// stabilizer-settings.yaml (go:embed'd below) is a vendored, hand-maintained
// copy of the stabilizer repo's internal/settings/settings.yaml - the single
// definition of every settable value, shared with stabilizer's own Go
// handler and the cloud UI. It is NOT fetched at build time: ruddervirt/
// stabilizer is a private repo (no anonymous GitHub API/release access), so
// there is no way for an arbitrary `go build` here to pull it automatically.
// Instead it's committed to this repo and bumped by hand whenever the pinned
// stabilizer version (defaultStabilizerVersion, default-versions.go) changes
// - the same "hand-maintained, go:embed'd from the package directory"
// pattern default-versions.yaml itself already uses.
//
// DRIVE EVERYTHING FROM THIS FILE. Do not hardcode the setting list
// elsewhere - there are 11 settings today and the set will grow; a
// hardcoded list silently omits new ones and the writers (this tool, the
// cloud UI, stabilizer's own Go handler) stop agreeing about what exists.

//go:embed stabilizer-settings.yaml
var stabilizerSettingsYAML []byte

// stabilizerSettingType is one of the three value shapes a setting can have
// - mirrors stabilizer's own settings.Type.
type stabilizerSettingType string

const (
	stabilizerSettingBool     stabilizerSettingType = "bool"
	stabilizerSettingInt      stabilizerSettingType = "int"
	stabilizerSettingQuantity stabilizerSettingType = "quantity"
)

// stabilizerSettingDef is one entry from stabilizer-settings.yaml - field
// names and shapes mirror stabilizer's internal/settings.Setting exactly
// (see that repo's internal/settings/settings.yaml header comment for the
// authoritative field-by-field explanation, reproduced in short below).
//
// Unlimited and Default are `any` deliberately, not typed to a single Go
// type: Unlimited is an int (0) for int-typed settings, a string ("") for
// quantity-typed ones, and absent (nil) for settings with no "no limit"
// concept at all (the two watchdog timeouts) - after yaml.Unmarshal, a
// present-but-zero-value YAML key still leaves this field non-nil (e.g. int
// 0 or string ""), while a genuinely absent key leaves it nil. That
// nil-vs-present distinction is exactly what callers need: "unlimited" is a
// deliberate choice a setting either offers or doesn't, never "unset".
type stabilizerSettingDef struct {
	Key       string                `yaml:"key"`
	Path      string                `yaml:"path"`
	Env       string                `yaml:"env"`
	Type      stabilizerSettingType `yaml:"type"`
	Min       int                   `yaml:"min"`
	Max       int                   `yaml:"max"`
	Unlimited any                   `yaml:"unlimited"`
	Default   any                   `yaml:"default"`
	Component string                `yaml:"component"`
	Summary   string                `yaml:"summary"`
	Detail    string                `yaml:"detail"`
}

// hasUnlimited reports whether d declares a value meaning "no limit" -
// distinct from d.Unlimited simply being Go's zero value, per the doc
// comment above.
func (d stabilizerSettingDef) hasUnlimited() bool {
	return d.Unlimited != nil
}

type stabilizerSettingsFile struct {
	Settings []stabilizerSettingDef `yaml:"settings"`
}

// stabilizerSettingDefs and stabilizerSettingDefsByKey are populated once at
// init() from the embedded YAML - same "compiled-in, parsed once at
// startup, panic on malformed embedded data" role default-versions.go's own
// init() plays, since a parse failure here means the vendored file itself
// is broken, a build-time bug that should fail loudly rather than leave the
// settings command silently offering nothing.
var (
	stabilizerSettingDefs      []stabilizerSettingDef
	stabilizerSettingDefsByKey map[string]stabilizerSettingDef
)

func init() {
	var f stabilizerSettingsFile
	if err := yaml.Unmarshal(stabilizerSettingsYAML, &f); err != nil {
		panic(fmt.Sprintf("ruddervirt-setup/stabilizer-settings.yaml failed to parse: %v", err))
	}
	if len(f.Settings) == 0 {
		panic("ruddervirt-setup/stabilizer-settings.yaml declares no settings")
	}

	byKey := make(map[string]stabilizerSettingDef, len(f.Settings))
	for _, d := range f.Settings {
		// Same sanity checks stabilizer's own settings.go init() applies -
		// a vendored copy that fails these would silently disagree with
		// what stabilizer/the cloud UI actually enforce.
		if d.Key == "" || d.Path == "" || d.Env == "" || d.Component == "" || d.Summary == "" {
			panic(fmt.Sprintf("ruddervirt-setup/stabilizer-settings.yaml: setting %q is missing a required field (key/path/env/component/summary)", d.Key))
		}
		if d.Component != "stabilizer" && d.Component != "aileron" {
			panic(fmt.Sprintf("ruddervirt-setup/stabilizer-settings.yaml: setting %q has unknown component %q", d.Key, d.Component))
		}
		switch d.Type {
		case stabilizerSettingBool, stabilizerSettingInt, stabilizerSettingQuantity:
		default:
			panic(fmt.Sprintf("ruddervirt-setup/stabilizer-settings.yaml: setting %q has unknown type %q", d.Key, d.Type))
		}
		if d.Type == stabilizerSettingInt && d.Max <= d.Min {
			panic(fmt.Sprintf("ruddervirt-setup/stabilizer-settings.yaml: setting %q has max <= min (%d <= %d)", d.Key, d.Max, d.Min))
		}
		if _, dup := byKey[d.Key]; dup {
			panic(fmt.Sprintf("ruddervirt-setup/stabilizer-settings.yaml: duplicate setting key %q", d.Key))
		}
		byKey[d.Key] = d
	}

	stabilizerSettingDefs = f.Settings
	stabilizerSettingDefsByKey = byKey
}

// stabilizerSettingByKey looks up a setting by its wire key (e.g.
// "build_max_cpu").
func stabilizerSettingByKey(key string) (stabilizerSettingDef, bool) {
	d, ok := stabilizerSettingDefsByKey[key]
	return d, ok
}

// pathSegments splits a dotted settings-manifest path ("aileron.buildLimits.maxCPU")
// into its segments.
func pathSegments(path string) []string {
	return strings.Split(path, ".")
}

// getByPath walks values (as decoded from spec.values JSON - nested objects
// become map[string]any) along path's dotted segments and returns the leaf
// value found there. ok is false if any segment along the way is absent or
// not itself a map[string]any (i.e. the path doesn't exist in values at
// all) - callers must treat that as "no declared value; the chart default
// applies", never as an error.
func getByPath(values map[string]any, path string) (v any, ok bool) {
	cur := any(values)
	for _, seg := range pathSegments(path) {
		m, isMap := cur.(map[string]any)
		if !isMap {
			return nil, false
		}
		next, present := m[seg]
		if !present {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// setByPath writes value into dst at path's dotted segments, creating
// intermediate map[string]any nodes as needed and merging into any that
// already exist (so two settings sharing a path prefix, e.g.
// aileron.buildLimits.maxCPU and aileron.buildLimits.maxMemory, both land
// under the same nested aileron.buildLimits map instead of one clobbering
// the other).
func setByPath(dst map[string]any, path string, value any) {
	segs := pathSegments(path)
	cur := dst
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
	cur[segs[len(segs)-1]] = value
}
