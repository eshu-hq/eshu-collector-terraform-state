# Concepts

The host points this collector at Terraform state, either one local
file or state objects in S3. The collector reads the snapshot,
redacts anything sensitive, and hands back facts about what
Terraform last applied. It never runs Terraform and stores nothing
itself.

Some words up front. Terraform state is the file Terraform writes
after a run to remember what it created. A *fact* is one small,
typed piece of evidence. A *claim* is one bounded request to
collect one scope. The *host* is the Eshu process that starts this
collector, sends it a claim, and stores the result.

## When to use it

Use it when Eshu needs evidence of what Terraform applied: the
resources, outputs, modules, provider bindings, and tags recorded
in one or more state snapshots, plus PagerDuty routing resources
found in applied state.

## When not to use it

- Don't use it to run, plan, or apply Terraform. It only reads state.
- Don't use it as a state browser for humans.
  It returns facts to the Eshu host; it has no user interface.
- Don't expect it to show live cloud resources.
  State can be stale; this collector reports what the state
  records, not what the cloud has now.
- Don't feed it state you can't let it read.
  It redacts before emission, but the process still reads the file.

## What you get

The full fact table lives in [Facts](../reference/facts.md), which
mirrors `spec.emittedFacts` in [manifest.yaml](../../manifest.yaml).

Two kinds you might expect here live elsewhere. This collector
never declares or emits `terraform_state_candidate`; git-based
collection in Eshu core owns that kind. It also doesn't emit
`incident_routing.coverage_warning`, which the PagerDuty collector
owns.

State files can hold secrets, so treat the input with care.
Sensitive attribute values are redacted or withheld before
emission; only allow-listed drift attribute leaves are emitted,
and the hard-sensitive `aws_iam_policy` document is never emitted.

## Boundaries: what this never does

- It never writes to the Eshu graph, Postgres, or any queue.
  It imports no core storage handle; the host validates and commits.
- It never commits facts or decides final truth.
  Its manifest declares reducer phase `source_evidence_only:no_graph_truth`.
- It never runs Terraform and never writes or locks state.
- It never emits raw sensitive state values; redaction happens
  before emission and hard-sensitive documents are withheld.

For the flow behind these boundaries, see
[Architecture](../architecture/README.md) and
[Diagrams](../diagrams/README.md).
