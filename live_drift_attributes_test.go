// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"testing"
	"time"

	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

// TestCollectLiveSnapshotPayloadCarriesSerialAndLineage proves the snapshot
// fact payload preserves the state identity the host drift loader reads:
// eshu's drift evidence query selects payload->>'lineage' and
// payload->>'serial' from terraform_state_snapshot facts, so a snapshot
// without them dead-letters every config_state_drift intent for its scope.
func TestCollectLiveSnapshotPayloadCarriesSerialAndLineage(t *testing.T) {
	t.Parallel()

	live, err := ParseLiveConfig(opsQALiveConfig())
	if err != nil {
		t.Fatalf("ParseLiveConfig error = %v", err)
	}
	state := `{"serial": 11, "lineage": "lin-payload", "resources": [{"mode": "managed", "type": "aws_s3_bucket", "name": "one", "instances": [{}]}]}`
	reader := &fakeLiveReader{
		objects: map[string][]byte{"bg-ops-qa-terraform-state/env/terraform.tfstate": []byte(state)},
		lists:   map[string][]string{"bg-ops-qa-terraform-state/": {"env/terraform.tfstate"}},
	}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, reader, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	var snapshot *sdk.Fact
	for i, fact := range result.Facts {
		if fact.Kind == FactKindStateSnapshot {
			snapshot = &result.Facts[i]
		}
	}
	if snapshot == nil {
		t.Fatalf("no %q fact in %+v", FactKindStateSnapshot, result.Facts)
	}
	if got := snapshot.Payload["lineage"]; got != "lin-payload" {
		t.Fatalf("snapshot payload lineage = %#v, want %q", got, "lin-payload")
	}
	serial, ok := snapshot.Payload["serial"]
	if !ok {
		t.Fatalf("snapshot payload has no serial key: %#v", snapshot.Payload)
	}
	if got, ok := serial.(int64); !ok || got != 11 {
		t.Fatalf("snapshot payload serial = %#v, want int64(11)", serial)
	}
}

// TestCollectLiveResourcePayloadCarriesAddress proves each resource fact
// carries the in-tree payload address key: the host drift loader scans
// payload->>'address' into a NOT NULL column, so a fact without it
// dead-letters its scope's config_state_drift intent.
func TestCollectLiveResourcePayloadCarriesAddress(t *testing.T) {
	t.Parallel()

	live, err := ParseLiveConfig(opsQALiveConfig())
	if err != nil {
		t.Fatalf("ParseLiveConfig error = %v", err)
	}
	state := `{"serial": 11, "lineage": "lin-payload", "resources": [{"mode": "managed", "type": "aws_s3_bucket", "name": "one", "instances": [{}]}]}`
	reader := &fakeLiveReader{
		objects: map[string][]byte{"bg-ops-qa-terraform-state/env/terraform.tfstate": []byte(state)},
		lists:   map[string][]string{"bg-ops-qa-terraform-state/": {"env/terraform.tfstate"}},
	}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, reader, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	found := false
	for _, fact := range result.Facts {
		if fact.Kind != FactKindStateResource {
			continue
		}
		found = true
		if got := fact.Payload["address"]; got != "aws_s3_bucket.one" {
			t.Fatalf("resource payload address = %#v, want %q", got, "aws_s3_bucket.one")
		}
	}
	if !found {
		t.Fatalf("no %q fact in %+v", FactKindStateResource, result.Facts)
	}
}

// TestCollectLiveEmitsAllowlistedAttributesOnly proves bucket E's shape:
// the snapshot's aws_s3_bucket resource carries acl plus the SSE
// sse_algorithm leaf re-nested as pure maps, and nothing else — a bucket
// name, provider address, or any non-allowlisted key must not ride along.
func TestCollectLiveEmitsAllowlistedAttributesOnly(t *testing.T) {
	t.Parallel()

	live, err := ParseLiveConfig(opsQALiveConfig())
	if err != nil {
		t.Fatalf("ParseLiveConfig error = %v", err)
	}
	state := `{"serial": 11, "lineage": "lin-attrs", "resources": [{"mode": "managed", "type": "aws_s3_bucket", "name": "logs", "provider": "provider[\"registry.terraform.io/hashicorp/aws\"]", "instances": [{"attributes": {"acl": "private", "bucket": "eshu-drift-e-logs", "server_side_encryption_configuration": [{"rule": [{"apply_server_side_encryption_by_default": [{"sse_algorithm": "aws:kms"}]}]}]}}]}]}`
	reader := &fakeLiveReader{
		objects: map[string][]byte{"bg-ops-qa-terraform-state/env/terraform.tfstate": []byte(state)},
		lists:   map[string][]string{"bg-ops-qa-terraform-state/": {"env/terraform.tfstate"}},
	}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, reader, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	var resource *sdk.Fact
	for i, fact := range result.Facts {
		if fact.Kind == FactKindStateResource {
			resource = &result.Facts[i]
		}
	}
	if resource == nil {
		t.Fatalf("no %q fact in %+v", FactKindStateResource, result.Facts)
	}
	attrs, ok := resource.Payload["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("resource payload attributes = %#v, want nested map", resource.Payload["attributes"])
	}
	if got := attrs["acl"]; got != "private" {
		t.Fatalf("attributes[acl] = %#v, want %q", got, "private")
	}
	sse, ok := attrs["server_side_encryption_configuration"].(map[string]any)
	if !ok {
		t.Fatalf("attributes SSE = %#v, want nested map", attrs["server_side_encryption_configuration"])
	}
	rule, ok := sse["rule"].(map[string]any)
	if !ok {
		t.Fatalf("SSE rule = %#v, want nested map", sse["rule"])
	}
	byDefault, ok := rule["apply_server_side_encryption_by_default"].(map[string]any)
	if !ok {
		t.Fatalf("SSE apply_server_side_encryption_by_default = %#v, want nested map", rule["apply_server_side_encryption_by_default"])
	}
	if got := byDefault["sse_algorithm"]; got != "aws:kms" {
		t.Fatalf("sse_algorithm = %#v, want %q", got, "aws:kms")
	}
	if len(attrs) != 2 {
		t.Fatalf("attributes keys = %v, want exactly acl + server_side_encryption_configuration", attrs)
	}
	if _, ok := attrs["bucket"]; ok {
		t.Fatalf("attributes carry non-allowlisted bucket = %v", attrs["bucket"])
	}
}

// TestCollectLiveOmitsDeniedAndUnlistedAttributes proves the deny-wins and
// fail-closed rules: aws_iam_policy carries no attributes despite being
// allowlisted host-side, and unlisted types emit join keys only. A resource
// whose allowlisted leaves are all absent emits no attributes key at all.
func TestCollectLiveOmitsDeniedAndUnlistedAttributes(t *testing.T) {
	t.Parallel()

	live, err := ParseLiveConfig(opsQALiveConfig())
	if err != nil {
		t.Fatalf("ParseLiveConfig error = %v", err)
	}
	state := `{"serial": 12, "lineage": "lin-deny", "resources": [
		{"mode": "managed", "type": "aws_iam_policy", "name": "p", "instances": [{"attributes": {"policy": "{\"secret\":true}"}}]},
		{"mode": "managed", "type": "aws_made_up_thing", "name": "x", "instances": [{"attributes": {"anything": 1}}]},
		{"mode": "managed", "type": "aws_s3_bucket", "name": "bare", "instances": [{"attributes": {"bucket": "no-allowlisted-leaf"}}]}
	]}`
	reader := &fakeLiveReader{
		objects: map[string][]byte{"bg-ops-qa-terraform-state/env/terraform.tfstate": []byte(state)},
		lists:   map[string][]string{"bg-ops-qa-terraform-state/": {"env/terraform.tfstate"}},
	}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, reader, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	if len(result.Facts) != 4 {
		t.Fatalf("facts = %d, want 3 resources plus one snapshot", len(result.Facts))
	}
	for _, fact := range result.Facts {
		if fact.Kind != FactKindStateResource {
			continue
		}
		if _, ok := fact.Payload["attributes"]; ok {
			t.Fatalf("resource %q carries attributes = %#v, want join keys only", fact.SourceRef.RecordID, fact.Payload["attributes"])
		}
	}
}

// TestDigestChangesWhenAttributesChange proves the freshness digest covers
// the emitted attributes: two reports identical except for an allowlisted
// value must not collapse to ResultUnchanged, or drift would go stale.
func TestDigestChangesWhenAttributesChange(t *testing.T) {
	t.Parallel()

	base := Report{
		Source: "s3://b/k",
		Records: []Record{{
			ID:     "aws_s3_bucket.logs",
			Name:   "aws_s3_bucket",
			Detail: "managed",
			Attributes: map[string]any{
				"acl": "private",
			},
		}},
	}
	changed := Report{
		Source: "s3://b/k",
		Records: []Record{{
			ID:     "aws_s3_bucket.logs",
			Name:   "aws_s3_bucket",
			Detail: "managed",
			Attributes: map[string]any{
				"acl": "public-read",
			},
		}},
	}
	if Digest(base) == Digest(changed) {
		t.Fatal("Digest unchanged after attribute change: stale evidence would persist")
	}
}
