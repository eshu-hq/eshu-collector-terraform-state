// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"fmt"
	"strings"
)

// BackendKind identifies the Terraform state backend family. Ported from
// go/internal/collector/terraformstate (types.go): only the families this
// satellite can open without operator graph access are resolved here.
type BackendKind string

const (
	// BackendLocal identifies an exact operator-approved local state file.
	BackendLocal BackendKind = "local"
	// BackendS3 identifies an exact S3 object state backend.
	BackendS3 BackendKind = "s3"
)

// StateKey identifies one exact state snapshot source. Ported from the
// in-tree StateKey; the terragrunt spelling is rejected because this
// satellite resolves no nested backend indirection.
type StateKey struct {
	BackendKind BackendKind
	Locator     string
	VersionID   string
}

// Validate checks that the key names one exact readable state source.
// It mirrors validateExactCandidateState for the local and s3 families.
func (key StateKey) Validate() error {
	locator := strings.TrimSpace(key.Locator)
	if locator != key.Locator {
		return fmt.Errorf("terraform state source locator must not have surrounding whitespace")
	}
	versionID := strings.TrimSpace(key.VersionID)
	if versionID != key.VersionID {
		return fmt.Errorf("terraform state source version_id must not have surrounding whitespace")
	}
	switch key.BackendKind {
	case BackendLocal:
		if !strings.HasPrefix(locator, "/") {
			return fmt.Errorf("local state locator must be absolute")
		}
		if versionID != "" {
			return fmt.Errorf("local state version_id is unsupported")
		}
	case BackendS3:
		rest, ok := strings.CutPrefix(locator, "s3://")
		if !ok {
			return fmt.Errorf("s3 state locator must start with s3://")
		}
		bucket, stateKey, ok := strings.Cut(rest, "/")
		if !ok || strings.TrimSpace(bucket) == "" || strings.TrimSpace(stateKey) == "" {
			return fmt.Errorf("s3 state locator must include bucket and key")
		}
		if strings.HasSuffix(stateKey, "/") {
			return fmt.Errorf("s3 state locator must name an exact object")
		}
	default:
		return fmt.Errorf("unsupported terraform state backend kind %q", key.BackendKind)
	}
	return nil
}

// DiscoveryBackendFilter scopes backend discovery without naming individual
// repositories. Empty fields are wildcards; target_scope_id is routing
// metadata applied to matching candidates. Ported from discovery_types.go.
type DiscoveryBackendFilter struct {
	TargetScopeID string
	BackendKind   BackendKind
	Bucket        string
	Key           string
	Region        string
}

// DiscoverySeed is one exact operator-approved state locator. PreviousETag
// stays unset: it is durable freshness metadata the host owns, never claim
// config. Ported from discovery_types.go.
type DiscoverySeed struct {
	Kind          BackendKind
	TargetScopeID string
	Path          string
	Bucket        string
	Key           string
	Region        string
	VersionID     string
}

// DiscoveryCandidate is one exact Terraform state object to read. Ported
// from discovery_types.go, restricted to the fields the satellite resolves.
type DiscoveryCandidate struct {
	State         StateKey
	TargetScopeID string
	Region        string
}

// Validate checks that the candidate names an exact state source. It mirrors
// the in-tree DiscoveryCandidate.Validate for the seed and listing paths
// (the graph/git-local paths stay core-owned).
func (candidate DiscoveryCandidate) Validate() error {
	if err := candidate.State.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(candidate.TargetScopeID) != candidate.TargetScopeID {
		return fmt.Errorf("terraform state target_scope_id must not have surrounding whitespace")
	}
	return nil
}

// TargetScope is the host's per-scope AWS routing envelope, reduced to the
// fields the satellite enforces: identity plus the backend and region
// allowlists. Credential material (role_arn, external_id, credential_mode)
// is intentionally absent: the satellite authenticates with ambient
// workload credentials only and never reads credential keys from config.
type TargetScope struct {
	TargetScopeID   string
	Provider        string
	DeploymentMode  string
	AllowedRegions  []string
	AllowedBackends []string
}

// LiveConfig is the parsed live-source claim configuration: discovery plus
// target scopes, with no file input and no credential material.
type LiveConfig struct {
	Seeds          []DiscoverySeed
	BackendFilters []DiscoveryBackendFilter
	TargetScopes   []TargetScope
}

// HasLiveDiscovery reports whether the raw config carries a live-source
// activation (discovery or target_scopes) as opposed to a file input.
func HasLiveDiscovery(config map[string]any) bool {
	if config == nil {
		return false
	}
	if discovery, ok := config["discovery"].(map[string]any); ok && len(discovery) > 0 {
		return true
	}
	if scopes, ok := config["target_scopes"].([]any); ok && len(scopes) > 0 {
		return true
	}
	return false
}

// ParseLiveConfig parses the host's discovery/target_scopes activation into
// typed config. It mirrors the in-tree collectorConfiguration JSON shape
// (config.go ParseDiscoveryConfig) plus the runtime target_scopes envelope,
// failing closed on malformed values.
func ParseLiveConfig(config map[string]any) (LiveConfig, error) {
	var live LiveConfig
	discovery, _ := config["discovery"].(map[string]any)
	if config["discovery"] != nil && discovery == nil {
		return LiveConfig{}, fmt.Errorf("config discovery must be an object")
	}
	if discovery != nil {
		filters, err := parseBackendFilters(discovery["backend_filters"])
		if err != nil {
			return LiveConfig{}, err
		}
		live.BackendFilters = filters
		seeds, err := parseSeeds(discovery["seeds"])
		if err != nil {
			return LiveConfig{}, err
		}
		live.Seeds = seeds
	}
	scopes, err := parseTargetScopes(config["target_scopes"])
	if err != nil {
		return LiveConfig{}, err
	}
	live.TargetScopes = scopes
	return live, nil
}

func parseBackendFilters(raw any) ([]DiscoveryBackendFilter, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("config discovery.backend_filters must be an array")
	}
	filters := make([]DiscoveryBackendFilter, 0, len(items))
	for index, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("config discovery.backend_filters[%d] must be an object", index)
		}
		kind, err := optionalConfigString(entry, "backend_kind")
		if err != nil {
			return nil, fmt.Errorf("config discovery.backend_filters[%d]: %w", index, err)
		}
		bucket, err := optionalConfigString(entry, "bucket")
		if err != nil {
			return nil, fmt.Errorf("config discovery.backend_filters[%d]: %w", index, err)
		}
		key, err := optionalConfigString(entry, "key")
		if err != nil {
			return nil, fmt.Errorf("config discovery.backend_filters[%d]: %w", index, err)
		}
		region, err := optionalConfigString(entry, "region")
		if err != nil {
			return nil, fmt.Errorf("config discovery.backend_filters[%d]: %w", index, err)
		}
		targetScopeID, err := optionalConfigString(entry, "target_scope_id")
		if err != nil {
			return nil, fmt.Errorf("config discovery.backend_filters[%d]: %w", index, err)
		}
		filters = append(filters, DiscoveryBackendFilter{
			TargetScopeID: strings.TrimSpace(targetScopeID),
			BackendKind:   BackendKind(strings.ToLower(strings.TrimSpace(kind))),
			Bucket:        strings.TrimSpace(bucket),
			Key:           strings.Trim(strings.TrimSpace(key), "/"),
			Region:        strings.ToLower(strings.TrimSpace(region)),
		})
	}
	return normalizedBackendFilters(filters), nil
}

// normalizedBackendFilters drops fully-empty and duplicate filters. Ported
// from discovery_backend_filter.go.
func normalizedBackendFilters(filters []DiscoveryBackendFilter) []DiscoveryBackendFilter {
	normalized := make([]DiscoveryBackendFilter, 0, len(filters))
	seen := map[DiscoveryBackendFilter]struct{}{}
	for _, filter := range filters {
		item := DiscoveryBackendFilter{
			TargetScopeID: strings.TrimSpace(filter.TargetScopeID),
			BackendKind:   BackendKind(strings.ToLower(strings.TrimSpace(string(filter.BackendKind)))),
			Bucket:        strings.TrimSpace(filter.Bucket),
			Key:           strings.Trim(strings.TrimSpace(filter.Key), "/"),
			Region:        strings.ToLower(strings.TrimSpace(filter.Region)),
		}
		if item.TargetScopeID == "" && item.BackendKind == "" && item.Bucket == "" && item.Key == "" && item.Region == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	return normalized
}

func parseSeeds(raw any) ([]DiscoverySeed, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("config discovery.seeds must be an array")
	}
	seeds := make([]DiscoverySeed, 0, len(items))
	for index, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("config discovery.seeds[%d] must be an object", index)
		}
		seed := DiscoverySeed{}
		var err error
		if seed.TargetScopeID, err = optionalConfigString(entry, "target_scope_id"); err != nil {
			return nil, fmt.Errorf("config discovery.seeds[%d]: %w", index, err)
		}
		kind, err := optionalConfigString(entry, "kind")
		if err != nil {
			return nil, fmt.Errorf("config discovery.seeds[%d]: %w", index, err)
		}
		seed.Kind = BackendKind(strings.ToLower(strings.TrimSpace(kind)))
		if seed.Path, err = optionalConfigString(entry, "path"); err != nil {
			return nil, fmt.Errorf("config discovery.seeds[%d]: %w", index, err)
		}
		seed.Path = strings.TrimSpace(seed.Path)
		if seed.Bucket, err = optionalConfigString(entry, "bucket"); err != nil {
			return nil, fmt.Errorf("config discovery.seeds[%d]: %w", index, err)
		}
		seed.Bucket = strings.TrimSpace(seed.Bucket)
		if seed.Key, err = optionalConfigString(entry, "key"); err != nil {
			return nil, fmt.Errorf("config discovery.seeds[%d]: %w", index, err)
		}
		seed.Key = strings.Trim(strings.TrimSpace(seed.Key), "/")
		if seed.Region, err = optionalConfigString(entry, "region"); err != nil {
			return nil, fmt.Errorf("config discovery.seeds[%d]: %w", index, err)
		}
		seed.Region = strings.ToLower(strings.TrimSpace(seed.Region))
		if seed.VersionID, err = optionalConfigString(entry, "version_id"); err != nil {
			return nil, fmt.Errorf("config discovery.seeds[%d]: %w", index, err)
		}
		seed.VersionID = strings.TrimSpace(seed.VersionID)
		seed.TargetScopeID = strings.TrimSpace(seed.TargetScopeID)
		seeds = append(seeds, seed)
	}
	return seeds, nil
}

func parseTargetScopes(raw any) ([]TargetScope, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("config target_scopes must be an array")
	}
	scopes := make([]TargetScope, 0, len(items))
	for index, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("config target_scopes[%d] must be an object", index)
		}
		scope := TargetScope{}
		var err error
		if scope.TargetScopeID, err = optionalConfigString(entry, "target_scope_id"); err != nil {
			return nil, fmt.Errorf("config target_scopes[%d]: %w", index, err)
		}
		scope.TargetScopeID = strings.TrimSpace(scope.TargetScopeID)
		if scope.TargetScopeID == "" {
			return nil, fmt.Errorf("config target_scopes[%d].target_scope_id must not be blank", index)
		}
		if scope.Provider, err = optionalConfigString(entry, "provider"); err != nil {
			return nil, fmt.Errorf("config target_scopes[%d]: %w", index, err)
		}
		scope.Provider = strings.ToLower(strings.TrimSpace(scope.Provider))
		if scope.DeploymentMode, err = optionalConfigString(entry, "deployment_mode"); err != nil {
			return nil, fmt.Errorf("config target_scopes[%d]: %w", index, err)
		}
		scope.DeploymentMode = strings.ToLower(strings.TrimSpace(scope.DeploymentMode))
		if scope.AllowedRegions, err = optionalConfigStringList(entry, "allowed_regions"); err != nil {
			return nil, fmt.Errorf("config target_scopes[%d]: %w", index, err)
		}
		if scope.AllowedBackends, err = optionalConfigStringList(entry, "allowed_backends"); err != nil {
			return nil, fmt.Errorf("config target_scopes[%d]: %w", index, err)
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

func optionalConfigString(entry map[string]any, key string) (string, error) {
	raw, ok := entry[key]
	if !ok || raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%q must be a string", key)
	}
	return value, nil
}

func optionalConfigStringList(entry map[string]any, key string) ([]string, error) {
	raw, ok := entry[key]
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%q must be an array of strings", key)
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%q must be an array of strings", key)
		}
		values = append(values, strings.ToLower(strings.TrimSpace(value)))
	}
	return values, nil
}
