# Configuration

Start from [config.example.yaml](../../config.example.yaml) and adjust.
You have two ways to feed it state. With `source.input` set, the
collector reads one local state file. Without it, `discovery` plus
`target_scopes` takes the live S3 path: bucket filters resolve to
exact state objects, and each candidate must pass the target-scope
allow-list before it's read.

| Name | Required | Default | What it does |
|---|---|---|---|
| `host.sourceSystem` | Yes in the example | `terraform_state` | Source system recorded on facts |
| `host.scope.id` / `host.scope.kind` | Yes in the example | `component:terraform-state-primary` / `component` | Host scope for the claim |
| `process.command` / `process.args` | Yes in the example | `./collector` / `--sdk-stdio` | How the host starts this binary |
| `config.source.mode` | No | `local-file` in the example | Local-file source mode |
| `config.source.input` | For local mode | Flag default `testdata/complete.json` | State file to read |
| `config.source.sourceURI` | No | `https://example.invalid/source/terraform-state` | Credential-free source address recorded on facts |
| `config.freshness.previousDigest` | No | Empty | Prior snapshot digest for unchanged detection |
| `config.limits.maxRecordsPerClaim` | No | `5000` | Maximum facts in one claim |
| `config.limits.maxPayloadBytes` | No | `65536` | Maximum bytes in one fact payload |
| `config.limits.claimTimeoutSeconds` | No | `300` | Maximum wall-clock seconds for one claim |
| `config.discovery.backend_filters[]` | For live S3 | None | Bucket, region, backend kind, and target scope to search |
| `config.target_scopes[]` | For live S3 | None | Allow-list: provider, regions, backends, deployment mode |
| `AWS_S3_ENDPOINT` (env) | No | Unset uses AWS regional endpoints | Trusted S3-compatible base URL (MinIO or offline mirror) |
| `AWS_STS_ENDPOINT` (env) | No | Unset uses the AWS default | Endpoint for the workload-identity token exchange |

Local flags mirror the file form: `--input`, `--source-uri`,
`--previous-digest`, `--max-records`, `--max-payload-bytes`, and
`--claim-timeout-seconds`. Zero selects the default; negative
values fail.

```bash
go run ./cmd/collector --input ./testdata/complete.json
```

Endpoint overrides need care. They must be credential-free `http`
or `https` base URLs with no query or fragment.
Instance-metadata addresses are refused, plain `http` is accepted
only on loopback (and, for S3 only, single-label in-cluster names
such as `http://minio:9000`), and error text never echoes the
value. There's no credential key anywhere in config.

## Credentials and least privilege

Live S3 reads use ambient workload credentials only: IRSA web
identity first (the Kubernetes service account identity exchanged
for AWS credentials), then the standard process environment
credential variables. No credential key exists in config, and
source credentials never enter fact payloads, source URIs, logs,
or statuses. Source URIs that embed `user:secret@host` or
`password=`, `secret=`, or `token=` query values are refused.

This repo doesn't name a specific AWS policy, so grant the
workload identity only read access to the state buckets and
objects the allow-list names, and stop there.

## Claim budget

One claim is bounded by the limits above (defaults: 5,000 records,
65,536 payload bytes per fact, 300 seconds) and a retry queue
bound of 8, past which the collector fails terminal instead of
queuing forever.

---

Looking for what comes out the other side? See [Facts](facts.md).
Stuck on a value? [Troubleshoot](../guides/troubleshoot.md) covers
the failures people hit most.
