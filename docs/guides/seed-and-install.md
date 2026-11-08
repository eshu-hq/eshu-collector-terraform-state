# Seed and install

An init container (or you, by hand) can lay down the component
without a shell script: `seed` writes the collector
binary and [manifest.yaml](../../manifest.yaml) into `DIR`:

```bash
collector seed --dest DIR --image-ref ghcr.io/eshu-hq/eshu-collector-terraform-state@sha256:<digest>
```

Without `--image-ref`, `COLLECTOR_IMAGE_REF` supplies the
reference. The manifest pins the Linux amd64 image for version
`0.1.0` by digest:
`ghcr.io/eshu-hq/eshu-collector-terraform-state@sha256:36b51cee5abe3f7734cbcaeb79a005165a3a9694bd3a62a562799668583dd8e9`.
The image entrypoint is `/usr/local/bin/collector --sdk-stdio`
(see [Dockerfile](../../Dockerfile)).

Image references stay digest-pinned (`@sha256:...`). After a
release, record the published digest as described in
[Release](release.md), then seed activations from that digest.
First run after install? [Getting started](../getting-started/README.md)
has the one-minute version.
