// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import "testing"

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
