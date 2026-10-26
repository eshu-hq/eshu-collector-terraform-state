// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

// Package collector is the standalone Eshu terraform-state collector: it
// observes Terraform state artifacts and emits the seven terraform_state_*
// fact kinds plus the three incident_routing kinds co-emitted from applied
// state (see manifest.yaml), all at schema 1.0.0 under producer
// dev.eshu.collector.terraform-state.
//
// Boundary: this package imports only the published SDK modules
// (eshu-sdk/collector, eshu-sdk/factschema). It never imports go/internal,
// another collector implementation, or any core Postgres/graph/queue handle.
// Every one of those would fail the import-boundary test and block cutover
// per #4047.
package collector
