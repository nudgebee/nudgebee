package tools

const (
	// DefaultListLimit is the page size used by every provider's List when the
	// caller leaves limit unset (<= 0). It keeps behaviour consistent so that
	// omitting limit returns the same sensible page across providers.
	DefaultListLimit = 25

	// MaxListLimit is the largest page size a caller may request. Several
	// upstream APIs (GitHub, GitLab) cap PerPage at 100 server-side, so we cap
	// here too to keep local page calculations aligned with what is returned.
	MaxListLimit = 100
)

// normalizeLimit returns a sane page size for List operations: DefaultListLimit
// when limit is unset (<= 0) and MaxListLimit when it exceeds the cap.
func normalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultListLimit
	}
	if limit > MaxListLimit {
		return MaxListLimit
	}
	return limit
}

// normalizeOffset clamps a negative offset to 0.
func normalizeOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}
