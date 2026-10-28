// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"strings"

	tfstatev1 "github.com/eshu-hq/eshu-sdk/factschema/terraformstate/v1"
)

// driftAttributeDenyTypes excludes resource types from allowlisted attribute
// emission even when the contracts allowlist covers them. aws_iam_policy is
// allowlisted host-side (policy) but the policy document is a hard-sensitive
// credential-bearing value satellite-side, so it is never emitted; the host
// classifier skips absent state values, making this a documented coverage
// gap rather than false drift.
var driftAttributeDenyTypes = map[string]bool{
	"aws_iam_policy": true,
}

// driftAttributes extracts the contracts-allowlisted leaf paths
// (tfstatev1.DriftAllowlistFor) from one state instance's raw attributes and
// re-nests them as pure maps. The host drift evidence loader flattens nested
// objects itself and ignores array wrappers, so pure-map re-nesting flattens
// to the same dotted keys the classifier compares. It returns nil when the
// type is unlisted or denied, or when no allowlisted leaf is present, so the
// caller omits the attributes key. Lookup descends first-array-elements only,
// mirroring the loader's first-wins singleton policy; a malformed instance
// yields no attributes rather than failing the claim.
func driftAttributes(resourceType string, attrs map[string]any) map[string]any {
	resourceType = strings.TrimSpace(resourceType)
	if resourceType == "" || driftAttributeDenyTypes[resourceType] {
		return nil
	}
	var out map[string]any
	for _, path := range tfstatev1.DriftAllowlistFor(resourceType) {
		segments := strings.Split(path, ".")
		value, ok := lookupAttributePath(attrs, segments)
		if !ok {
			continue
		}
		if out == nil {
			out = map[string]any{}
		}
		setAttributePath(out, segments, value)
	}
	return out
}

// lookupAttributePath walks nested maps along segments, descending into the
// first element of an array-of-maps at any level (the state's
// nested-singleton-array shape). It reports false when any segment cannot be
// resolved to a map (mid-path) or is absent (leaf).
func lookupAttributePath(node any, segments []string) (any, bool) {
	current := node
	for i, segment := range segments {
		for {
			if arr, ok := current.([]any); ok {
				if len(arr) == 0 {
					return nil, false
				}
				current = arr[0]
				continue
			}
			break
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok := obj[segment]
		if !ok {
			return nil, false
		}
		if i == len(segments)-1 {
			return value, true
		}
		current = value
	}
	return nil, false
}

// setAttributePath nests value under segments in out, creating intermediate
// maps. Leaf values are stored as observed; intermediate arrays from the
// source shape are intentionally not reproduced (the loader's flattening is
// array-transparent).
func setAttributePath(out map[string]any, segments []string, value any) {
	node := out
	for _, segment := range segments[:len(segments)-1] {
		next, ok := node[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			node[segment] = next
		}
		node = next
	}
	node[segments[len(segments)-1]] = value
}

// firstInstanceAttributes returns the attributes object of the first instance
// carrying one, or nil when no instance does. Multi-instance resources
// share one stable address key (the satellite collapses them, as the merge
// test pins), so the first instance's attributes stand in for the set; a
// resource with no usable attributes emits join keys only.
func firstInstanceAttributes(instances []any) map[string]any {
	for _, instance := range instances {
		obj, ok := instance.(map[string]any)
		if !ok {
			continue
		}
		attrs, ok := obj["attributes"].(map[string]any)
		if !ok {
			continue
		}
		return attrs
	}
	return nil
}
