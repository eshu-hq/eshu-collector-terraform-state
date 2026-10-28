// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

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
		WorkItemID:    "work-item-9",
		FencingToken:  "fence-9",
		ConfigHandle:  "config-9",
		SourceRunID:   "source-run-9",
		Attempt:       1,
		Deadline:      time.Now().Add(time.Hour).UTC(),
	}
}

// TestCollectLiveMergesTwoStates proves two exact seeds collect and merge:
// complete state, credential-free s3:// source URIs, and the claim echoed
// back. The byte-identical states share one address, which collapses to a
// single resource fact; each state keeps its own snapshot.
func TestCollectLiveMergesTwoStates(t *testing.T) {
	t.Parallel()

	state := `{"serial": 3, "lineage": "lin-merge", "resources": [{"mode": "managed", "type": "aws_s3_bucket", "name": "one", "instances": [{}]}]}`
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
	if want := "lineage=lin-merge serial=3"; result.Generation.FreshnessHint != want {
		t.Fatalf("FreshnessHint = %q, want %q", result.Generation.FreshnessHint, want)
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
	one := `{"serial": 7, "lineage": "lin-one", "resources": [` + shared + `, {"mode": "managed", "type": "aws_s3_bucket", "name": "one", "instances": [{}]}]}`
	two := `{"serial": 9, "lineage": "lin-two", "resources": [` + shared + `, {"mode": "managed", "type": "aws_s3_bucket", "name": "two", "instances": [{}]}]}`
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
	if want := "lineage=lin-one serial=7 lineage=lin-two serial=9"; result.Generation.FreshnessHint != want {
		t.Fatalf("FreshnessHint = %q, want %q", result.Generation.FreshnessHint, want)
	}
	if len(result.Statuses) != 1 || result.Statuses[0].Class != sdk.StatusComplete {
		t.Fatalf("Statuses = %+v, want one complete status after identical collapse", result.Statuses)
	}
	if result.Statuses[0].FactCount != len(result.Facts) {
		t.Fatalf("status FactCount = %d, want %d after collapse", result.Statuses[0].FactCount, len(result.Facts))
	}
	assertHostAccepts(t, result)
}

// TestCollectLiveConflictAcrossStatesWarnsInsteadOfFailing proves a genuine
// conflict (same address, different payload across states) degrades to
// partial with a warning naming the dropped source instead of failing the
// whole claim terminal and darkening every state.
func TestCollectLiveConflictAcrossStatesWarnsInsteadOfFailing(t *testing.T) {
	t.Parallel()

	one := `{"serial": 7, "lineage": "lin-one", "resources": [{"mode": "managed", "type": "aws_vpc", "name": "default", "instances": [{}]}]}`
	two := `{"serial": 9, "lineage": "lin-two", "resources": [{"mode": "data", "type": "aws_vpc", "name": "default", "instances": [{}]}]}`
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
	if want := "lineage=lin-one serial=7 lineage=lin-two serial=9"; result.Generation.FreshnessHint != want {
		t.Fatalf("FreshnessHint = %q, want %q", result.Generation.FreshnessHint, want)
	}
	if len(result.Statuses) != 1 || result.Statuses[0].Class != sdk.StatusWarning || !result.Statuses[0].Partial {
		t.Fatalf("Statuses = %+v, want one partial warning status after conflict", result.Statuses)
	}
	if result.Statuses[0].FactCount != len(result.Facts) || result.Statuses[0].WarningCount != warnings {
		t.Fatalf("status counts = %+v, want true fact and warning counts", result.Statuses[0])
	}
	assertHostAccepts(t, result)
}

// assertHostAccepts runs the merged result through the host's own SDK
// validation: the contract gate the extension host enforces before commit
// (terminal/retryable must be factless with a failure status, partial
// needs a partial/warning status, no conflicting duplicate stable keys).
func assertHostAccepts(t *testing.T, result sdk.Result) {
	t.Helper()
	if _, err := sdk.NewValidator(Contract()).ValidateResult(result); err != nil {
		t.Fatalf("host validation rejected merged result: %v", err)
	}
}

// TestCollectLiveTransientPlusCompleteStaysRetryable proves a mixed merge
// never demotes the retry signal: one transient source plus one healthy
// state returns a factless retryable result with the bounded-retry status,
// so the host releases the claim for retry instead of committing a partial
// claim and losing the retry.
func TestCollectLiveTransientPlusCompleteStaysRetryable(t *testing.T) {
	t.Parallel()

	good := `{"serial": 3, "lineage": "lin-good", "resources": [{"mode": "managed", "type": "aws_s3_bucket", "name": "one", "instances": [{}]}]}`
	live := LiveConfig{
		Seeds: []DiscoverySeed{
			{Kind: BackendS3, TargetScopeID: "s", Bucket: "b", Key: "bad/terraform.tfstate", Region: "us-east-1"},
			{Kind: BackendS3, TargetScopeID: "s", Bucket: "b", Key: "good/terraform.tfstate", Region: "us-east-1"},
		},
		TargetScopes: []TargetScope{{TargetScopeID: "s"}},
	}
	reader := &fakeLiveReader{
		objects:   map[string][]byte{"b/good/terraform.tfstate": []byte(good)},
		transient: map[string]bool{"b/bad/terraform.tfstate": true},
	}
	result, err := CollectLive(context.Background(), liveTestClaim(), live, reader, CollectOptions{ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CollectLive error = %v", err)
	}
	if result.State != sdk.ResultRetryable {
		t.Fatalf("State = %q, want retryable so the host retries the claim", result.State)
	}
	if len(result.Facts) != 0 {
		t.Fatalf("facts = %d, want factless so the host accepts the retryable", len(result.Facts))
	}
	if len(result.Statuses) != 1 || result.Statuses[0].Class != sdk.StatusFailure || result.Statuses[0].RetryAfterSeconds == 0 {
		t.Fatalf("Statuses = %+v, want one bounded-retry failure status", result.Statuses)
	}
	assertHostAccepts(t, result)
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
	if want := "states=0"; result.Generation.FreshnessHint != want {
		t.Fatalf("FreshnessHint = %q, want %q for the authoritative empty listing", result.Generation.FreshnessHint, want)
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
