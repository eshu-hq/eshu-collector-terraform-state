// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"testing"
	"time"

	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

// TestMergeLiveResultsTerminalPlusCompleteStaysFactless proves a terminal
// candidate is never merged with complete facts: the host rejects facts on
// terminal results, so the merge returns the factless winner with its
// failure status intact instead of an invalid mixed result.
func TestMergeLiveResultsTerminalPlusCompleteStaysFactless(t *testing.T) {
	t.Parallel()

	claim := liveTestClaim()
	observedAt := time.Now().UTC()
	complete, err := Collect(claim, Report{GeneratedAt: observedAt, Source: "s3://b/k", Serial: 1, Lineage: "lin-1", Records: []Record{{ID: "a.b", Name: "b", Detail: "managed"}}}, CollectOptions{ObservedAt: observedAt, SourceURI: "s3://b/k"})
	if err != nil {
		t.Fatalf("Collect error = %v", err)
	}
	terminal := terminalResult(claim, observedAt, "over-budget")
	merged := mergeLiveResults([]sdk.Result{complete, terminal}, claim, observedAt)
	if merged.State != sdk.ResultTerminal {
		t.Fatalf("State = %q, want terminal", merged.State)
	}
	if len(merged.Facts) != 0 {
		t.Fatalf("facts = %d, want factless terminal", len(merged.Facts))
	}
	if len(merged.Statuses) != 1 || merged.Statuses[0].Class != sdk.StatusFailure || merged.Statuses[0].FailureClass != "over-budget" {
		t.Fatalf("Statuses = %+v, want the preserved budget failure", merged.Statuses)
	}
	assertHostAccepts(t, merged)
}

// TestMergeLiveResultsPartialCollapseKeepsPartial proves identical
// collapse under a partial merge keeps the partial signal: a single
// partial warning status with true counts, which the host accepts.
func TestMergeLiveResultsPartialCollapseKeepsPartial(t *testing.T) {
	t.Parallel()

	claim := liveTestClaim()
	observedAt := time.Now().UTC()
	mkFact := func(uri string) sdk.Fact {
		return sdk.Fact{
			Kind: FactKindStateResource, SchemaVersion: "1.0.0", StableKey: "record:aws_vpc.default",
			SourceConfidence: sdk.SourceConfidenceObserved, ObservedAt: observedAt,
			SourceRef: sdk.SourceRef{SourceSystem: SourceSystem, ScopeID: claim.Scope.ID, GenerationID: claim.GenerationID, FactKey: "record:aws_vpc.default", URI: uri, RecordID: "aws_vpc.default"},
			Payload:   map[string]any{"detail": "data", "id": "aws_vpc.default", "name": "aws_vpc"},
		}
	}
	in := []sdk.Fact{mkFact("s3://b/one"), mkFact("s3://b/two")}
	statuses := []sdk.Status{{Class: sdk.StatusComplete, FactCount: 1}, {Class: sdk.StatusWarning, Partial: true, WarningCount: 1, FactCount: 1}}
	outFacts, outStatuses, outState := dedupeMergedFacts(in, statuses, sdk.ResultPartial, claim, observedAt)
	if outState != sdk.ResultPartial {
		t.Fatalf("State = %q, want partial preserved", outState)
	}
	if len(outFacts) != 1 {
		t.Fatalf("facts = %d, want the collapsed single fact", len(outFacts))
	}
	if len(outStatuses) != 1 || outStatuses[0].Class != sdk.StatusWarning || !outStatuses[0].Partial {
		t.Fatalf("Statuses = %+v, want one partial warning status", outStatuses)
	}
	if outStatuses[0].FactCount != 1 {
		t.Fatalf("status FactCount = %d, want true count 1", outStatuses[0].FactCount)
	}
	assertHostAccepts(t, sdk.Result{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1, State: outState, Claim: claim,
		Generation: sdk.Generation{ID: claim.GenerationID, ObservedAt: observedAt, FreshnessHint: "lineage=lin-1 serial=1"},
		Facts:      outFacts, Statuses: outStatuses,
	})
}
