# PORT_PLAN.md — terraform-state logic port (AFTER the scaffold)

The scaffold in this repo is green and shippable as a component package, but
its observe/emit core is still the placeholder `Report`/`Collect` flow. This
plan maps the in-tree packages to their scaffold targets for the later logic
port. Nothing here grants, deploys, or removes in-tree code.

Source of truth for paths and identities: `eshu-hq/eshu` at the cutover
base. Prep docs (`EXTRACTION_PREP.md`, `SHIM_MAP.md`,
`PRODUCER_GRANT_DRAFT.md`, `ROLLBACK_DRAFT.md`) live on the extraction prep
branch `drive/8868-tfstate-extraction-prep` (not on `main`); re-verify them
after they merge.

## 1. Entry point: `go/cmd/collector-terraform-state/` → `cmd/collector/`

| In-tree file | Scaffold target | Notes |
|---|---|---|
| `main.go` (entry, `parseArgs`, `run`) | `cmd/collector/main.go` | Keep the `--sdk-stdio` host protocol and wall-time bound; port flag surface |
| `config.go` (`loadRuntimeConfig`, `selectTerraformStateInstance`, validation, redaction-bundle startup-fatal) | `cmd/collector/main.go` + `config.example.yaml` | Startup-fatal redaction bundle stays startup-fatal |
| `service.go` (`buildCassetteService`, `buildClaimedService`, `newClaimID` `tfstate-claim-` namespace) | `cmd/collector/main.go` or new `cmd/collector/service.go` | Keep the `tfstate-claim-` claim-ID namespace verbatim |
| `aws_s3.go` (read-only exact-key + `IfNoneMatch`) | new `s3.go` (root package or `cmd/collector/`) | Read-only stays read-only; no writes |
| `aws_dynamodb.go` (digest-only lock metadata) | new `dynamodb.go` | Lock metadata only, never state content |
| `target_scope_source_factory.go` (`OpenSource`, `credentialsForCandidate`, `targetScopeAllowsCandidate`) | new `source_factory.go` | Credential-free URIs only (keep `checkSourceURI`) |
| `time.go`, `doc.go` | root package | Keep clock seams testable |

## 2. Parse/emit: `go/internal/collector/terraformstate/` → root package

Port the whole package surface into the root `collector` package (the repo
name already says which collector this is; file and symbol names do not
repeat it). Files, in dependency order:

- `types.go`, `attributes.go`, `composite_walker.go`, `anchors.go`,
  `identity.go`, `snapshot_identity.go`
- `candidate_identity.go` (planning only), `discovery.go`,
  `discovery_types.go`, `backend_filter.go`, `local_candidates.go`
- `config/` (`backend_config.go`, `local.go`, `resolution.go`),
  `source_terragrunt.go`, `source_local.go`, `source_s3.go`,
  `source_limit.go`, `schema_resolver.go`, `sensitive_attributes.go`
- `parser.go` (`Parse`/`ParseStream`/envelope), `resources.go`,
  `outputs.go`, `modules.go`, `providers.go`, `tags.go`
- `warnings.go`, `warning_fact.go`, `warning_classification.go`,
  `uncovered_provider.go`, `empty_state.go`
- `pagerduty_applied.go` (the 3 `incident_routing` constructors),
  `metrics.go`, `encode_helpers.go`, `json_token.go`, `fact_stream.go`
- Test parity pack `testdata/8868-parity-tfstate.json` → `testdata/`

The ten `FactKind*` constants in `collector.go` already name the port's
targets; the port replaces the placeholder `Report`/`Record` flow and wires
each file above to emit its kinds. `Contract()` needs no change.

## 3. Claim runtime: `go/internal/collector/tfstateruntime/` → root package

- `source.go` (`ClaimedSource.NextClaimed`, validate, `collectCandidate`,
  `readIdentity`, `parseCandidate`)
- `source_helpers.go` (`claimMatchesCandidate`, `claimMatchesCollected`,
  `attestTrustedDeletion`)
- `fact_spool.go`, `metrics.go`, `composite_capture_recorder.go`,
  `too_large_warning.go`

## 4. Non-negotiable port rules

- Import boundary: ported code may import only the published SDK modules,
  never `go/internal` or core stores (`boundary_test.go` enforces this).
- Telemetry preserved verbatim: spans
  `tfstate.collector.claim.process`, `discovery.resolve`, `source.open`,
  `parser.stream`, `fact.emit_batch`, `coordinator.complete`;
  `eshu_dp_tfstate_*` counters/histograms/gauge; the `tfstate-claim-`
  namespace; redaction-bundle + schema-bundle + `allow_empty_successor`
  attestation provisioning.
- `terraform_state_candidate` is never declared or emitted (git-owned,
  `core_owned`).
- Grant wiring stays one grant per kind per schema version
  (`grant.example.json` shape, `grant_test.go` matrix).

## 5. Known pre-port limits

- **#8851 bare-kind path (resolved in-scaffold).** The `conform` path
  requires a granted core kind to carry a `.` namespace prefix unless the
  caller supplies trusted host-granted coverage. The original-path SDK
  (`eshu/sdk/go/collector v0.2.0`) has no such path, so it hard-fails the
  seven bare `terraform_state_*` kinds; this scaffold therefore pins the
  relocated pair (`eshu-sdk/collector v0.1.0-rc.3` + `factschema
  v0.1.0-rc.5`, the core #9111 pins) and every `conformance.Run` supplies
  `CoreFacts` + `ReservedFactKinds` for the ten kinds
  (`corefacts_test.go`). The host supplies the real registry coverage at
  install/activation. Do not work around the rule by renaming kinds.
- **SDK pin conflict (resolved).** The grant draft's cited pins
  (`eshu-sdk/collector v0.1.0-rc.3`, `factschema v0.1.0-rc.5`) are the
  relocated-path modules, not the template's original-path `v0.2.0` pair —
  so the scaffold follows the draft. `compatibleCore` stays `>=0.0.5
  <0.2.0` (that range names the target core release, unchanged).
- **Prep docs unmerged.** `EXTRACTION_PREP.md` §8.6 also records the
  missing `collector_entrypoints.yaml` terraform-state entry as an open
  gap; close it in-tree before cutover claims to be complete.
