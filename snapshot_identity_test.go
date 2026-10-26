// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"strings"
	"testing"
)

// Parity with the in-tree terraformstate package: these vectors are frozen
// from TestReadSnapshotIdentityStreamsTopLevelSerialAndLineage and
// TestReadSnapshotIdentityRejectsMissingLineage. The ported
// ReadSnapshotIdentity must accept the same documents and reject the same
// malformed identity for the same reasons.
func TestReadSnapshotIdentityStreamsTopLevelSerialAndLineage(t *testing.T) {
	t.Parallel()

	// Frozen vector: the in-tree streaming test state (resource values and
	// sensitive outputs present but never retained).
	state := `{
		"format_version": "1.0",
		"outputs": {"password": {"sensitive": true, "value": "not-returned"}},
		"serial": 17,
		"resources": [{"mode":"managed","type":"aws_s3_bucket","name":"logs","instances":[]}],
		"lineage": "lineage-123"
	}`

	identity, err := ReadSnapshotIdentity(context.Background(), strings.NewReader(state))
	if err != nil {
		t.Fatalf("ReadSnapshotIdentity() error = %v, want nil", err)
	}
	if got, want := identity.Serial, int64(17); got != want {
		t.Fatalf("Serial = %d, want %d", got, want)
	}
	if got, want := identity.Lineage, "lineage-123"; got != want {
		t.Fatalf("Lineage = %q, want %q", got, want)
	}
}

func TestReadSnapshotIdentityRejectsMissingLineage(t *testing.T) {
	t.Parallel()

	// Frozen vector: serial without lineage is not an identity.
	if _, err := ReadSnapshotIdentity(context.Background(), strings.NewReader(`{"serial":17}`)); err == nil {
		t.Fatal("ReadSnapshotIdentity() error = nil, want non-nil")
	}
}

func TestReadSnapshotIdentityRejectsNegativeSerial(t *testing.T) {
	t.Parallel()

	if _, err := ReadSnapshotIdentity(context.Background(), strings.NewReader(`{"serial":-1,"lineage":"lineage-123"}`)); err == nil {
		t.Fatal("ReadSnapshotIdentity() error = nil, want non-nil for negative serial")
	}
}

func TestReadSnapshotIdentityRejectsNonObjectRoot(t *testing.T) {
	t.Parallel()

	if _, err := ReadSnapshotIdentity(context.Background(), strings.NewReader(`[]`)); err == nil {
		t.Fatal("ReadSnapshotIdentity() error = nil, want non-nil for array root")
	}
}

func TestReadSnapshotIdentityRejectsNilReader(t *testing.T) {
	t.Parallel()

	if _, err := ReadSnapshotIdentity(context.Background(), nil); err == nil {
		t.Fatal("ReadSnapshotIdentity() error = nil, want non-nil for nil reader")
	}
}
