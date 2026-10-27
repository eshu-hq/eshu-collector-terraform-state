// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/eshu-hq/eshu-sdk/collector"
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

const liveStateFixture = `{
	"resources": [
		{"mode": "managed", "type": "aws_s3_bucket", "name": "state", "instances": [{"attributes": {"bucket": "bg-ops-qa-terraform-state", "acl": "private", "secret_token": "hunter2"}}]},
		{"mode": "data", "type": "aws_ami", "name": "base", "module": "module.vpc", "instances": []},
		{"type": "", "name": "broken", "instances": []}
	],
	"outputs": {"db_password": {"value": "hunter3", "sensitive": true}}
}`

// TestReportForStateEmitsIdentityOnly proves the state projection carries
// addresses, types, and modes while no attribute or output value (the
// hunter2/hunter3 markers) reaches any record payload.
func TestReportForStateEmitsIdentityOnly(t *testing.T) {
	t.Parallel()

	report, err := ReportForState("s3://b/k", []byte(liveStateFixture))
	if err != nil {
		t.Fatalf("ReportForState error = %v", err)
	}
	if len(report.Records) != 2 {
		t.Fatalf("records = %+v, want the two well-formed resources", report.Records)
	}
	encoded, err := json.Marshal(report.Records)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}
	for _, secret := range []string{"hunter2", "hunter3", "db_password", "secret_token", "private"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("record payloads leak %q: %s", secret, encoded)
		}
	}
	byID := map[string]Record{}
	for _, record := range report.Records {
		byID[record.ID] = record
	}
	managed := byID["aws_s3_bucket.state"]
	if managed.Name != "aws_s3_bucket" || managed.Detail != "managed" {
		t.Fatalf("managed record = %+v, want type name and managed detail", managed)
	}
	data := byID["module.vpc.aws_ami.base"]
	if data.Name != "aws_ami" || data.Detail != "data" {
		t.Fatalf("data record = %+v, want module address and data detail", data)
	}
	if _, err := ReportForState("s3://b/k", []byte("{nope")); err == nil {
		t.Fatal("ReportForState(invalid JSON) error = nil, want failure")
	}
}

// fakeLiveReader is an in-memory SourceReader for claim tests.
type fakeLiveReader struct {
	objects   map[string][]byte
	missing   map[string]bool
	tooLarge  map[string]bool
	transient map[string]bool
	lists     map[string][]string
	listErr   error
}

func (reader *fakeLiveReader) GetObject(_ context.Context, bucket, key, _ string) (StateObject, error) {
	identity := bucket + "/" + key
	switch {
	case reader.transient[identity]:
		return StateObject{}, ErrStateTransient
	case reader.missing[identity]:
		return StateObject{}, ErrStateMissing
	case reader.tooLarge[identity]:
		return StateObject{}, ErrStateTooLarge
	}
	body, ok := reader.objects[identity]
	if !ok {
		return StateObject{}, ErrStateMissing
	}
	return StateObject{Body: body}, nil
}

func (reader *fakeLiveReader) ListKeys(_ context.Context, bucket, _region, prefix string) ([]string, error) {
	if reader.listErr != nil {
		return nil, reader.listErr
	}
	return reader.lists[bucket+"/"+prefix], nil
}

func liveTestClaim() sdk.Claim {
	return sdk.Claim{
		ComponentID:   ComponentID,
		InstanceID:    "terraform-state-primary",
		CollectorKind: CollectorKind,
		SourceSystem:  SourceSystem,
		Scope:         sdk.Scope{ID: "component:terraform-state-primary", Kind: "component"},
		GenerationID:  "generation-9",
	}
}

// TestCollectLiveMergesTwoStates proves two exact seeds collect and merge:
// complete state, credential-free s3:// source URIs, and the claim echoed
// back. The byte-identical states share one address, which collapses to a
// single resource fact; each state keeps its own snapshot.
func TestCollectLiveMergesTwoStates(t *testing.T) {
	t.Parallel()

	state := `{"resources": [{"mode": "managed", "type": "aws_s3_bucket", "name": "one", "instances": [{}]}]}`
	live := LiveConfig{
		Seeds: []DiscoverySeed{
			{Kind: BackendS3, TargetScopeID: "s", Bucket: "b", Key: "one/terraform.tfstate", Region: "us-east-1"},
			{Kind: BackendS3, TargetScopeID: "s", Bucket: "b", Key: "two/terraform.tfstate", Region: "us-east-1"},
		},
		TargetScopes: []TargetScope{{TargetScopeID: "s"}},
	}
	reader := &fakeLiveReader{objects: map[string][]byte{
		"b/one/terraform.tfstate": []byte(state),
		"b/two/terraform.tfstate": []byte(state),
	}}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, reader, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	if result.State != sdk.ResultComplete {
		t.Fatalf("State = %q, want complete", result.State)
	}
	if result.Claim.GenerationID != "generation-9" {
		t.Fatalf("Claim = %+v, want the claim echoed", result.Claim)
	}
	resources, snapshots := 0, 0
	for _, fact := range result.Facts {
		if !strings.HasPrefix(fact.SourceRef.URI, "s3://b/") {
			t.Fatalf("SourceRef.URI = %q, want credential-free s3:// URI", fact.SourceRef.URI)
		}
		switch fact.Kind {
		case FactKindStateResource:
			resources++
		case FactKindStateSnapshot:
			snapshots++
		}
	}
	// The two states are byte-identical, so their shared address collapses
	// to one resource fact (the host rejects any repeated stable key);
	// each state keeps its own snapshot because snapshot digests bind the
	// source locator.
	if resources != 1 || snapshots != 2 {
		t.Fatalf("resources = %d, snapshots = %d; want one collapsed resource and one snapshot per state", resources, snapshots)
	}
}

// TestCollectLiveCollapsesIdenticalAddressAcrossStates proves the ops-qa
// failure mode stays host-valid: two states sharing one bare address with
// byte-identical payloads emit that fact once, not twice. The host rejects
// any repeated stable key as invalid_result (eshu-hq/eshu#8868), so the
// merged result must carry each (kind, stable key) exactly once.
func TestCollectLiveCollapsesIdenticalAddressAcrossStates(t *testing.T) {
	t.Parallel()

	shared := `{"mode": "data", "type": "aws_vpc", "name": "default", "instances": [{}]}`
	one := `{"resources": [` + shared + `, {"mode": "managed", "type": "aws_s3_bucket", "name": "one", "instances": [{}]}]}`
	two := `{"resources": [` + shared + `, {"mode": "managed", "type": "aws_s3_bucket", "name": "two", "instances": [{}]}]}`
	live := LiveConfig{
		Seeds: []DiscoverySeed{
			{Kind: BackendS3, TargetScopeID: "s", Bucket: "b", Key: "one/terraform.tfstate", Region: "us-east-1"},
			{Kind: BackendS3, TargetScopeID: "s", Bucket: "b", Key: "two/terraform.tfstate", Region: "us-east-1"},
		},
		TargetScopes: []TargetScope{{TargetScopeID: "s"}},
	}
	reader := &fakeLiveReader{objects: map[string][]byte{
		"b/one/terraform.tfstate": []byte(one),
		"b/two/terraform.tfstate": []byte(two),
	}}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, reader, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	if result.State != sdk.ResultComplete {
		t.Fatalf("State = %q, want complete for identical cross-state facts", result.State)
	}
	seen := map[string]int{}
	for _, fact := range result.Facts {
		if fact.Kind == FactKindStateResource {
			seen[fact.StableKey]++
		}
	}
	for key, count := range seen {
		if count != 1 {
			t.Fatalf("StableKey %q emitted %d times, want exactly once", key, count)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("distinct resource keys = %d, want shared plus two unique", len(seen))
	}
}

// TestCollectLiveConflictAcrossStatesWarnsInsteadOfFailing proves a genuine
// conflict (same address, different payload across states) degrades to
// partial with a warning naming the dropped source instead of failing the
// whole claim terminal and darkening every state.
func TestCollectLiveConflictAcrossStatesWarnsInsteadOfFailing(t *testing.T) {
	t.Parallel()

	one := `{"resources": [{"mode": "managed", "type": "aws_vpc", "name": "default", "instances": [{}]}]}`
	two := `{"resources": [{"mode": "data", "type": "aws_vpc", "name": "default", "instances": [{}]}]}`
	live := LiveConfig{
		Seeds: []DiscoverySeed{
			{Kind: BackendS3, TargetScopeID: "s", Bucket: "b", Key: "one/terraform.tfstate", Region: "us-east-1"},
			{Kind: BackendS3, TargetScopeID: "s", Bucket: "b", Key: "two/terraform.tfstate", Region: "us-east-1"},
		},
		TargetScopes: []TargetScope{{TargetScopeID: "s"}},
	}
	reader := &fakeLiveReader{objects: map[string][]byte{
		"b/one/terraform.tfstate": []byte(one),
		"b/two/terraform.tfstate": []byte(two),
	}}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, reader, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	if result.State != sdk.ResultPartial {
		t.Fatalf("State = %q, want partial for a conflicting cross-state address", result.State)
	}
	resources, warnings := 0, 0
	for _, fact := range result.Facts {
		switch fact.Kind {
		case FactKindStateResource:
			resources++
			if fact.StableKey != "record:aws_vpc.default" {
				t.Fatalf("resource StableKey = %q, want the surviving shared address", fact.StableKey)
			}
		case FactKindStateWarning:
			warnings++
		}
	}
	if resources != 1 {
		t.Fatalf("resources = %d, want exactly the surviving shared address", resources)
	}
	if warnings == 0 {
		t.Fatalf("warnings = 0, want a warning naming the dropped source")
	}
}

// TestCollectLiveMissingBecomesPartial proves a vanished object degrades
// the claim to partial with a warning instead of failing it.
func TestCollectLiveMissingBecomesPartial(t *testing.T) {
	t.Parallel()

	live := LiveConfig{
		Seeds: []DiscoverySeed{
			{Kind: BackendS3, TargetScopeID: "s", Bucket: "b", Key: "gone/terraform.tfstate", Region: "us-east-1"},
		},
		TargetScopes: []TargetScope{{TargetScopeID: "s"}},
	}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, &fakeLiveReader{}, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	if result.State != sdk.ResultPartial {
		t.Fatalf("State = %q, want partial for a missing object", result.State)
	}
	warnings := 0
	for _, fact := range result.Facts {
		if fact.Kind == FactKindStateWarning {
			warnings++
		}
	}
	if warnings != 1 {
		t.Fatalf("warning facts = %d, want one state-missing warning", warnings)
	}
}

// TestCollectLiveTransientBecomesRetryable proves a throttled source
// releases the claim for a bounded retry instead of failing it.
func TestCollectLiveTransientBecomesRetryable(t *testing.T) {
	t.Parallel()

	live := LiveConfig{
		Seeds: []DiscoverySeed{
			{Kind: BackendS3, TargetScopeID: "s", Bucket: "b", Key: "hot/terraform.tfstate", Region: "us-east-1"},
		},
		TargetScopes: []TargetScope{{TargetScopeID: "s"}},
	}
	reader := &fakeLiveReader{transient: map[string]bool{"b/hot/terraform.tfstate": true}}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, reader, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	if result.State != sdk.ResultRetryable {
		t.Fatalf("State = %q, want retryable for a transient source", result.State)
	}
}

// TestCollectLiveBucketListingCollectsKeys proves the ops-qa shape
// end to end: a bucket filter lists keys and each key collects.
func TestCollectLiveBucketListingCollectsKeys(t *testing.T) {
	t.Parallel()

	live, err := ParseLiveConfig(opsQALiveConfig())
	if err != nil {
		t.Fatalf("ParseLiveConfig error = %v", err)
	}
	state := `{"resources": [{"mode": "managed", "type": "aws_s3_bucket", "name": "one", "instances": [{}]}]}`
	reader := &fakeLiveReader{
		objects: map[string][]byte{"bg-ops-qa-terraform-state/env/terraform.tfstate": []byte(state)},
		lists:   map[string][]string{"bg-ops-qa-terraform-state/": {"env/terraform.tfstate"}},
	}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, reader, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	if result.State != sdk.ResultComplete {
		t.Fatalf("State = %q, want complete", result.State)
	}
	if len(result.Facts) != 2 {
		t.Fatalf("facts = %d, want one resource plus one snapshot", len(result.Facts))
	}
}

// TestCollectLiveEmptyListingIsAuthoritativeEmpty proves a bucket with no
// keys emits an authoritative complete snapshot with zero records (retire
// stale evidence), distinct from an unchanged freshness proof.
func TestCollectLiveEmptyListingIsAuthoritativeEmpty(t *testing.T) {
	t.Parallel()

	live, err := ParseLiveConfig(opsQALiveConfig())
	if err != nil {
		t.Fatalf("ParseLiveConfig error = %v", err)
	}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, &fakeLiveReader{}, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	if result.State != sdk.ResultComplete {
		t.Fatalf("State = %q, want complete for an empty listing", result.State)
	}
	if len(result.Facts) != 1 || result.Facts[0].Kind != FactKindStateSnapshot {
		t.Fatalf("facts = %+v, want exactly the snapshot", result.Facts)
	}
}

// TestCollectLiveFailsClosedWithoutReaderOrDiscovery proves nil readers
// and sourceless configs error instead of emitting.
func TestCollectLiveFailsClosedWithoutReaderOrDiscovery(t *testing.T) {
	t.Parallel()

	live, err := ParseLiveConfig(opsQALiveConfig())
	if err != nil {
		t.Fatalf("ParseLiveConfig error = %v", err)
	}
	if _, err := CollectLive(context.Background(), liveTestClaim(), live, nil, CollectOptions{}); err == nil {
		t.Fatal("CollectLive(nil reader) error = nil, want failure")
	}
	if _, err := CollectLive(context.Background(), liveTestClaim(), LiveConfig{}, &fakeLiveReader{}, CollectOptions{}); err == nil {
		t.Fatal("CollectLive(empty config) error = nil, want failure")
	}
}
