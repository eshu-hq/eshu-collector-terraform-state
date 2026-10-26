// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression: `collector seed --dest DIR --image-ref REF` must install the
// binary and manifest instead of falling through to a claim run. Before the
// seed dispatch existed, the seed init container failed on missing fixture
// input on ops-qa.
func TestRunSeedDispatchInstallsBinaryAndManifest(t *testing.T) {
	dest := t.TempDir()
	ref := "ghcr.io/eshu-hq/eshu-collector-terraform-state@sha256:ae5bfbc85790360a2837886a3c5157b03448d3cc4358a2134f0e2a51f22170f6"
	var stdout, stderr strings.Builder
	err := run([]string{"seed", "--dest", dest, "--image-ref", ref}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("seed dispatch failed: %v (stderr: %s)", err, stderr.String())
	}
	for _, name := range []string{"collector", "manifest.yaml"} {
		info, statErr := os.Stat(filepath.Join(dest, name))
		if statErr != nil {
			t.Fatalf("seed did not write %s: %v", name, statErr)
		}
		if info.IsDir() {
			t.Fatalf("seed wrote %s as a directory", name)
		}
	}
	manifest, readErr := os.ReadFile(filepath.Join(dest, "manifest.yaml"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(manifest), ref) {
		t.Fatalf("seeded manifest does not stamp the image reference")
	}
	if !strings.Contains(stdout.String(), "seeded ") {
		t.Fatalf("seed did not report the seeded binary path: %q", stdout.String())
	}
}

func TestRunSeedRejectsBadImageRef(t *testing.T) {
	var stdout, stderr strings.Builder
	err := run([]string{"seed", "--dest", t.TempDir(), "--image-ref", "not-a-digest"}, nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("seed accepted a malformed image reference")
	}
}

func TestRunSeedRequiresDest(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run([]string{"seed"}, nil, &stdout, &stderr); err == nil {
		t.Fatal("seed without --dest succeeded")
	}
}
