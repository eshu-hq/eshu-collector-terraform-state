// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"strings"
	"testing"

	tfstatev1 "github.com/eshu-hq/eshu-sdk/factschema/terraformstate/v1"
)

// The in-tree terraformstate package covers this table through parser
// redaction tests that need the core-owned redact engine. These contract
// tests pin the ported table entries directly.
func TestIsHardSensitiveStateAttributeMatchesListedPairs(t *testing.T) {
	t.Parallel()

	for _, pair := range [][2]string{
		{"aws_iam_policy", "policy"},
		{"aws_ssm_parameter", "value"},
		{"aws_lambda_function", "environment"},
		{"aws_cloudwatch_event_target", "input"},
		{"aws_sns_topic_subscription", "endpoint"},
		{"pagerduty_service_integration", "routing_key"},
		{"pagerduty_service_integration", "integration_key"},
		{"pagerduty_user", "email"},
		{"pagerduty_webhook_subscription", "webhook_secret"},
	} {
		if !isHardSensitiveStateAttribute(pair[0], pair[1]) {
			t.Errorf("isHardSensitiveStateAttribute(%q, %q) = false, want true", pair[0], pair[1])
		}
	}
}

func TestIsHardSensitiveStateAttributeRejectsUnlistedPairs(t *testing.T) {
	t.Parallel()

	for _, pair := range [][2]string{
		{"aws_s3_bucket", "bucket"},
		{"aws_iam_policy", "name"},
		{"unknown_type", "value"},
		{"", ""},
	} {
		if isHardSensitiveStateAttribute(pair[0], pair[1]) {
			t.Errorf("isHardSensitiveStateAttribute(%q, %q) = true, want false", pair[0], pair[1])
		}
	}
}

func TestIsHardSensitiveStateAttributeTrimsWhitespace(t *testing.T) {
	t.Parallel()

	if !isHardSensitiveStateAttribute(" aws_ssm_parameter ", " value ") {
		t.Fatal("isHardSensitiveStateAttribute() with padded input = false, want true")
	}
}

// TestAllowlistNeverEmitsHardSensitiveLeaf proves the fail-closed emission
// claim against contracts bumps: for every hard-sensitive (type, leaf) pair,
// either the contracts allowlist carries no path resolving to that leaf for
// the type, or the type is covered by driftAttributeDenyTypes. A future
// allowlist addition that would emit a credential-bearing value fails here
// instead of leaking silently.
func TestAllowlistNeverEmitsHardSensitiveLeaf(t *testing.T) {
	t.Parallel()

	for resourceType, leaves := range hardSensitiveStateAttributes {
		for leaf := range leaves {
			emits := false
			for _, path := range tfstatev1.DriftAllowlistFor(resourceType) {
				segments := strings.Split(path, ".")
				if segments[len(segments)-1] == leaf {
					emits = true
					break
				}
			}
			if emits && !driftAttributeDenyTypes[resourceType] {
				t.Errorf("allowlist path for %q resolves to hard-sensitive leaf %q without deny coverage", resourceType, leaf)
			}
		}
	}
}

// TestDenyCoversAllowlistedSensitivePairs proves every contracts-allowlisted
// path whose leaf is hard-sensitive is gated by driftAttributeDenyTypes, so
// the deny-wins rule holds by construction rather than by current table luck
// (today: aws_iam_policy/policy).
func TestDenyCoversAllowlistedSensitivePairs(t *testing.T) {
	t.Parallel()

	for _, resourceType := range tfstatev1.DriftAllowlistResourceTypes() {
		for _, path := range tfstatev1.DriftAllowlistFor(resourceType) {
			segments := strings.Split(path, ".")
			leaf := segments[len(segments)-1]
			if isHardSensitiveStateAttribute(resourceType, leaf) && !driftAttributeDenyTypes[resourceType] {
				t.Errorf("allowlisted path %q for %q ends in hard-sensitive leaf %q without deny coverage", path, resourceType, leaf)
			}
		}
	}
	if !driftAttributeDenyTypes["aws_iam_policy"] {
		t.Error("driftAttributeDenyTypes must cover aws_iam_policy (contracts-allowlisted policy document)")
	}
}
