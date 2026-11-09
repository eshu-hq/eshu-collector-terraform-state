# Architecture

Short version: the Eshu host hands this collector one bounded scope,
the collector reads it and hands typed facts back, and the host
validates and commits them to Eshu core. This collector stores
nothing itself. All diagrams live in
[Diagrams](../diagrams/README.md); this page is the words.

## Where it sits

Terraform state (local file or S3 object) -> this
collector -> Eshu host process -> Eshu core.

Overview diagram: [Where it sits](../diagrams/README.md#1-where-it-sits).

## One claim, start to finish

On the live S3 path there's one extra step worth knowing about:
bucket filters resolve to exact state objects, and each candidate
must pass the target-scope allow-list before the collector reads
it. Nothing outside the allow-list gets touched.

Sequence diagram:
[One claim, start to finish](../diagrams/README.md#2-one-claim-start-to-finish).

## When something goes wrong

a bucket outside the allow-list fails the claim closed.
Missing state objects degrade a live claim to partial with a
`state-missing` warning. Throttling releases the claim as
retryable, and past the retry queue bound of 8 the collector fails
terminal instead of queuing forever.

Failure-flow diagram:
[When something goes wrong](../diagrams/README.md#3-when-something-goes-wrong).

## Trust boundary

- It never writes to the Eshu graph, Postgres, or any queue.
  It imports no core storage handle; the host validates and commits.
- It never commits facts or decides final truth.
  Its manifest declares reducer phase `source_evidence_only:no_graph_truth`.
- It never runs Terraform and never writes or locks state.
- It never emits raw sensitive state values; redaction happens
  before emission and hard-sensitive documents are withheld.
