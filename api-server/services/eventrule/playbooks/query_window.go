package playbooks

import "time"

// Bounds for the observability query window an enricher derives from an event.
//
// DefaultQueryWindowMinutes doubles as the floor. Enrichers ran
// [StartedAt, EndedAt or now], which for the common case — an alert that just
// fired and has not resolved — is a couple of seconds wide by the time the
// enricher executes, because enrichment starts within ~1-9s of the event row
// being written. Measured against the dev cluster, a window that narrow gives:
//
//   - logs: nothing at all for a moderately chatty workload, or a handful of
//     lines for a busy one (fraud-detection 0, ad 1, cart 2, loki-canary 16 —
//     versus 170, 290, 1000 and 1000 over the floored window). Zero is what sent
//     log enrichment off to the k8s agent instead of the configured source.
//   - metrics: a single sample per series, which renders as a flat line rather
//     than a chart (3 samples versus 33 over the floored window).
//
// MaxQueryWindowMinutes bounds the other end: a long-firing alert (StartedAt
// days ago, EndedAt still nil) must not turn the same query into a multi-day
// backend scan.
const (
	DefaultQueryWindowMinutes = 60
	MaxQueryWindowMinutes     = 24 * 60
)

// ResolveQueryWindow computes the [start, end] window an enricher should query
// for this event. durationMinutes is the caller's own default; <1 means "use
// DefaultQueryWindowMinutes". It is the floor, not the width — an event whose
// own window is wider keeps it, up to MaxQueryWindowMinutes.
//
// The floor extends the start backwards rather than the end forwards, so the
// query always covers the lead-up to the event, which is where the evidence
// that explains it lives.
func (e PlaybookEvent) ResolveQueryWindow(durationMinutes int) (time.Time, time.Time) {
	if durationMinutes < 1 {
		durationMinutes = DefaultQueryWindowMinutes
	}

	var start, end time.Time
	if e.StartedAt != nil {
		start = *e.StartedAt
	}
	if e.EndedAt != nil {
		end = *e.EndedAt
	}
	if end.IsZero() {
		end = time.Now().UTC()
	}

	minWindow := time.Duration(durationMinutes) * time.Minute
	maxWindow := time.Duration(MaxQueryWindowMinutes) * time.Minute
	if maxWindow < minWindow {
		maxWindow = minWindow
	}

	if start.IsZero() || end.Sub(start) < minWindow {
		start = end.Add(-minWindow)
	}
	if end.Sub(start) > maxWindow {
		start = end.Add(-maxWindow)
	}
	return start, end
}
