// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import "testing"

// Parity with the in-tree terraformstate package: the (0, 0, 0) tuple is the
// frozen #8329 theory shape (resources:[] with outputs absent parses to zero
// body facts), and any content-bearing tuple must not count as legitimately
// empty.
func TestIsLegitimatelyEmptyBodyAcceptsZeroBodyFacts(t *testing.T) {
	t.Parallel()

	if !IsLegitimatelyEmptyBody(0, 0, 0) {
		t.Fatal("IsLegitimatelyEmptyBody(0, 0, 0) = false, want true")
	}
}

func TestIsLegitimatelyEmptyBodyRejectsContentBearingStates(t *testing.T) {
	t.Parallel()

	for _, counts := range [][3]int64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}, {2, 3, 4}} {
		if IsLegitimatelyEmptyBody(counts[0], counts[1], counts[2]) {
			t.Fatalf("IsLegitimatelyEmptyBody(%d, %d, %d) = true, want false", counts[0], counts[1], counts[2])
		}
	}
}
