# NOT_PORTED.md — terraform-state logic port, blocked / core-owned surface

Cutover base: `eshu-hq/eshu` at the port-branch HEAD. Scaffold pins
`eshu-sdk/collector v0.1.0-rc.3` + `factschema v0.1.0-rc.5`.

Shim seam for every entry below: the published SDK pair
(`eshu-sdk/collector`, `eshu-sdk/factschema`) plus the `--sdk-stdio` host
protocol in `cmd/collector` (PORT_PLAN §4). No ported file may import
`go/internal`, core stores, or another collector (`boundary_test.go`).

Satellite live-source increment (`drive/tfstate-live-source`): the `aws_s3.go`
row below stays blocked for the full in-tree port (aws-sdk-go-v2 + DynamoDB
lock reads), but the satellite's own live path (`live.go`, `livereport.go`,
`s3http.go`, `s3auth.go`) covers read-only S3 discovery/fetch with a stdlib
SigV4 client instead of porting `aws_s3.go`. Not carried over in this
increment: conditional ETag reads, DynamoDB lock metadata, graph-backed
backend facts, and attribute-level parsing/redaction.
`SHIM_MAP.md` / `EXTRACTION_PREP.md` live on the extraction prep branch
(`drive/8868-tfstate-extraction-prep`) and name no file that exists there
under the documented names — re-verify them after they merge (gap).

Legend: **blocked-on-SDK** = no replacement in the pinned SDK pair; the
file STOPs here per the port contract (no invented SDK surface).
**blocked-transitive** = stdlib-only file whose intra-package siblings are
blocked-on-SDK (it cannot compile in the root package yet).
**core-owned** = host control plane; intentionally stays in-tree.

## `go/cmd/collector-terraform-state/` (host entry)

| File | Disposition | Core-owned reason / missing symbol |
|---|---|---|
| `main.go` | core-owned | Host bootstrap: `app`, `buildinfo`, runtime config, `storage/postgres` open, `telemetry.NewBootstrap`. Scaffold `cmd/collector/main.go` re-implements only the `--sdk-stdio` protocol. |
| `config.go` | core-owned | `loadRuntimeConfig` binds `redact.RuleSet`, the `terraformschema` embedded bundle, `workflow.DesiredCollectorInstance`, and `scope`; port flag surface + validation only. |
| `service.go` | core-owned | `buildCassetteService` / `buildClaimedService` bind `storage/postgres`, `replay/cassette`, `collector.ClaimedService`, `workflow`; keep the `tfstate-claim-` namespace verbatim in the scaffold. |
| `aws_s3.go` | blocked-on-SDK (boundary) | Needs `aws-sdk-go-v2` (read-only exact-key + `IfNoneMatch`), which is outside the `boundary_test.go` allowlist. Owner decision required to extend the boundary + `go.mod`. |
| `aws_dynamodb.go` | blocked-on-SDK (boundary) | Same as `aws_s3.go` (digest-only lock metadata reads). |
| `target_scope_source_factory.go` | blocked-transitive | Imports `tfstateruntime` (core-owned, below) plus blocked `terraformstate` siblings; `checkSourceURI` credential-free rule already lives in the scaffold `collector.go`. |

## `go/internal/collector/terraformstate/` (parse/emit)

Blocked-on-SDK (direct — eshu-internal import, no pinned-SDK replacement):

| File | Missing SDK symbol |
|---|---|
| `attributes.go`, `types.go`, `tags.go`, `outputs.go`, `identity.go`, `identity_join_keys.go`, `anchors.go` | Redact engine: `redact.RuleSet`, `Key`, `Value`, `Scalar`, `Decision`, `ActionRedact/Preserve/Drop`, `SchemaKnown/Unknown`, `Reason*` (SDK `redaction.go` exposes only `IsSensitiveKeyName` + `ValidateShareSafeKeys`). |
| `fact_stream.go`, `types.go`, `parser.go`, `warning_fact.go`, `pagerduty_applied.go`, `source.go` | `facts.Envelope` (SDK has `Fact`, no envelope). |
| `modules.go`, `outputs.go`, `providers.go`, `resources.go`, `tags.go`, `warnings.go`, `warning_fact.go`, `pagerduty_applied.go` | `facts/cloud` typed builders + `facts/incident` (3 `incident_routing` constructors); factschema has decode only, no emit-side builders. |
| `types.go`, `parser.go`, `warning_fact.go`, `pagerduty_applied.go`, `source.go`, `too_large_warning.go` (via runtime), `source_helpers.go` (via runtime) | `scope.*`: `IngestionScope`, `ScopeGeneration`, `CollectorTerraformState`, `TriggerKindSnapshot`, `GenerationStatusPending` (SDK `Scope`/`Generation` partial only). |
| `metrics.go`, `discovery.go` | Telemetry: `telemetry.Instruments`, `AttrBackendKind`, `AttrSafeLocatorHash`, spans `tfstate.collector.claim.process`, `discovery.resolve`, `source.open`, `parser.stream`, `fact.emit_batch`, `coordinator.complete`; rewrite path is direct OTel + re-declared `eshu_dp_tfstate_*` instruments. |
| `schema_resolver.go` | `terraformschema.EmbeddedSchemasFS` (bundle must be vendored; no SDK equivalent). |
| `backend_config.go` | `facts.StableID` (fact identity keys; confirmed import `go/internal/facts`). |
| `uncovered_provider.go`, `warning_classification.go` | `tfstatewarning` (`ReasonProviderNotInSchemaBundle`, `Classification`, severity/actionability). Tiny logic-only package: portable by copying once its own imports are scanned (NOT verified this increment — no new SDK surface needed, sequencing only). |

Blocked-transitive (stdlib-only, compiles only with the rows above):

| File | Blocked sibling symbol |
|---|---|
| `backend_config_local.go`, `source_local.go`, `source_s3.go`, `source_terragrunt.go`, `config.go`, `discovery_backend_filter.go`, `discovery_types.go` | `BackendKind`, `StateKey`, `SourceMetadata`, `StateLocator` (all `types.go`); `BackendLocal` |
| `backend_config_resolution.go` | `BackendConfigContext` (`backend_config.go`) |
| `candidate_identity.go` | `locatorHash` (`identity.go`), `DiscoveryCandidate` (`discovery_types.go`) |
| `source_limit.go` | `ErrStateTooLarge` (`source_local.go`) |
| `composite_walker.go` | `stateParser` + composite-capture recorders (`parser.go`) |

## `go/internal/collector/tfstateruntime/` (claim runtime, core-owned)

| File | Disposition | Core-owned reason / missing symbol |
|---|---|---|
| `source.go` | core-owned | Claim-lease control loop (`workflow.WorkItem`, `collector.CollectedGeneration`, generation attest incl. `attestTrustedDeletion` trust wiring). No SDK replacement for `workflow` or `collector.Service`/`ClaimedService`. |
| `source_helpers.go` | core-owned | `claimMatchesCandidate` / `claimMatchesCollected` lease matching on `scope` + `workflow` types; same control plane. |
| `fact_spool.go`, `metrics.go`, `composite_capture_recorder.go`, `too_large_warning.go` | blocked-on-SDK | Need `facts.Envelope`, `collector.*`, `telemetry.*`, `scope.*` respectively (see terraformstate rows); `fact_spool.go` additionally needs the runtime claim loop. |

## Ported this increment (verbatim, package clause only)

`json_token.go`, `snapshot_identity.go`, `encode_helpers.go`,
`sensitive_attributes.go`, `empty_state.go`, `local_candidates.go`,
`time.go` — each differs from in-tree by exactly one line
(`package terraformstate`/`package main` → `package collector`;
`diff` evidence in the port commit). Doc files (`doc.go` × 3) merged by
reference: the scaffold `doc.go` already states the standalone boundary and
the in-tree docs describe the core-owned runtime above, so copying them
would misdescribe the scaffold.
