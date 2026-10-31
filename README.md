# eshu-collector-terraform-state

Standalone Eshu collector that observes Terraform state artifacts and emits
source evidence for the `terraform_state` collector kind. This repository was
scaffolded from `templates/collector` in `eshu-hq/eshu`; the placeholder
observe/emit core is still in place and the real Terraform state logic port
is tracked in [PORT_PLAN.md](PORT_PLAN.md).

Identity:

- Producer: `dev.eshu.collector.terraform-state`, version `0.1.0`
- Collector kind / grant scope: `terraform_state`
- Source system: `terraform_state`
- Metric prefix: `eshu_dp_tfstate_`

Emitted facts (all schema `1.0.0`, all `observed` confidence):

- `terraform_state_snapshot`, `terraform_state_resource`,
  `terraform_state_output`, `terraform_state_module`,
  `terraform_state_provider_binding`, `terraform_state_tag_observation`,
  `terraform_state_warning`
- `incident_routing.applied_pagerduty_resource`,
  `incident_routing.applied_alert_route`

No `incident_routing.coverage_warning`: the shared component registry
enforces one owner per fact kind and `dev.eshu.collector.pagerduty` owns
it (ops-qa installer conflict, eshu-hq/eshu#8868; partial reversal of the
#9195 EXTEND per arbiter verdict, coverage_warning only).

Explicitly excluded: `terraform_state_candidate` stays git-owned (git-core,
`core_owned`). This producer must never declare or emit it.

Pins (the relocated `eshu-sdk` pair core pins in
[#9111](https://github.com/eshu-hq/eshu/pull/9111); the grant draft cites the
same pins):

- `github.com/eshu-hq/eshu-sdk/collector v0.1.0-rc.3`
- `github.com/eshu-hq/eshu-sdk/factschema v0.1.0-rc.5`

Wire protocol: `collector-sdk/v1alpha1`. There are no `replace` directives:
a clean clone resolves both modules through the Go module proxy.

The seven bare `terraform_state_*` kinds carry no dot namespace, so the
harness only accepts them with trusted host-granted coverage: every
`conformance.Run` in this repo's tests supplies `CoreFacts` authorizations
for this producer, version, collector kind, and schema plus the
reserved-kind inventory (the #8851 host-granted-kind path, available in the
relocated SDK).

## Build and test from a clean clone

```bash
go mod tidy -diff
go build ./...
go vet ./...
go test ./... -count=1
go run ./cmd/collector --input ./testdata/complete.json
```

`go.sum` lists only the modules this repo's own imports reach. Importing the
`factschema` root package pulls in that module's own dependencies, and
`go mod tidy -diff` then fails until you run `go mod tidy` and commit the
added `go.sum` lines. The conformance script runs `go mod tidy -diff` first
so that drift fails before review.

`scripts/run-conformance.sh` runs the same gate CI runs
(`.github/workflows/ci.yml`).

## Configuration

Copy `config.example.yaml`. The collector reads one source document
(`source.input`), emits against one credential-free `sourceURI`, and tracks
freshness with `freshness.previousDigest`. All payloads pass
`ValidateShareSafeKeys`; anything sensitive must be redacted before emission
(see `collector.go` and the redaction test).

Without `source.input`, a `discovery` plus `target_scopes` activation takes
the live path: bucket filters and seeds resolve to exact S3 state objects,
target-scope allowlists bind each candidate, and reads use ambient workload
credentials only (IRSA web identity, then the process environment triple —
no credential key exists in config). Each fetched state emits join-key
facts (resource address, type, mode) plus only contracts-allowlisted drift
attribute leaves (`tfstatev1.DriftAllowlistFor`, fail-closed; the
hard-sensitive `aws_iam_policy` document is never emitted) through the
existing `Collect` contract. Missing objects degrade to partial with a
`state-missing` warning, throttles release the claim retryable, and a wrong
bucket fails the claim closed.

### S3 endpoint override (MinIO / offline)

Set `AWS_S3_ENDPOINT` on the collector process to point live S3 reads at an
S3-compatible endpoint (MinIO, an offline mirror, a VPC endpoint) instead of
the regional AWS endpoints. Unset or blank keeps the AWS default. The live
reader is built with `NewHTTPS3Reader`, so the override flows through the
real claim path, and a misconfigured value fails the claim closed at reader
construction instead of misdirecting signed reads.

Endpoint contract:

- The value is a base URL; requests address it path-style as
  `<base>/<bucket>/<key>` (ListObjectsV2 as `<base>/<bucket>`). The AWS
  default instead uses virtual-hosted addressing (`<bucket>.s3.<region>…`),
  with dotted buckets falling back to path style so TLS never breaks.
- Cosmetic trailing slashes are trimmed; an operator path prefix is kept.

Trust posture (enforced by `validateEndpointTrust`, shared with the
`AWS_STS_ENDPOINT` IRSA override; STS never takes the single-label
allowance below):

- Scheme must be `http` or `https`. Plain `http` is accepted on loopback
  (`localhost`, `127.0.0.0/8`, `::1`) for offline development, and — S3
  only — on single-label DNS names (no dots, not an IP, e.g.
  `http://minio:9000`) that resolve inside the operator's own compose or
  cluster network; every other host requires `https`.
- No credentials: any `@` (userinfo) is refused, as are query strings and
  fragments.
- No instance metadata: `169.254.0.0/16`, IPv6 link-local, and the
  well-known metadata hostnames (`metadata.google.internal`,
  `instance-data`) are refused in every scheme.
- Error text never echoes the configured value.

## Least-privilege credentials

The collector receives only a bounded claim plus local config. It never gets
Postgres, graph, queue, lease, reducer, API, or MCP handles. Source
credentials stay in the collector runtime environment and never enter fact
payloads, source URIs, logs, or statuses. URIs embedding `user:secret@host`
or `password=`/`secret=`/`token=` query shapes are refused.

## Limits

Default bounds live in `health.go` (`DefaultResourceUse`): 5000 records per
claim, 64 KiB per payload, 300 s claim timeout, 8 queued retries. Tune them
per source and record the values in the cutover issue. Past the retry bound
the collector fails terminal instead of queuing forever.

## Producer grants, revocation, and rollback

Core-owned fact kinds require a live #6709 producer grant naming this
producer, version, kind, schema, and scope. This collector emits ten
core-owned kinds, so it ships one grant per kind per schema version in
`grant.example.json` (ten entries, all producer
`dev.eshu.collector.terraform-state`, version `0.1.0`, schema `1.0.0`,
scope `terraform_state`, bounded expiry). A grant's scope is the collector
kind (`CollectorKind`, declared in `spec.collectorKinds`), not a scope ID
such as `host.scope.id`: the host matches the grant against the manifest's
collector kinds, and `grant_test.go` fails when an example drifts from them.
`grant_test.go` also locks the fail-closed matrix (wrong scope/schema/
producer refused). Revocation is a core-side grant revoke followed by replay
from the last committed generation: stop new claims, expire in-flight
leases, revoke, restore the last supported producer, replay, and verify
fact/graph/read truth. Two producers never hold one scope+generation at
once. The full issuance loops, enforcement, and rollback live in
`PRODUCER_GRANT_DRAFT.md` / `ROLLBACK_DRAFT.md` on the extraction prep
branch in `eshu-hq/eshu` until cutover; see [PORT_PLAN.md](PORT_PLAN.md).

This repo issues no grants itself: `grant.example.json` is the issuance
request shape operators submit at cutover.

## Releases

Every artifact in `manifest.yaml` is digest-pinned (`@sha256:…`, never
`:latest`). `manifest_test.go` enforces pinning plus manifest validity, and
the all-zero placeholder digest is rejected now that the component is
renamed. The current digest is a documented scaffold placeholder naming no
pullable artifact (see the comment in `manifest.yaml`): after the first
image push, resolve the real pushed digest with `scripts/pin-digest.sh`.
Base images in `Dockerfile` are pinned the same way; re-pin them when
bumping either base. `version` in `manifest.yaml` and the git tag must match
(`v0.1.0` for `0.1.0`); pushing a `v*` tag runs `release.yml`, which builds,
signs, and publishes the image plus SBOM and provenance attestations. The
workflow publishes only when the tag is exactly `v` plus the manifest
version and points at the protected `main` head; anything else fails before
anything builds or pushes.

Bare-kind gate: the `conform` path requires a granted core kind to carry a
`.` namespace prefix unless the caller supplies trusted host-granted
coverage ([#8851](https://github.com/eshu-hq/eshu/issues/8851) relocated the
SDK modules so the harness accepts it). The seven bare `terraform_state_*`
kinds therefore pass only with `CoreFacts` coverage — which this repo's
tests supply and the host supplies at install/activation. See
[PORT_PLAN.md](PORT_PLAN.md).

## Remote proof

The collector never submits results to core. Under the `process` adapter the
host starts `./collector --sdk-stdio`, writes one claim on stdin, reads one
SDK result from stdout, and commits the facts itself. The collector holds no
endpoint, queue, or datastore handle, so there is nothing for it to submit
to and no collector-side script can stand in for the host.

To prove a deployment, run the real component lifecycle against a component
home (see
[Community extension authoring](https://github.com/eshu-hq/eshu/blob/main/docs/public/extend/community-extension-authoring.md)
and
[Component producer grants](https://github.com/eshu-hq/eshu/blob/main/docs/public/reference/component-producer-grants.md)):

```bash
eshu component grant <component-id> --component-home "$HOME_DIR" \
  --version 0.1.0 --kind <each of the 10 kinds> --schema-version 1.0.0 \
  --scope terraform_state --expires-in 2160h
eshu component install manifest.yaml --component-home "$HOME_DIR" \
  --trust-mode allowlist --allow-id <component-id> --allow-publisher eshu-hq
eshu component enable <component-id> --component-home "$HOME_DIR" \
  --instance <instance> --mode scheduled --claims --config config.example.yaml
```

Then let the coordinator claim a scope and read the committed facts back
through the target core's own API and MCP surfaces, using the paths in that
core's API reference. Record the tested core version, topology, and evidence
location in the cutover issue; credential-free conformance stays in CI.
