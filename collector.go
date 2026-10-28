// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

const (
	// ComponentID is the producer identity for the terraform-state
	// collector. It matches the grant draft (PRODUCER_GRANT_DRAFT.md on the
	// extraction prep branch) and manifest.yaml; the three must move together.
	ComponentID = "dev.eshu.collector.terraform-state"
	// CollectorKind is the collector family. It matches scope.CollectorTerraformState
	// ("terraform_state") in the core tree and the grant scope.
	CollectorKind = "terraform_state"
	// SourceSystem names the observed source. It matches the in-tree
	// SourceSystem for terraform-state facts (scope.CollectorTerraformState).
	SourceSystem = "terraform_state"
	// MetricsPrefix is the component-owned metric prefix from the manifest.
	MetricsPrefix = "eshu_dp_tfstate_"
)

const (
	// The seven terraform_state_* kinds owned by this producer (the
	// TerraformStateFamilyDeclaration list minus git-owned
	// terraform_state_candidate, which this producer must never emit).
	// All are schema 1.0.0.
	FactKindStateSnapshot        = "terraform_state_snapshot"
	FactKindStateResource        = "terraform_state_resource"
	FactKindStateOutput          = "terraform_state_output"
	FactKindStateModule          = "terraform_state_module"
	FactKindStateProviderBinding = "terraform_state_provider_binding"
	FactKindStateTagObservation  = "terraform_state_tag_observation"
	FactKindStateWarning         = "terraform_state_warning"
	// The three incident_routing kinds co-emitted from applied Terraform
	// state (IncidentRoutingFamilyDeclaration). All are schema 1.0.0.
	FactKindIncidentAppliedPagerDutyResource = "incident_routing.applied_pagerduty_resource"
	FactKindIncidentAppliedAlertRoute        = "incident_routing.applied_alert_route"
	// No incident_routing.coverage_warning constant: the shared component
	// registry enforces one owner per fact kind and
	// dev.eshu.collector.pagerduty owns it (ops-qa installer conflict,
	// eshu-hq/eshu#8868). Partial reversal of the #9195 EXTEND per arbiter
	// verdict (coverage_warning only).
)

// Report is the placeholder source document this scaffold reads. The logic
// port replaces it with real Terraform state observation; keep the
// redaction-safe shape (no secrets in struct fields that land in payloads).
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`
	Source      string    `json:"source"`
	Records     []Record  `json:"records"`
	// Serial and Lineage identify the state revision read. The host
	// requires a non-blank generation freshness hint on every committed
	// terraform-state claim (identity_mismatch otherwise), so Collect
	// renders them as `lineage=<uuid> serial=<n>`, the in-tree shape.
	Serial  int64  `json:"serial,omitempty"`
	Lineage string `json:"lineage,omitempty"`
}

// Record is one placeholder source row.
type Record struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	// Secret is intentionally never emitted: Collect redacts it and records
	// the redaction instead. Keep a field like this so the redaction path
	// stays exercised after the scaffold gains the real emitters.
	Secret string `json:"secret,omitempty"`
}

// CollectOptions controls emission for one claimed scope.
type CollectOptions struct {
	ObservedAt     time.Time
	SourceURI      string
	PreviousDigest string
	// Limits bounds one claim's emission. Each non-positive field selects
	// its DefaultResourceUse value, so a partially customized Limits keeps
	// the caller's values and only the gaps fall back to defaults; the zero
	// value resolves to DefaultResourceUse, and an unconfigured copy still
	// fails terminal instead of allocating without bound.
	Limits ResourceUse
}

// Contract returns the SDK fact families this collector may emit: the seven
// terraform_state_* kinds plus the three co-emitted incident_routing kinds,
// all at schema 1.0.0 with observed confidence (state evidence read directly
// from the source artifact, matching the in-tree emitters). The scaffold
// Collect below emits snapshot/resource/warning facts; the logic port fills
// in the remaining emitters without changing this declaration.
func Contract() sdk.Contract {
	observed := []sdk.SourceConfidence{sdk.SourceConfidenceObserved}
	return sdk.Contract{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		Facts: []sdk.FactDeclaration{
			{Kind: FactKindStateSnapshot, SchemaVersions: []string{"1.0.0"}, SourceConfidence: observed},
			{Kind: FactKindStateResource, SchemaVersions: []string{"1.0.0"}, SourceConfidence: observed},
			{Kind: FactKindStateOutput, SchemaVersions: []string{"1.0.0"}, SourceConfidence: observed},
			{Kind: FactKindStateModule, SchemaVersions: []string{"1.0.0"}, SourceConfidence: observed},
			{Kind: FactKindStateProviderBinding, SchemaVersions: []string{"1.0.0"}, SourceConfidence: observed},
			{Kind: FactKindStateTagObservation, SchemaVersions: []string{"1.0.0"}, SourceConfidence: observed},
			{Kind: FactKindStateWarning, SchemaVersions: []string{"1.0.0"}, SourceConfidence: observed},
			{Kind: FactKindIncidentAppliedPagerDutyResource, SchemaVersions: []string{"1.0.0"}, SourceConfidence: observed},
			{Kind: FactKindIncidentAppliedAlertRoute, SchemaVersions: []string{"1.0.0"}, SourceConfidence: observed},
		},
	}
}

// maxReportBytes caps one source document. Reads past the cap fail instead
// of decoding unbounded input; emission bounds below still gate every fact.
const maxReportBytes = 32 << 20

// LoadReport decodes one placeholder source document.
func LoadReport(r io.Reader) (Report, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxReportBytes+1))
	if err != nil {
		return Report{}, fmt.Errorf("read report: %w", err)
	}
	if len(raw) > maxReportBytes {
		return Report{}, fmt.Errorf("source document exceeds %d bytes", maxReportBytes)
	}
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		return Report{}, fmt.Errorf("decode report: %w", err)
	}
	return report, nil
}

// Digest returns the stable identity of one report for freshness/stale proof.
// It covers every field that changes emitted facts: the source (snapshot
// payload), each record's emitted fields, and each record's redaction
// presence, so a stale digest never preserves stale evidence.
func Digest(report Report) string {
	ids := make([]string, 0, len(report.Records)+1)
	ids = append(ids, "source\x00"+report.Source)
	for _, record := range report.Records {
		secretPresence := "secret:0"
		if record.Secret != "" {
			secretPresence = "secret:1"
		}
		ids = append(ids, record.ID+"\x00"+record.Name+"\x00"+record.Detail+"\x00"+secretPresence)
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\x01")))
	return hex.EncodeToString(sum[:])
}

// Collect builds one SDK result for the claimed scope and generation.
// Behavior contract, covered by conformance_test.go:
//   - empty input emits an authoritative complete snapshot with zero records
//     (retires stale evidence; distinct from unchanged freshness);
//   - unchanged digest emits ResultUnchanged (stale/freshness proof);
//   - duplicate record IDs emit one fact (idempotent re-emission);
//   - secrets never enter payloads; each redaction is recorded;
//   - source_ref carries claim scope, generation, and a credential-free URI;
//   - record, payload, and deadline bounds fail terminal, never unbounded.
func Collect(claim sdk.Claim, report Report, opts CollectOptions) (sdk.Result, error) {
	observedAt := opts.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	// Default per field, not wholesale: a partially customized Limits
	// keeps the caller's values (nestedLimits in cmd/collector already
	// defaults this way, so both entry points agree).
	limits := opts.Limits
	defaults := DefaultResourceUse()
	if limits.MaxRecordsPerClaim <= 0 {
		limits.MaxRecordsPerClaim = defaults.MaxRecordsPerClaim
	}
	if limits.MaxPayloadBytes <= 0 {
		limits.MaxPayloadBytes = defaults.MaxPayloadBytes
	}
	if limits.ClaimTimeoutSeconds <= 0 {
		limits.ClaimTimeoutSeconds = defaults.ClaimTimeoutSeconds
	}
	if !claim.Deadline.IsZero() && observedAt.After(claim.Deadline) {
		return terminalResult(claim, observedAt, "claim-deadline-exceeded"), nil
	}
	if err := sdk.ValidateShareSafeKeys(map[string]any{"source": report.Source}); err != nil {
		return sdk.Result{}, err
	}
	if strings.TrimSpace(opts.SourceURI) == "" {
		return sdk.Result{}, fmt.Errorf("source URI must not be blank")
	}
	if err := checkSourceURI(opts.SourceURI); err != nil {
		return sdk.Result{}, err
	}
	if len(report.Records) > limits.MaxRecordsPerClaim {
		return terminalResult(claim, observedAt, "record-budget-exceeded"), nil
	}
	// PreviousDigest accepts the last emitted snapshot stable key (the
	// LastDigest the Monitor reports) or the raw report digest; either
	// match means the source has not moved since the last emission.
	digest := Digest(report)
	if opts.PreviousDigest != "" && len(report.Records) > 0 &&
		(opts.PreviousDigest == digest || opts.PreviousDigest == "snapshot:"+digest) {
		return sdk.Result{
			ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
			State:           sdk.ResultUnchanged,
			Claim:           claim,
			Generation:      sdk.Generation{ID: claim.GenerationID, ObservedAt: observedAt, FreshnessHint: "unchanged-digest"},
			Statuses:        []sdk.Status{{Class: sdk.StatusComplete, FactCount: 0}},
		}, nil
	}

	seen := map[string]bool{}
	facts := make([]sdk.Fact, 0, len(report.Records)+1)
	for _, record := range report.Records {
		key := "record:" + record.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		// address mirrors the in-tree parser resource payload contract
		// (identity.go resourceAddress synthesizes type.name when the
		// state entry carries no explicit address): the host drift
		// evidence loader scans payload->>'address' into a NOT NULL
		// column, and a resource fact without it dead-letters its
		// scope's config_state_drift intent. The record ID already is
		// that address (liveResourceAddress), so it rides under both keys.
		payload := map[string]any{
			"id":      record.ID,
			"name":    record.Name,
			"address": record.ID,
		}
		if record.Detail != "" {
			payload["detail"] = record.Detail
		}
		factRedactions := []sdk.Redaction{}
		if record.Secret != "" {
			factRedactions = append(factRedactions, sdk.Redaction{Field: "secret", Reason: "credential-redacted"})
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return sdk.Result{}, fmt.Errorf("encode record payload: %w", err)
		}
		if len(encoded) > limits.MaxPayloadBytes {
			return terminalResult(claim, observedAt, "payload-budget-exceeded"), nil
		}
		facts = append(facts, sdk.Fact{
			Kind:             FactKindStateResource,
			SchemaVersion:    "1.0.0",
			StableKey:        key,
			SourceConfidence: sdk.SourceConfidenceObserved,
			ObservedAt:       observedAt,
			SourceRef: sdk.SourceRef{
				SourceSystem: SourceSystem,
				ScopeID:      claim.Scope.ID,
				GenerationID: claim.GenerationID,
				FactKey:      key,
				URI:          opts.SourceURI + "#" + record.ID,
				RecordID:     record.ID,
			},
			Payload:    payload,
			Redactions: factRedactions,
		})
	}
	// serial and lineage stay in the snapshot payload, not just the
	// generation freshness hint: the host drift evidence loader selects
	// payload->>'lineage' and payload->>'serial' from
	// terraform_state_snapshot facts, and a snapshot without them
	// dead-letters its scope's config_state_drift intent. This mirrors
	// the in-tree parser snapshotFact payload contract.
	snapshotPayload := map[string]any{
		"source":       report.Source,
		"record_count": len(seen),
		"serial":       report.Serial,
		"lineage":      report.Lineage,
	}
	if encoded, err := json.Marshal(snapshotPayload); err != nil || len(encoded) > limits.MaxPayloadBytes {
		return terminalResult(claim, observedAt, "payload-budget-exceeded"), nil
	}
	snapshotKey := "snapshot:" + Digest(report)
	facts = append(facts, sdk.Fact{
		Kind:             FactKindStateSnapshot,
		SchemaVersion:    "1.0.0",
		StableKey:        snapshotKey,
		SourceConfidence: sdk.SourceConfidenceObserved,
		ObservedAt:       observedAt,
		SourceRef: sdk.SourceRef{
			SourceSystem: SourceSystem,
			ScopeID:      claim.Scope.ID,
			GenerationID: claim.GenerationID,
			FactKey:      snapshotKey,
			URI:          opts.SourceURI,
			RecordID:     "snapshot",
		},
		Payload: snapshotPayload,
	})
	return sdk.Result{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		State:           sdk.ResultComplete,
		Claim:           claim,
		Generation:      sdk.Generation{ID: claim.GenerationID, ObservedAt: observedAt, FreshnessHint: stateFreshnessHint(report)},
		Facts:           facts,
		Statuses:        []sdk.Status{{Class: sdk.StatusComplete, FactCount: len(facts)}},
	}, nil
}

// stateFreshnessHint renders the generation freshness hint the host requires
// on every committed terraform-state claim (a blank hint fails the claim
// terminal identity_mismatch). A state revision carries the in-tree
// `lineage=<uuid> serial=<n>` shape. A report without identity — the
// authoritative-empty listing, or an unreadable object degraded to partial —
// reports how many states were observed instead of inventing an identity.
func stateFreshnessHint(report Report) string {
	if lineage := strings.TrimSpace(report.Lineage); lineage != "" {
		return fmt.Sprintf("lineage=%s serial=%d", lineage, report.Serial)
	}
	return "states=0"
}

// CollectPartial is the partial-failure shape: reachable records commit as
// ResultPartial with a warning status, never as an ambiguous complete.
func CollectPartial(claim sdk.Claim, report Report, opts CollectOptions, failure string) (sdk.Result, error) {
	result, err := Collect(claim, report, opts)
	if err != nil {
		return sdk.Result{}, err
	}
	if result.State != sdk.ResultComplete {
		return result, nil
	}
	// The cause travels as a FactKindStateWarning fact so operators see why the
	// claim is partial. The reason is share-safe validated first; an
	// unshareable cause is withheld and the redaction recorded instead.
	reason := strings.TrimSpace(failure)
	if reason == "" {
		reason = "source-degraded"
	}
	warningPayload := map[string]any{"reason": reason}
	warningRedactions := []sdk.Redaction{}
	// ValidateShareSafeKeys guards key shape; the reason travels as a value,
	// so credential-marker text is scanned separately. Either trip withholds
	// the cause and records the redaction instead of leaking it.
	if err := sdk.ValidateShareSafeKeys(map[string]any{"reason": reason}); err != nil || containsCredentialText(reason) {
		warningPayload = map[string]any{"reason": "withheld-unshareable-cause"}
		warningRedactions = append(warningRedactions, sdk.Redaction{Field: "reason", Reason: "unshareable-cause-redacted"})
	}
	observedAt := result.Generation.ObservedAt
	warningKey := "warning:partial:" + claim.GenerationID
	result.Facts = append(result.Facts, sdk.Fact{
		Kind:             FactKindStateWarning,
		SchemaVersion:    "1.0.0",
		StableKey:        warningKey,
		SourceConfidence: sdk.SourceConfidenceObserved,
		ObservedAt:       observedAt,
		SourceRef: sdk.SourceRef{
			SourceSystem: SourceSystem,
			ScopeID:      claim.Scope.ID,
			GenerationID: claim.GenerationID,
			FactKey:      warningKey,
			URI:          opts.SourceURI + "#partial",
			RecordID:     "partial",
		},
		Payload:    warningPayload,
		Redactions: warningRedactions,
	})
	result.State = sdk.ResultPartial
	result.Statuses = append(result.Statuses, sdk.Status{Class: sdk.StatusWarning, Partial: true, WarningCount: 1, FactCount: len(result.Facts)})
	return result, nil
}

// terminalResult is the bounded-failure shape for exhausted budgets and
// expired claims: explicit terminal state with a failure class, never an
// ambiguous complete and never unbounded allocation.
func terminalResult(claim sdk.Claim, observedAt time.Time, failureClass string) sdk.Result {
	return sdk.Result{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		State:           sdk.ResultTerminal,
		Claim:           claim,
		Generation:      sdk.Generation{ID: claim.GenerationID, ObservedAt: observedAt},
		Statuses: []sdk.Status{{
			Class: sdk.StatusFailure, FailureClass: failureClass,
		}},
	}
}

// CollectRetryable is the bounded-retry shape for transient source errors.
func CollectRetryable(claim sdk.Claim, observedAt time.Time, retryAfterSeconds int) sdk.Result {
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	return sdk.Result{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		State:           sdk.ResultRetryable,
		Claim:           claim,
		Generation:      sdk.Generation{ID: claim.GenerationID, ObservedAt: observedAt.UTC()},
		Statuses: []sdk.Status{{
			Class: sdk.StatusFailure, FailureClass: "transient", RetryAfterSeconds: retryAfterSeconds,
		}},
	}
}

func checkSourceURI(raw string) error {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	// url.Parse only surfaces userinfo after "//", so check "@" first: a
	// hierarchical URI with userinfo (https://user:secret@host) and an
	// opaque credential body (svc:SECRET@host/x) are both refused. Error
	// text never echoes the URI, which may carry the credential.
	if strings.Contains(trimmed, "@") {
		if u, err := url.Parse(trimmed); err != nil || u.User != nil {
			return fmt.Errorf("source URI must not embed credentials")
		}
		if !strings.Contains(lower, "://") {
			return fmt.Errorf("source URI must not embed credentials")
		}
	}
	for _, key := range []string{"password", "secret", "token"} {
		if strings.Contains(lower, key+"=") {
			return fmt.Errorf("source URI must not embed credentials")
		}
	}
	return nil
}

// credentialValueMarkers are substrings that mark free text as unsafe to
// emit. Matching is deliberately broad: a withheld reason stays diagnosable
// through its redaction record, while a leaked credential does not.
var credentialValueMarkers = []string{
	"password", "passwd", "secret", "token", "bearer",
	"api_key", "apikey", "private_key", "client_secret", "credentials",
}

// containsCredentialText reports whether free text may carry a credential.
func containsCredentialText(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range credentialValueMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
