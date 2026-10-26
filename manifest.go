// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	_ "embed"
)

// ManifestYAML is the embedded collector manifest (manifest.yaml) that the
// seed installer stamps with the deployed image reference at install time.
//
//go:embed manifest.yaml
var ManifestYAML []byte
