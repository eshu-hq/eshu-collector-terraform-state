// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"testing"

	"github.com/eshu-hq/eshu-sdk/collector/conformance"
)

// coreTestAuthorization returns the trusted host-granted authorization
// snapshot for this collector's ten core-owned kinds, plus the reserved-kind
// inventory the tests play host with. The seven bare terraform_state_* kinds
// carry no dot namespace, so the harness only accepts them with exact
// CoreFacts coverage for this producer, version, collector kind, and schema
// (the #8851 host-granted-kind path, now in the relocated SDK): every
// conformance.Run in this repo must pass both values. PayloadSchemaRef stays
// empty so no fixture-pack schema coupling is required; the port that emits
// real payloads declares refs then.
func coreTestAuthorization(t *testing.T) (*conformance.CoreFactContext, []string) {
	t.Helper()

	kinds := []string{
		FactKindStateSnapshot,
		FactKindStateResource,
		FactKindStateOutput,
		FactKindStateModule,
		FactKindStateProviderBinding,
		FactKindStateTagObservation,
		FactKindStateWarning,
	}
	facts := make([]conformance.CoreFactAuthorization, 0, len(kinds))
	for _, kind := range kinds {
		facts = append(facts, conformance.CoreFactAuthorization{
			Kind: kind,
			Emissions: []conformance.CoreFactEmission{
				{CollectorKind: CollectorKind, SchemaVersions: []string{"1.0.0"}},
			},
			ReducerPhases: []string{"source_evidence_only:no_graph_truth"},
		})
	}
	reserved := make([]string, len(kinds))
	copy(reserved, kinds)
	return &conformance.CoreFactContext{
		ComponentID:      ComponentID,
		ComponentVersion: "0.1.0",
		Facts:            facts,
	}, reserved
}
