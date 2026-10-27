// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

// liveRetryAfterSeconds bounds the host retry for a transient source
// failure before the next claim attempt re-reads the object.
const liveRetryAfterSeconds = 30

// maxLiveCandidates caps one claim's exact candidate set. A listing that
// resolves past the cap fails closed instead of fanning one claim out
// across an unbounded bucket.
const maxLiveCandidates = 128

// liveEmptySourceURI is the credential-free source URI for a claim whose
// discovery resolved zero keys: an authoritative empty observation, not a
// missing source.
const liveEmptySourceURI = "live:discovery"

// liveStateResource is the identity-only projection of one Terraform state
// resource. Attribute values are deliberately absent: without the ported
// redaction engine every value is unshareable, so the satellite emits
// addresses, types, and modes (join keys) and leaves attribute emission to
// the full parser port.
type liveStateResource struct {
	Mode      string `json:"mode"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Module    string `json:"module"`
	Instances []any  `json:"instances"`
}

// ReportForState builds the placeholder Report for one fetched state
// document. One record per resource carries its address as the stable ID,
// its type as the name, and its mode as detail; no attribute value ever
// enters a payload, so no secret can flow through this path by
// construction.
func ReportForState(locator string, body []byte) (Report, error) {
	var document struct {
		Resources []liveStateResource `json:"resources"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return Report{}, fmt.Errorf("decode state document: %w", err)
	}
	report := Report{GeneratedAt: time.Now().UTC(), Source: locator}
	for _, resource := range document.Resources {
		address := liveResourceAddress(resource)
		if address == "" {
			continue
		}
		detail := strings.TrimSpace(resource.Mode)
		if detail == "" {
			detail = "managed"
		}
		report.Records = append(report.Records, Record{
			ID:     address,
			Name:   strings.TrimSpace(resource.Type),
			Detail: detail,
		})
	}
	return report, nil
}

func liveResourceAddress(resource liveStateResource) string {
	resourceType := strings.TrimSpace(resource.Type)
	name := strings.TrimSpace(resource.Name)
	if resourceType == "" || name == "" {
		return ""
	}
	address := resourceType + "." + name
	if module := strings.TrimSpace(resource.Module); module != "" {
		address = strings.TrimSuffix(module, ".") + "." + address
	}
	return address
}

// CollectLive resolves discovery against target scopes, fetches every
// candidate through the reader, and folds each state into the existing
// Collect contract. It mirrors the tempo Handler shape: parse first (done
// by the caller), collect here, and return the SDK result the host
// validates and commits. Per-candidate outcomes merge by severity
// (terminal above retryable above partial above complete above unchanged)
// so one unreadable object degrades the claim instead of failing it, while
// a transient source releases the claim for a bounded retry.
func CollectLive(ctx context.Context, claim sdk.Claim, live LiveConfig, reader SourceReader, opts CollectOptions) (sdk.Result, error) {
	observedAt := opts.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	if reader == nil {
		return sdk.Result{}, fmt.Errorf("live state reader must not be nil")
	}
	exact, listings, err := ResolveLiveCandidates(live)
	if err != nil {
		return sdk.Result{}, err
	}
	candidates, err := listLiveCandidates(ctx, reader, live, exact, listings)
	if err != nil {
		return sdk.Result{}, err
	}
	if len(candidates) == 0 {
		return Collect(claim, Report{GeneratedAt: observedAt, Source: liveEmptySourceURI}, CollectOptions{
			ObservedAt:     observedAt,
			SourceURI:      liveEmptySourceURI,
			PreviousDigest: opts.PreviousDigest,
			Limits:         opts.Limits,
		})
	}
	results := make([]sdk.Result, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return sdk.Result{}, err
		}
		result, retryable, err := collectLiveCandidate(ctx, claim, candidate, reader, opts, observedAt)
		if err != nil {
			return sdk.Result{}, err
		}
		if retryable != nil {
			results = append(results, *retryable)
			continue
		}
		results = append(results, result)
	}
	return mergeLiveResults(results, claim, observedAt), nil
}

func listLiveCandidates(
	ctx context.Context,
	reader SourceReader,
	live LiveConfig,
	exact []DiscoveryCandidate,
	listings []DiscoveryBackendFilter,
) ([]DiscoveryCandidate, error) {
	candidates := append([]DiscoveryCandidate{}, exact...)
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		seen[liveCandidateKey(candidate)] = struct{}{}
	}
	for _, filter := range listings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		keys, err := reader.ListKeys(ctx, filter.Bucket, filter.Region, filter.Key)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			candidate := DiscoveryCandidate{
				State: StateKey{
					BackendKind: BackendS3,
					Locator:     "s3://" + filter.Bucket + "/" + key,
				},
				TargetScopeID: filter.TargetScopeID,
				Region:        filter.Region,
			}
			if err := candidate.Validate(); err != nil {
				continue
			}
			scopeID, err := resolveTargetScopeID(live.TargetScopes, candidate)
			if err != nil {
				return nil, err
			}
			candidate.TargetScopeID = scopeID
			identity := liveCandidateKey(candidate)
			if _, ok := seen[identity]; ok {
				continue
			}
			seen[identity] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) > maxLiveCandidates {
		return nil, fmt.Errorf("terraform state discovery resolved %d candidates past the cap of %d", len(candidates), maxLiveCandidates)
	}
	return candidates, nil
}

func liveCandidateKey(candidate DiscoveryCandidate) string {
	return string(candidate.State.BackendKind) + "\x00" + candidate.State.Locator + "\x00" + candidate.State.VersionID
}

func collectLiveCandidate(
	ctx context.Context,
	claim sdk.Claim,
	candidate DiscoveryCandidate,
	reader SourceReader,
	opts CollectOptions,
	observedAt time.Time,
) (sdk.Result, *sdk.Result, error) {
	bucket, key := splitS3Locator(candidate.State.Locator)
	candidateOpts := CollectOptions{
		ObservedAt:     observedAt,
		SourceURI:      candidate.State.Locator,
		PreviousDigest: opts.PreviousDigest,
		Limits:         opts.Limits,
	}
	if candidate.State.BackendKind != BackendS3 {
		return sdk.Result{}, nil, fmt.Errorf("terraform state backend kind %q has no live reader in this satellite", candidate.State.BackendKind)
	}
	object, err := reader.GetObject(ctx, bucket, key, candidate.Region)
	if err != nil {
		switch {
		case isStateMissing(err):
			partial, partialErr := CollectPartial(claim, Report{GeneratedAt: observedAt, Source: candidate.State.Locator}, candidateOpts, "state-missing")
			return partial, nil, partialErr
		case isStateTooLarge(err):
			partial, partialErr := CollectPartial(claim, Report{GeneratedAt: observedAt, Source: candidate.State.Locator}, candidateOpts, "state-too-large")
			return partial, nil, partialErr
		case isStateTransient(err):
			retryable := CollectRetryable(claim, observedAt, liveRetryAfterSeconds)
			return sdk.Result{}, &retryable, nil
		default:
			return sdk.Result{}, nil, err
		}
	}
	report, err := ReportForState(candidate.State.Locator, object.Body)
	if err != nil {
		return sdk.Result{}, nil, err
	}
	result, err := Collect(claim, report, candidateOpts)
	if err != nil {
		return sdk.Result{}, nil, err
	}
	return result, nil, nil
}

func splitS3Locator(locator string) (string, string) {
	rest, _ := strings.CutPrefix(locator, "s3://")
	bucket, key, _ := strings.Cut(rest, "/")
	return bucket, key
}

func isStateMissing(err error) bool {
	return errors.Is(err, ErrStateMissing)
}

func isStateTooLarge(err error) bool {
	return errors.Is(err, ErrStateTooLarge)
}

func isStateTransient(err error) bool {
	return errors.Is(err, ErrStateTransient)
}

// mergeLiveResults folds per-candidate results into one claim result by
// severity: terminal wins over retryable, retryable over partial, partial
// over complete, complete over unchanged. Facts and statuses concatenate;
// the generation carries the claim ID at the claim observation time.
func mergeLiveResults(results []sdk.Result, claim sdk.Claim, observedAt time.Time) sdk.Result {
	merged := sdk.Result{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		State:           sdk.ResultUnchanged,
		Claim:           claim,
		Generation:      sdk.Generation{ID: claim.GenerationID, ObservedAt: observedAt},
	}
	rank := map[sdk.ResultState]int{
		sdk.ResultUnchanged: 0,
		sdk.ResultComplete:  1,
		sdk.ResultPartial:   2,
		sdk.ResultRetryable: 3,
		sdk.ResultTerminal:  4,
	}
	for _, result := range results {
		merged.Facts = append(merged.Facts, result.Facts...)
		merged.Statuses = append(merged.Statuses, result.Statuses...)
		if rank[result.State] > rank[merged.State] {
			merged.State = result.State
		}
		if result.Generation.FreshnessHint != "" && merged.Generation.FreshnessHint == "" {
			merged.Generation.FreshnessHint = result.Generation.FreshnessHint
		}
	}
	// A freshness hint only describes an unchanged claim: emitting facts
	// alongside one would contradict the result state.
	if merged.State != sdk.ResultUnchanged {
		merged.Generation.FreshnessHint = ""
	}
	return merged
}
