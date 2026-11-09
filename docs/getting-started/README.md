# Getting started

Hi! In about a minute you can watch this collector turn
Terraform state snapshots, from a local file or S3 objects into facts for Eshu. It only collects and
returns evidence; the Eshu host validates the result and stores it.
Nothing is stored (or changed) here.

## Run it once

```bash
go run ./cmd/collector --input ./testdata/complete.json
```

That reads the checked-in state fixture. It never runs Terraform; remember that state can be stale, so read the facts as "what Terraform last applied."

## What just happened?

The host runs one *claim* (one bounded request to collect one scope)
per process invocation: one `collector-sdk/v1alpha1` request on
stdin, one result full of typed *facts* on stdout. Eshu can then
answer which resources, modules, and outputs a state snapshot records. The picture and the step-by-step flow live
in [Diagrams](../diagrams/README.md) and
[Architecture](../architecture/README.md).

## Where next?

- Every knob: [Configuration](../reference/configuration.md)
- What comes out: [Facts](../reference/facts.md)
- Day-2 work: [Guides](../guides/README.md)
- New words and boundaries: [Concepts](../concepts/README.md)
