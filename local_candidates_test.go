// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"reflect"
	"testing"
)

// The in-tree terraformstate package carries no unit tests for these pure
// helpers (they are covered there through discovery integration tests that
// need core-owned graph and scope types). These contract tests pin the
// ported behavior so a future full-port diff has a frozen baseline.
func TestCleanRelativePathNormalizesSeparators(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		`a\b\c`:        "a/b/c",
		"a//b///c":     "a/b/c",
		"./a/b":        "a/b",
		"/a/b/":        "a/b",
		"  a/b  ":      "a/b",
		"":             "",
		"terraform.tf": "terraform.tf",
	} {
		if got := cleanRelativePath(input); got != want {
			t.Errorf("cleanRelativePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizedLocalCandidateRefsDropsBlanksAndDuplicates(t *testing.T) {
	t.Parallel()

	refs := []LocalStateCandidateRef{
		{RepoID: "r1", RelativePath: "a/terraform.tfstate", TargetScopeID: "s1"},
		{RepoID: " r1 ", RelativePath: "./a/terraform.tfstate ", TargetScopeID: " s1 "},
		{RepoID: "", RelativePath: "a/terraform.tfstate"},
		{RepoID: "r1", RelativePath: ""},
		{RepoID: "r2", RelativePath: "b/terraform.tfstate"},
	}

	got := normalizedLocalCandidateRefs(refs)
	want := []LocalStateCandidateRef{
		{RepoID: "r1", RelativePath: "a/terraform.tfstate", TargetScopeID: "s1"},
		{RepoID: "r2", RelativePath: "b/terraform.tfstate"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizedLocalCandidateRefs() = %#v, want %#v", got, want)
	}
}

func TestLocalCandidateRefsContainMatchesWithoutTargetScope(t *testing.T) {
	t.Parallel()

	refs := []LocalStateCandidateRef{{RepoID: "r1", RelativePath: "a/terraform.tfstate", TargetScopeID: "s1"}}

	if !localCandidateRefsContain(refs, LocalStateCandidateRef{RepoID: "r1", RelativePath: "a/terraform.tfstate"}) {
		t.Fatal("localCandidateRefsContain() = false, want true (scope is routing metadata, not identity)")
	}
	if localCandidateRefsContain(refs, LocalStateCandidateRef{RepoID: "r1", RelativePath: "other.tfstate"}) {
		t.Fatal("localCandidateRefsContain() = true for other path, want false")
	}
	if localCandidateRefsContain(refs, LocalStateCandidateRef{RepoID: "r9", RelativePath: "a/terraform.tfstate"}) {
		t.Fatal("localCandidateRefsContain() = true for other repo, want false")
	}
}

func TestLocalCandidateRefForCandidateReturnsStoredRef(t *testing.T) {
	t.Parallel()

	stored := LocalStateCandidateRef{RepoID: "r1", RelativePath: "a/terraform.tfstate", TargetScopeID: "s1"}
	refs := []LocalStateCandidateRef{stored}

	got, ok := localCandidateRefForCandidate(refs, LocalStateCandidateRef{RepoID: "r1", RelativePath: "./a/terraform.tfstate"})
	if !ok {
		t.Fatal("localCandidateRefForCandidate() ok = false, want true")
	}
	if !reflect.DeepEqual(got, stored) {
		t.Fatalf("localCandidateRefForCandidate() = %#v, want stored %#v", got, stored)
	}
	if _, ok := localCandidateRefForCandidate(refs, LocalStateCandidateRef{RepoID: "r1", RelativePath: "missing.tfstate"}); ok {
		t.Fatal("localCandidateRefForCandidate() ok = true for missing ref, want false")
	}
}

func TestLocalStateCandidatePolicyApprovedMode(t *testing.T) {
	t.Parallel()

	if (LocalStateCandidatePolicy{Mode: LocalStateCandidateModeDiscoverOnly}).approvedMode() {
		t.Fatal("discover_only policy approvedMode() = true, want false")
	}
	if !(LocalStateCandidatePolicy{Mode: LocalStateCandidateModeApproved}).approvedMode() {
		t.Fatal("approved_candidates policy approvedMode() = false, want true")
	}
}
