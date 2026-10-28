// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"strings"
	"testing"
)

func opsQALiveConfig() map[string]any {
	return map[string]any{
		"discovery": map[string]any{
			"backend_filters": []any{
				map[string]any{
					"target_scope_id": "ops-qa-aws",
					"backend_kind":    "s3",
					"bucket":          "bg-ops-qa-terraform-state",
					"region":          "us-east-1",
				},
			},
		},
		"target_scopes": []any{
			map[string]any{
				"target_scope_id":  "ops-qa-aws",
				"provider":         "aws",
				"deployment_mode":  "account_local",
				"allowed_regions":  []any{"us-east-1"},
				"allowed_backends": []any{"s3"},
			},
		},
	}
}

// TestParseLiveConfigOpsQaShape proves the host activation shape parses:
// one bucket filter plus one target scope, with normalization applied.
func TestParseLiveConfigOpsQaShape(t *testing.T) {
	t.Parallel()

	live, err := ParseLiveConfig(opsQALiveConfig())
	if err != nil {
		t.Fatalf("ParseLiveConfig(ops-qa) error = %v", err)
	}
	if len(live.BackendFilters) != 1 {
		t.Fatalf("BackendFilters = %+v, want one filter", live.BackendFilters)
	}
	filter := live.BackendFilters[0]
	if filter.Bucket != "bg-ops-qa-terraform-state" || filter.Region != "us-east-1" || filter.BackendKind != BackendS3 {
		t.Fatalf("filter = %+v, want the ops-qa s3 filter", filter)
	}
	if len(live.TargetScopes) != 1 || live.TargetScopes[0].TargetScopeID != "ops-qa-aws" {
		t.Fatalf("TargetScopes = %+v, want ops-qa-aws", live.TargetScopes)
	}
	if !HasLiveDiscovery(opsQALiveConfig()) {
		t.Fatal("HasLiveDiscovery(ops-qa) = false, want true")
	}
	if HasLiveDiscovery(map[string]any{"source": map[string]any{"input": "x"}}) {
		t.Fatal("HasLiveDiscovery(file) = true, want false")
	}
}

// TestParseLiveConfigMalformedFailsClosed proves wrong-typed discovery and
// scope blocks error instead of silently selecting defaults.
func TestParseLiveConfigMalformedFailsClosed(t *testing.T) {
	t.Parallel()

	cases := map[string]map[string]any{
		"discovery string": {
			"discovery": "s3",
		},
		"filters non-array": {
			"discovery": map[string]any{"backend_filters": "s3"},
		},
		"filter non-object": {
			"discovery": map[string]any{"backend_filters": []any{"s3"}},
		},
		"blank scope id": {
			"target_scopes": []any{map[string]any{"provider": "aws"}},
		},
		"scopes non-array": {
			"target_scopes": "aws",
		},
	}
	for name, config := range cases {
		if _, err := ParseLiveConfig(config); err == nil {
			t.Errorf("ParseLiveConfig(%s) error = nil, want failure", name)
		}
	}
}

// TestResolveLiveCandidatesSeedsAndKeyedFilters proves exact seeds and
// keyed filters resolve to deduplicated exact candidates.
func TestResolveLiveCandidatesSeedsAndKeyedFilters(t *testing.T) {
	t.Parallel()

	live := LiveConfig{
		Seeds: []DiscoverySeed{
			{Kind: BackendS3, TargetScopeID: "ops-qa-aws", Bucket: "bg-ops-qa-terraform-state", Key: "vpc/terraform.tfstate", Region: "us-east-1"},
		},
		BackendFilters: []DiscoveryBackendFilter{
			{TargetScopeID: "ops-qa-aws", BackendKind: BackendS3, Bucket: "bg-ops-qa-terraform-state", Key: "vpc/terraform.tfstate", Region: "us-east-1"},
			{TargetScopeID: "ops-qa-aws", BackendKind: BackendS3, Bucket: "bg-ops-qa-terraform-state", Key: "app/terraform.tfstate", Region: "us-east-1"},
		},
		TargetScopes: []TargetScope{{TargetScopeID: "ops-qa-aws", AllowedRegions: []string{"us-east-1"}, AllowedBackends: []string{"s3"}}},
	}
	exact, listings, err := ResolveLiveCandidates(live)
	if err != nil {
		t.Fatalf("ResolveLiveCandidates error = %v", err)
	}
	if len(exact) != 2 || len(listings) != 0 {
		t.Fatalf("exact = %d, listings = %d; want 2 exact and no listings", len(exact), len(listings))
	}
	for _, candidate := range exact {
		if err := candidate.Validate(); err != nil {
			t.Fatalf("candidate.Validate() error = %v", err)
		}
	}
}

// TestResolveLiveCandidatesBucketFilterLists proves a bucket-only filter
// becomes a listing (the ops-qa activation shape) rather than an error.
func TestResolveLiveCandidatesBucketFilterLists(t *testing.T) {
	t.Parallel()

	live, err := ParseLiveConfig(opsQALiveConfig())
	if err != nil {
		t.Fatalf("ParseLiveConfig error = %v", err)
	}
	exact, listings, err := ResolveLiveCandidates(live)
	if err != nil {
		t.Fatalf("ResolveLiveCandidates error = %v", err)
	}
	if len(exact) != 0 || len(listings) != 1 {
		t.Fatalf("exact = %d, listings = %+v; want no exact and one listing", len(exact), listings)
	}
}

// TestResolveLiveCandidatesFailsClosed enumerates the discovery shapes
// that must error: bucket-less filters, local filters, region-less
// filters, unknown or out-of-allowlist scope bindings, and empty configs.
func TestResolveLiveCandidatesFailsClosed(t *testing.T) {
	t.Parallel()

	scopes := []TargetScope{{TargetScopeID: "ops-qa-aws", AllowedRegions: []string{"us-east-1"}, AllowedBackends: []string{"s3"}}}
	cases := map[string]LiveConfig{
		"empty": {},
		"bucket-less filter": {
			BackendFilters: []DiscoveryBackendFilter{{BackendKind: BackendS3, Region: "us-east-1"}},
		},
		"region-less filter": {
			BackendFilters: []DiscoveryBackendFilter{{BackendKind: BackendS3, Bucket: "b"}},
		},
		"local filter": {
			BackendFilters: []DiscoveryBackendFilter{{BackendKind: BackendLocal, Bucket: "b", Region: "us-east-1"}},
		},
		"unknown scope": {
			Seeds:        []DiscoverySeed{{Kind: BackendS3, TargetScopeID: "nope", Bucket: "b", Key: "k", Region: "us-east-1"}},
			TargetScopes: scopes,
		},
		"outside allowlist": {
			Seeds:        []DiscoverySeed{{Kind: BackendS3, TargetScopeID: "ops-qa-aws", Bucket: "b", Key: "k", Region: "eu-west-1"}},
			TargetScopes: scopes,
		},
		"blank seed kind": {
			Seeds: []DiscoverySeed{{Bucket: "b", Key: "k", Region: "us-east-1"}},
		},
	}
	for name, live := range cases {
		if _, _, err := ResolveLiveCandidates(live); err == nil {
			t.Errorf("ResolveLiveCandidates(%s) error = nil, want failure", name)
		}
	}
}

// TestResolveTargetScopeInferenceFailsClosed proves blank scope bindings
// infer across allowlists, failing closed on ambiguity or no match.
func TestResolveTargetScopeInferenceFailsClosed(t *testing.T) {
	t.Parallel()

	candidate := DiscoveryCandidate{
		State:  StateKey{BackendKind: BackendS3, Locator: "s3://b/k"},
		Region: "us-east-1",
	}
	single := []TargetScope{{TargetScopeID: "a", AllowedRegions: []string{"us-east-1"}, AllowedBackends: []string{"s3"}}}
	if scopeID, err := resolveTargetScopeID(single, candidate); err != nil || scopeID != "a" {
		t.Fatalf("resolveTargetScopeID(single) = (%q, %v), want (a, nil)", scopeID, err)
	}
	ambiguous := append(append([]TargetScope{}, single...), TargetScope{TargetScopeID: "b"})
	if _, err := resolveTargetScopeID(ambiguous, candidate); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("resolveTargetScopeID(ambiguous) error = %v, want ambiguity failure", err)
	}
	nomatch := []TargetScope{{TargetScopeID: "a", AllowedRegions: []string{"eu-west-1"}, AllowedBackends: []string{"s3"}}}
	if _, err := resolveTargetScopeID(nomatch, candidate); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("resolveTargetScopeID(nomatch) error = %v, want no-match failure", err)
	}
}
