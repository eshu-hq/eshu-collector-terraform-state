// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import "time"

func timeZeroUTC() time.Time {
	return time.Time{}.UTC()
}
