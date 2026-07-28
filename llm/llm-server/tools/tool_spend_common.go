package tools

import (
	"fmt"
	"strconv"
	"strings"
)

// spendWindowDefaultDays is the fallback lookback window (in days) used by the
// spend tools when the caller does not specify a window.
const spendWindowDefaultDays = 30

// spendWindowMaxDays bounds the lookback window. Spend data is retained well
// beyond a year, but a 365-day cap keeps a single ad-hoc query from scanning an
// unbounded range; anything larger should use a dedicated reporting path.
const spendWindowMaxDays = 365

// parseWindowDays parses a "{N}d" window string (e.g. "7d", "15d", "90d") and
// returns the number of days. An empty window returns the default (30) with no
// error. Invalid formats and out-of-range values (outside 1–365) return an
// error so the tool can surface it to the LLM for self-correction rather than
// silently falling back to a different window.
func parseWindowDays(window string) (int, error) {
	if window == "" {
		return spendWindowDefaultDays, nil
	}
	if !strings.HasSuffix(window, "d") {
		return 0, fmt.Errorf("invalid window %q: must be in the form {N}d (e.g. '15d', '30d')", window)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(window, "d"))
	if err != nil || n < 1 || n > spendWindowMaxDays {
		return 0, fmt.Errorf("invalid window %q: days must be an integer between 1 and %d", window, spendWindowMaxDays)
	}
	return n, nil
}
