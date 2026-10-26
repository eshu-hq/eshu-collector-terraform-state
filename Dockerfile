# Standalone terraform-state collector image: pure-Go binary plus manifest
# and fixture, built reproducibly with -trimpath.
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# EKS nodes are linux/amd64; pin GOARCH so builds on any host (e.g. arm64
# dev boxes) produce a runnable binary. An unpinned build shipped an arm64
# image that failed with "exec format error" on ops-qa (#8868).
RUN CGO_ENABLED=0 GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/collector ./cmd/collector

# Base digests resolved 2026-09-17; re-pin with scripts/pin-digest.sh flow when
# bumping either base.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/collector /usr/local/bin/collector
COPY manifest.yaml /etc/collector/manifest.yaml
COPY testdata/complete.json /var/lib/collector/complete.json
# After the first image push, re-pin manifest.yaml with scripts/pin-digest.sh.
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/collector", "--sdk-stdio"]
