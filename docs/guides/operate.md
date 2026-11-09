# Operate

## How it gets run

The host runs one claim per process invocation. It starts
`./collector --sdk-stdio`, writes one `collector-sdk/v1alpha1`
request to stdin, and reads one result from stdout. The host owns
admission, identity, storage, and recovery. This binary never
commits facts itself and holds no endpoint, queue, or datastore
handle to submit anything to.

```bash
go run ./cmd/collector --sdk-stdio < request.json > result.json
```

Installing it first? See [Seed and install](seed-and-install.md).

## What to watch

Each run writes one `collector health:` JSON line to stderr with
liveness, last success time and digest, failure count and reason,
retry depth, and the effective limits. Health text carries no
credentials or payloads. The manifest declares the metric prefix
`eshu_dp_tfstate_`; the stderr health line is the operator signal
proven by the code here.

## Dependencies

| Dependency | Version or contract | Where it's pinned |
|---|---|---|
| `github.com/eshu-hq/eshu-sdk/collector` | `v0.1.0-rc.3` | `go.mod` |
| `github.com/eshu-hq/eshu-sdk/factschema` | `v0.1.0-rc.8` | `go.mod` |
| Host protocol | `collector-sdk/v1alpha1`, `process` adapter | [manifest.yaml](../../manifest.yaml) |
| Eshu core compatibility | `>=0.0.5 <0.2.0` | [manifest.yaml](../../manifest.yaml) |
| Producer identity | `dev.eshu.collector.terraform-state`, version `0.1.0` | [manifest.yaml](../../manifest.yaml) |

There are no `replace` directives; a clean clone resolves both SDK
modules through the Go module proxy. Producer grants are issued by
Eshu core, not this repo. [grant.example.json](../../grant.example.json)
is the request shape: 9 entries, one per emitted kind at schema
`1.0.0`, scope `terraform_state`.
