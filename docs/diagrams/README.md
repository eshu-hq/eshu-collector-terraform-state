# Diagrams

Every diagram for this collector lives on this page, each with a
caption and a plain-text version so nothing depends on rendering.
Other pages link here instead of copying a diagram. (The repo
[README](../../README.md) keeps the first one as its overview.)

## 1. Where it sits

*Caption: source data flows into this collector, then to the Eshu
host process, then to Eshu core. The collector itself stores
nothing.*

```mermaid
flowchart LR
    S[Terraform state: local file or S3 object] --> C[This collector]
    C --> H[Eshu host process]
    H --> E[Eshu core]
```

Text version: Terraform state (local file or S3 object) -> this
collector -> Eshu host process -> Eshu core.

## 2. One claim, start to finish

*Caption: one process invocation serves exactly one claim. The host
writes the request, the collector reads the claimed scope, facts
come back on stdout, and the host validates and commits.*

```mermaid
sequenceDiagram
    participant H as Eshu host process
    participant C as This collector
    participant S as Terraform state (local file or S3 object)
    participant E as Eshu core
    H->>C: one claim on stdin (--sdk-stdio)
    C->>S: read the claimed state snapshot
    S-->>C: state JSON
    C-->>H: one result with facts on stdout
    H->>E: validate and commit the facts
```

Text version: the host writes one `collector-sdk/v1alpha1` request to
stdin, the collector reads the claimed state snapshot and builds
facts, then writes one result to stdout. The host validates and
commits.

## 3. When something goes wrong

*Caption: the failure paths. Bounded, fail-closed outcomes are
preferred over late, oversized, or silently incomplete evidence;
partial reads say so instead of pretending.*

```mermaid
flowchart TD
    S[Claim arrives] --> A{Candidate passes the target-scope allow-list?}
    A -- no --> F[Fail the claim closed]
    A -- yes --> R[Read the state object]
    R --> X{Object there?}
    X -- missing --> P[Degrade to partial with a state-missing warning]
    X -- throttled --> Q[Release the claim as retryable]
    X -- read ok --> O[Return facts on stdout]
    P --> O
```

Text version: a bucket outside the allow-list fails the claim closed.
Missing state objects degrade a live claim to partial with a
`state-missing` warning. Throttling releases the claim as
retryable, and past the retry queue bound of 8 the collector fails
terminal instead of queuing forever.
