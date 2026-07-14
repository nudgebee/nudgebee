package recommendation

import "time"

// WastedSinceDetected returns the estimated spend already lost by leaving a
// recommendation open: monthly savings prorated over the days since detection.
// Same formula as the reports service's opportunity-lost aggregate, but
// uncapped — per-item display wants the true accrued figure.
func WastedSinceDetected(estimatedSavings float64, createdAt time.Time, now time.Time) float64 {
	if estimatedSavings <= 0 || createdAt.IsZero() {
		return 0
	}
	days := now.Sub(createdAt).Hours() / 24
	if days <= 0 {
		return 0
	}
	return estimatedSavings * days / 30
}
