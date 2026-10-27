// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"fmt"
	"strings"
)

// candidateFromSeed resolves one exact candidate from one operator seed. It
// ports the in-tree candidateFromSeed for the local and s3 families.
func candidateFromSeed(seed DiscoverySeed) (DiscoveryCandidate, error) {
	switch seed.Kind {
	case BackendLocal:
		if seed.Path == "" {
			return DiscoveryCandidate{}, fmt.Errorf("local path must not be blank")
		}
		if !strings.HasPrefix(seed.Path, "/") {
			return DiscoveryCandidate{}, fmt.Errorf("local path must be absolute")
		}
		if seed.VersionID != "" {
			return DiscoveryCandidate{}, fmt.Errorf("local version_id is unsupported")
		}
		return DiscoveryCandidate{
			State:         StateKey{BackendKind: BackendLocal, Locator: seed.Path},
			TargetScopeID: seed.TargetScopeID,
		}, nil
	case BackendS3:
		if seed.Bucket == "" {
			return DiscoveryCandidate{}, fmt.Errorf("s3 bucket must not be blank")
		}
		if seed.Key == "" {
			return DiscoveryCandidate{}, fmt.Errorf("s3 key must not be blank")
		}
		if strings.HasSuffix(seed.Key, "/") {
			return DiscoveryCandidate{}, fmt.Errorf("s3 key must name an exact object")
		}
		if seed.Region == "" {
			return DiscoveryCandidate{}, fmt.Errorf("s3 region must not be blank")
		}
		return DiscoveryCandidate{
			State: StateKey{
				BackendKind: BackendS3,
				Locator:     "s3://" + seed.Bucket + "/" + seed.Key,
				VersionID:   seed.VersionID,
			},
			TargetScopeID: seed.TargetScopeID,
			Region:        seed.Region,
		}, nil
	case "":
		return DiscoveryCandidate{}, fmt.Errorf("kind must not be blank")
	default:
		return DiscoveryCandidate{}, fmt.Errorf("kind %q is unsupported", seed.Kind)
	}
}

// resolveTargetScopeID binds one candidate to one configured target scope.
// It ports the matching semantics of the in-tree credentialsForCandidate
// minus credentials: an explicit target_scope_id must be known and its
// allowlists must admit the candidate; a blank one is inferred across
// scopes, failing closed on ambiguity or no match.
func resolveTargetScopeID(scopes []TargetScope, candidate DiscoveryCandidate) (string, error) {
	if err := candidate.Validate(); err != nil {
		return "", err
	}
	targetScopeID := strings.TrimSpace(candidate.TargetScopeID)
	if targetScopeID != "" {
		for _, scope := range scopes {
			if scope.TargetScopeID != targetScopeID {
				continue
			}
			if !targetScopeAllowsCandidate(scope, candidate) {
				return "", fmt.Errorf(
					"terraform state candidate is outside target_scope_id %q allowlist",
					targetScopeID,
				)
			}
			return targetScopeID, nil
		}
		return "", fmt.Errorf("terraform state target_scope_id %q is unknown", targetScopeID)
	}
	if len(scopes) == 0 {
		return "", nil
	}
	var matched *TargetScope
	for index := range scopes {
		if !targetScopeAllowsCandidate(scopes[index], candidate) {
			continue
		}
		if matched != nil {
			return "", fmt.Errorf(
				"terraform state candidate has ambiguous target_scope_id for backend %q region %q",
				candidate.State.BackendKind,
				candidate.Region,
			)
		}
		matched = &scopes[index]
	}
	if matched == nil {
		return "", fmt.Errorf(
			"terraform state candidate does not match any configured target_scope_id for backend %q region %q",
			candidate.State.BackendKind,
			candidate.Region,
		)
	}
	return matched.TargetScopeID, nil
}

// targetScopeAllowsCandidate ports the in-tree allowlist check: empty
// allowlists admit everything, otherwise backend kind and (for s3) region
// must be listed.
func targetScopeAllowsCandidate(scope TargetScope, candidate DiscoveryCandidate) bool {
	if !stringListAllows(scope.AllowedBackends, string(candidate.State.BackendKind)) {
		return false
	}
	if candidate.State.BackendKind == BackendS3 &&
		!stringListAllows(scope.AllowedRegions, candidate.Region) {
		return false
	}
	return true
}

func stringListAllows(values []string, want string) bool {
	if len(values) == 0 {
		return true
	}
	want = strings.ToLower(strings.TrimSpace(want))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == want {
			return true
		}
	}
	return false
}

// ResolveLiveCandidates turns parsed discovery into exact candidates plus
// the bucket listing filters that still need a ListKeys round trip. A
// keyed filter is an exact candidate; a bucket-only filter lists that
// bucket under its key prefix. A filter with no bucket names nothing this
// satellite can open, so it fails closed instead of silently matching
// everything.
func ResolveLiveCandidates(live LiveConfig) ([]DiscoveryCandidate, []DiscoveryBackendFilter, error) {
	candidates := []DiscoveryCandidate{}
	listings := []DiscoveryBackendFilter{}
	seen := map[string]struct{}{}
	add := func(candidate DiscoveryCandidate) error {
		if err := candidate.Validate(); err != nil {
			return err
		}
		scopeID, err := resolveTargetScopeID(live.TargetScopes, candidate)
		if err != nil {
			return err
		}
		candidate.TargetScopeID = scopeID
		key := string(candidate.State.BackendKind) + "\x00" + candidate.State.Locator + "\x00" + candidate.State.VersionID
		if _, ok := seen[key]; ok {
			return nil
		}
		seen[key] = struct{}{}
		candidates = append(candidates, candidate)
		return nil
	}
	for index, seed := range live.Seeds {
		candidate, err := candidateFromSeed(seed)
		if err != nil {
			return nil, nil, fmt.Errorf("terraform state seed %d: %w", index, err)
		}
		if err := add(candidate); err != nil {
			return nil, nil, fmt.Errorf("terraform state seed %d: %w", index, err)
		}
	}
	for index, filter := range live.BackendFilters {
		if filter.BackendKind != "" && filter.BackendKind != BackendS3 && filter.BackendKind != BackendLocal {
			return nil, nil, fmt.Errorf("terraform state backend filter %d: kind %q is unsupported", index, filter.BackendKind)
		}
		if filter.Bucket == "" {
			return nil, nil, fmt.Errorf("terraform state backend filter %d: bucket must not be blank", index)
		}
		if filter.BackendKind == BackendLocal {
			return nil, nil, fmt.Errorf("terraform state backend filter %d: local discovery needs an explicit operator seed", index)
		}
		if filter.Region == "" {
			return nil, nil, fmt.Errorf("terraform state backend filter %d: region must not be blank", index)
		}
		if filter.Key == "" {
			listings = append(listings, filter)
			continue
		}
		if err := add(DiscoveryCandidate{
			State: StateKey{
				BackendKind: BackendS3,
				Locator:     "s3://" + filter.Bucket + "/" + filter.Key,
			},
			TargetScopeID: filter.TargetScopeID,
			Region:        filter.Region,
		}); err != nil {
			return nil, nil, fmt.Errorf("terraform state backend filter %d: %w", index, err)
		}
	}
	if len(candidates) == 0 && len(listings) == 0 {
		return nil, nil, fmt.Errorf("terraform state discovery names no state source: configure seeds or bucket backend filters")
	}
	return candidates, listings, nil
}
