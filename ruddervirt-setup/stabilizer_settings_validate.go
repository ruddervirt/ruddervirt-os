// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// maxQuantityLen mirrors stabilizer's own internal/handler/upgrade.go
// maxQuantityLen constant exactly - a resource quantity is never anywhere
// close to this long ("1500Gi" is 6 characters), so this rejects an
// oversized payload before it's even handed to resource.ParseQuantity.
const maxQuantityLen = 32

// parseStabilizerSettingValue is the ONLY validator on this path: unlike a
// settings change from the cloud UI (which goes through stabilizer's own Go
// handler - validateSetting in internal/handler/upgrade.go - before it ever
// reaches the cluster), a value written by this tool goes straight into
// spec.values with nothing else checking it. A bad value here is not
// rejected downstream - it lands on a cluster nobody can reach remotely to
// fix. So this enforces exactly the same rules stabilizer's own handler
// does, using the real Kubernetes quantity parser (resource.ParseQuantity)
// rather than a hand-rolled approximation, for byte-for-byte parity.
//
// raw is the operator's literal --set value. The literal string "unlimited"
// (case-insensitive) is accepted as an explicit alias for d.Unlimited, when
// d has one - so nobody has to already know that 0 or "" means "no limit".
func parseStabilizerSettingValue(d stabilizerSettingDef, raw string) (any, error) {
	if strings.EqualFold(strings.TrimSpace(raw), "unlimited") {
		if !d.hasUnlimited() {
			return nil, fmt.Errorf("%s has no \"unlimited\" value", d.Key)
		}
		raw = fmt.Sprint(d.Unlimited)
	}

	switch d.Type {
	case stabilizerSettingBool:
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, fmt.Errorf("%s must be a boolean (true or false), got %q", d.Key, raw)
		}

	case stabilizerSettingInt:
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%s must be an integer, got %q", d.Key, raw)
		}
		if n < d.Min || n > d.Max {
			return nil, fmt.Errorf("%s must be between %d and %d, got %d", d.Key, d.Min, d.Max, n)
		}
		return n, nil

	case stabilizerSettingQuantity:
		q := strings.TrimSpace(raw)
		if q == "" {
			if !d.hasUnlimited() {
				return nil, fmt.Errorf("%s must not be empty", d.Key)
			}
			return "", nil // the documented "unlimited" for quantity settings
		}
		if len(q) > maxQuantityLen {
			return nil, fmt.Errorf("%s is %d characters; a resource quantity is never that long", d.Key, len(q))
		}
		if _, err := resource.ParseQuantity(q); err != nil {
			return nil, fmt.Errorf("%s must be a Kubernetes quantity such as \"16Gi\" (or \"unlimited\"), got %q", d.Key, q)
		}
		return q, nil

	default:
		return nil, fmt.Errorf("%s has unknown type %q", d.Key, d.Type)
	}
}

// coerceJSONValue normalizes a value decoded from `kubectl -o json` (a
// generic map[string]any tree: JSON numbers arrive as float64, JSON objects
// as map[string]any) into the same Go type parseStabilizerSettingValue
// returns for d - so declared and applied/requested values can be compared
// directly. ok is false if raw isn't a legal encoding of d's type (e.g. a
// non-whole float64 for an int setting) - callers must treat that as "the
// resource disagrees with the manifest", not silently coerce it.
func coerceJSONValue(d stabilizerSettingDef, raw any) (any, bool) {
	switch d.Type {
	case stabilizerSettingBool:
		b, ok := raw.(bool)
		return b, ok
	case stabilizerSettingInt:
		switch n := raw.(type) {
		case float64:
			if math.Trunc(n) != n {
				return nil, false
			}
			return int(n), true
		case int:
			return n, true
		default:
			return nil, false
		}
	case stabilizerSettingQuantity:
		s, ok := raw.(string)
		return s, ok
	default:
		return nil, false
	}
}

// stabilizerSettingValuesEqual reports whether a and b (both already
// coerced/parsed into d's Go type, per parseStabilizerSettingValue/
// coerceJSONValue) represent the same setting value. Quantities compare
// semantically via resource.ParseQuantity (e.g. "1Gi" == "1024Mi"), not
// byte-for-byte, since a merge patch that changes a quantity's spelling
// without changing its value would still bounce the release for nothing -
// exactly the redundant-write case this equality check exists to catch. An
// empty quantity string ("unlimited") only equals another empty string.
func stabilizerSettingValuesEqual(d stabilizerSettingDef, a, b any) bool {
	switch d.Type {
	case stabilizerSettingQuantity:
		as, aok := a.(string)
		bs, bok := b.(string)
		if !aok || !bok {
			return false
		}
		if as == "" || bs == "" {
			return as == bs
		}
		aq, aerr := resource.ParseQuantity(as)
		bq, berr := resource.ParseQuantity(bs)
		if aerr != nil || berr != nil {
			return as == bs
		}
		return aq.Cmp(bq) == 0
	default:
		return a == b
	}
}

// formatStabilizerSettingValue renders value (in d's Go type) for display,
// annotating it as "(unlimited)" when it equals d's declared unlimited
// sentinel - so an operator sees what a bare 0 or empty string actually
// means instead of having to already know the convention.
func formatStabilizerSettingValue(d stabilizerSettingDef, value any) string {
	var s string
	switch v := value.(type) {
	case bool:
		s = strconv.FormatBool(v)
	case int:
		s = strconv.Itoa(v)
	case string:
		if v == "" {
			s = `""`
		} else {
			s = v
		}
	default:
		s = fmt.Sprint(v)
	}
	if d.hasUnlimited() && stabilizerSettingValuesEqual(d, value, mustCoerceUnlimited(d)) {
		s += " (unlimited)"
	}
	return s
}

// mustCoerceUnlimited returns d.Unlimited in the same Go type
// parseStabilizerSettingValue produces - safe to call only when
// d.hasUnlimited() is true.
func mustCoerceUnlimited(d stabilizerSettingDef) any {
	switch d.Type {
	case stabilizerSettingInt:
		switch n := d.Unlimited.(type) {
		case int:
			return n
		case float64:
			return int(n)
		}
	case stabilizerSettingQuantity:
		if s, ok := d.Unlimited.(string); ok {
			return s
		}
	}
	return d.Unlimited
}
