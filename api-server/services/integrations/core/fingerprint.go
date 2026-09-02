package core

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// canonicalFingerprintPrefix marks a fingerprint that nudgebee derived from an
// alert's stable identity (rather than one the source provided). Handy when
// eyeballing the events table to see which rows were canonicalized.
const canonicalFingerprintPrefix = "nbfp:"

// CanonicalFingerprint builds a deterministic fingerprint from an alert's stable
// identity parts (e.g. source, alert-type, namespace, subject). It exists for
// sources whose native fingerprint changes on every delivery — PagerDuty's
// raw-payload fallback, Splunk's search-job id, SolarWinds' per-instance trigger
// id — where each firing of the same recurring alert would otherwise land in its
// own occurrence chain and fabricate a fake correlated incident.
//
// Parts are joined with a NUL separator so ("ab","c") and ("a","bc") never
// collide. Callers MUST pass at least one distinguishing part (alert-type or
// subject); keying on empty parts alone would collapse unrelated alerts.
func CanonicalFingerprint(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			_, _ = io.WriteString(h, "\x00")
		}
		_, _ = io.WriteString(h, p)
	}
	return canonicalFingerprintPrefix + hex.EncodeToString(h.Sum(nil))
}
