// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	collector "github.com/eshu-hq/eshu-collector-terraform-state"
)

// Regression: `collector seed --dest DIR` must install the binary and
// manifest instead of falling through to a claim run. Before the seed
// dispatch existed, the seed init container failed with
// "open testdata/complete.json: permission denied" on ops-qa.
func TestRunSeedDispatchInstallsBinaryAndManifest(t *testing.T) {
	dest := t.TempDir()
	var stdout, stderr strings.Builder
	if err := run([]string{"seed", "--dest", dest}, nil, &stdout, &stderr); err != nil {
		t.Fatalf("seed dispatch failed: %v", err)
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
	// No reference given: the embedded manifest is written unchanged.
	if !bytes.Equal(manifest, collector.ManifestYAML) {
		t.Fatal("seed without a reference rewrote the manifest")
	}
	if !strings.HasPrefix(stdout.String(), "seeded ") {
		t.Fatalf("seed did not report the seeded binary path: %q", stdout.String())
	}
}

func TestRunSeedStampsDigestPinnedRef(t *testing.T) {
	ref := "ghcr.io/eshu-hq/eshu-collector-terraform-state@sha256:a5729d540c771239836ba08da7c590630813ca7d19cb128045b64c2b886da157"
	dest := t.TempDir()
	var stdout, stderr strings.Builder
	if err := run([]string{"seed", "--dest", dest, "--image-ref", ref}, nil, &stdout, &stderr); err != nil {
		t.Fatalf("seed with image-ref failed: %v", err)
	}
	manifest, readErr := os.ReadFile(filepath.Join(dest, "manifest.yaml"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(manifest), ref) {
		t.Fatal("seeded manifest does not stamp the image reference")
	}
}

func TestRunSeedReadsRefFromEnv(t *testing.T) {
	ref := "ghcr.io/eshu-hq/eshu-collector-terraform-state@sha256:a5729d540c771239836ba08da7c590630813ca7d19cb128045b64c2b886da157"
	dest := t.TempDir()
	var stdout strings.Builder
	getenv := func(key string) string {
		if key == "COLLECTOR_IMAGE_REF" {
			return ref
		}
		return ""
	}
	if err := runSeed([]string{"--dest", dest}, &stdout, getenv); err != nil {
		t.Fatalf("seed with env ref failed: %v", err)
	}
	manifest, readErr := os.ReadFile(filepath.Join(dest, "manifest.yaml"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(manifest), ref) {
		t.Fatal("seeded manifest does not stamp the env image reference")
	}
}

func TestRunSeedRejectsBadImageRef(t *testing.T) {
	for _, ref := range []string{"not-a-digest", "ghcr.io/eshu-hq/eshu-collector-aws-cloud:0.1.0", "ghcr.io/eshu-hq/eshu-collector-aws-cloud@sha256:xyz"} {
		dest := t.TempDir()
		var stdout, stderr strings.Builder
		if err := run([]string{"seed", "--dest", dest, "--image-ref", ref}, nil, &stdout, &stderr); err == nil {
			t.Fatalf("seed accepted malformed image reference %q", ref)
		}
		if _, statErr := os.Stat(filepath.Join(dest, "collector")); !os.IsNotExist(statErr) {
			t.Fatalf("seed wrote a binary for malformed reference %q", ref)
		}
	}
}

func TestRunSeedRequiresDest(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run([]string{"seed"}, nil, &stdout, &stderr); err == nil {
		t.Fatal("seed without --dest succeeded")
	}
}
