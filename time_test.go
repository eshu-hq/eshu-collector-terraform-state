// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"testing"
	"time"
)

// The clock seam stays a zero UTC instant so generation timestamps built on
// it remain testable without wall-clock dependence.
func TestTimeZeroUTCIsZeroInstant(t *testing.T) {
	t.Parallel()

	got := timeZeroUTC()
	if !got.IsZero() {
		t.Fatalf("timeZeroUTC() = %v, want zero time", got)
	}
	if got.Location() != time.UTC {
		t.Fatalf("timeZeroUTC() location = %v, want UTC", got.Location())
	}
}
