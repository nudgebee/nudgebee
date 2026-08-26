package network

import (
	"regexp"
	"strings"
	"time"
)

// WHOIS outcome values reported in the task's "status" field. They let a
// consumer branch on what actually happened without regexing the raw blob:
// every TLD registry phrases "not found" differently, so that classification
// belongs here rather than in each caller.
const (
	whoisStatusFound        = "found"
	whoisStatusNotFound     = "not_found"
	whoisStatusRateLimited  = "rate_limited"
	whoisStatusParseError   = "parse_error"
	whoisStatusLookupFailed = "lookup_failed"
)

// expiryPatterns matches common labels for expiration dates in WHOIS output.
// The regex looks for the label at the start of a line (ignoring case) followed by a colon or space, and captures the rest of the line.
var expiryPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:Registry Expiry Date|Expiration Date|paid-till|expire|Expires on|Expiry date|Domain Expiration Date|Renewal date)(?:\s*:)?\s+(.*)`),
}

// notFoundPatterns match the "this domain is not registered" reply of the
// major registries. Each registry words it differently, hence the list.
var notFoundPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)no match for`),                  // Verisign .com/.net, Nominet .co.uk
	regexp.MustCompile(`(?im)^\s*not found\s*$`),            // .io, PIR .org, many Identity Digital TLDs
	regexp.MustCompile(`(?i)domain not found`),              // .io and others
	regexp.MustCompile(`(?i)no entries found`),              // .it, RIPE-style servers
	regexp.MustCompile(`(?i)%error:\s*101`),                 // RIPE "no entries found" error code
	regexp.MustCompile(`(?i)no data found`),                 // .br and others
	regexp.MustCompile(`(?i)no object found`),               // "Domain Status: No Object Found"
	regexp.MustCompile(`(?i)no such domain`),                // misc
	regexp.MustCompile(`(?i)not registered`),                // .nl, misc
	regexp.MustCompile(`(?i)is available for registration`), // misc resellers/registries
	regexp.MustCompile(`(?im)^\s*status:\s*free\s*$`),       // DENIC .de
	regexp.MustCompile(`(?i)this query returned 0 objects`), // IANA, unknown TLD
}

// rateLimitPatterns match a registry throttling us. Checked only after we fail
// to parse a record, so boilerplate that merely mentions rate limiting on an
// otherwise-good response cannot mask a real answer.
var rateLimitPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)query rate exceeded`),
	regexp.MustCompile(`(?i)exceeded the maximum allowable number`),
	regexp.MustCompile(`(?i)whois limit exceeded`),
	regexp.MustCompile(`(?i)too many requests`),
	regexp.MustCompile(`(?i)connection limit exceeded`),
	regexp.MustCompile(`(?i)quota exceeded`),
}

// labelPattern builds an anchored matcher for a set of field labels. Anchoring
// matters: a bare "Registrar" prefix match would also swallow "Registrar URL",
// "Registrar WHOIS Server" and "Registrar IANA ID". The value is captured with
// (.*) rather than (.+) so a label with no inline value still matches — that is
// the signal for the indented block form used by Nominet (.co.uk).
func labelPattern(labels ...string) *regexp.Regexp {
	quoted := make([]string, len(labels))
	for i, l := range labels {
		quoted[i] = regexp.QuoteMeta(l)
	}
	return regexp.MustCompile(`(?i)^(?:` + strings.Join(quoted, "|") + `)\s*:\s*(.*)$`)
}

var (
	domainNamePattern       = labelPattern("Domain Name", "Domain", "domain")
	registrarPattern        = labelPattern("Registrar", "Sponsoring Registrar", "registrar")
	createdPattern          = labelPattern("Creation Date", "Created On", "Created", "Registered on", "Domain Registration Date", "created", "registered")
	updatedPattern          = labelPattern("Updated Date", "Last Modified", "Last updated", "last-update", "changed", "modified")
	nameserverPattern       = labelPattern("Name servers", "Nameservers", "Name Server", "Nameserver", "nserver")
	domainStatusPattern     = labelPattern("Domain Status", "status", "state")
	registrantCountryPatern = labelPattern("Registrant Country", "country")
)

// whoisRecord holds the fields we manage to pull out of a raw WHOIS response.
type whoisRecord struct {
	DomainName        string
	Registrar         string
	Created           string
	Updated           string
	Expiry            *time.Time
	Nameservers       []string
	DomainStatus      []string
	RegistrantCountry string
}

// hasRecord reports whether anything registry-specific was parsed. This is what
// distinguishes a real record from an unparseable blob, so "found" falls out of
// the field parsing rather than needing its own list of per-registry phrases.
func (r whoisRecord) hasRecord() bool {
	return r.DomainName != "" ||
		r.Registrar != "" ||
		r.Created != "" ||
		r.Expiry != nil ||
		len(r.Nameservers) > 0 ||
		len(r.DomainStatus) > 0
}

// parseWhois extracts the structured fields from a raw WHOIS response.
func parseWhois(raw string) whoisRecord {
	lines := strings.Split(raw, "\n")

	rec := whoisRecord{
		DomainName:        firstLabeled(lines, domainNamePattern),
		Registrar:         firstLabeled(lines, registrarPattern),
		Created:           normalizeDate(firstLabeled(lines, createdPattern)),
		Updated:           normalizeDate(firstLabeled(lines, updatedPattern)),
		Expiry:            extractExpiry(raw),
		Nameservers:       normalizeNameservers(collectLabeled(lines, nameserverPattern)),
		DomainStatus:      dedupe(collectLabeled(lines, domainStatusPattern)),
		RegistrantCountry: firstLabeled(lines, registrantCountryPatern),
	}
	return rec
}

// classify decides the outcome for a response we did receive. A failed lookup
// never reaches here — the task reports whoisStatusLookupFailed itself.
//
// Order is deliberate: not-found first (those replies carry no record data),
// then a successfully parsed record, and only then rate limiting. Testing rate
// limiting last means registry boilerplate that happens to mention rate limits
// on an otherwise-valid response cannot mask a real answer.
func classify(raw string, rec whoisRecord) (registered bool, status string) {
	for _, re := range notFoundPatterns {
		if re.MatchString(raw) {
			return false, whoisStatusNotFound
		}
	}
	if rec.hasRecord() {
		return true, whoisStatusFound
	}
	for _, re := range rateLimitPatterns {
		if re.MatchString(raw) {
			return false, whoisStatusRateLimited
		}
	}
	return false, whoisStatusParseError
}

// firstLabeled returns the first value found for a label set, or "".
func firstLabeled(lines []string, re *regexp.Regexp) string {
	values := collectLabeled(lines, re)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// collectLabeled walks the response and returns every value for a label set.
// Registries use two shapes:
//
//	inline — "Registrar: MarkMonitor Inc."
//	block  — "Name servers:" followed by indented value lines (Nominet .co.uk)
func collectLabeled(lines []string, re *regexp.Regexp) []string {
	var out []string
	for i := 0; i < len(lines); i++ {
		matches := re.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if matches == nil {
			continue
		}
		if value := strings.TrimSpace(matches[1]); value != "" {
			out = append(out, value)
			continue
		}
		// Empty inline value: consume the indented block that follows.
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			// A blank line, an unindented line, or another label ends the block.
			// Test the leading whitespace specifically: comparing against the
			// fully trimmed line would misread any line with trailing spaces
			// (or a CRLF carriage return) as indented.
			if trimmed == "" || strings.TrimLeft(lines[j], " \t") == lines[j] || strings.HasSuffix(trimmed, ":") {
				break
			}
			out = append(out, trimmed)
			i = j
		}
	}
	return out
}

// normalizeNameservers keeps the host portion of each entry (some registries
// append the glue IP), lowercases it, and drops duplicates.
func normalizeNameservers(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		fields := strings.Fields(v)
		if len(fields) == 0 {
			continue
		}
		host := strings.ToLower(strings.TrimSuffix(fields[0], "."))
		if !strings.Contains(host, ".") {
			continue
		}
		out = append(out, host)
	}
	return dedupe(out)
}

// dedupe removes duplicates while preserving order. It always returns a
// non-nil slice so the field marshals as [] rather than null.
func dedupe(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// normalizeDate renders a parseable date as RFC3339 and otherwise returns the
// captured text unchanged — an unrecognised format is still useful to a reader.
func normalizeDate(value string) string {
	if value == "" {
		return ""
	}
	if t := parseDate(value); t != nil {
		return t.Format(time.RFC3339)
	}
	return value
}

// dateLayouts defines common date formats found in WHOIS responses.
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05.00Z",
	"2006-01-02T15:04:05",
	"02-Jan-2006",
	"2006-01-02",
	"Mon Jan 02 15:04:05 MST 2006",
	"2006.01.02",
	"02.01.2006",
	"02/01/2006",
}

// extractExpiry attempts to parse the domain expiration date from the raw WHOIS response.
func extractExpiry(raw string) *time.Time {
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		for _, re := range expiryPatterns {
			matches := re.FindStringSubmatch(line)
			if len(matches) > 1 {
				dateStr := strings.TrimSpace(matches[1])
				if t := parseDate(dateStr); t != nil {
					return t
				}
			}
		}
	}
	return nil
}

// parseDate tries to parse a date string using a list of known layouts.
func parseDate(dateStr string) *time.Time {
	// Clean up date string: sometimes there are comments or extra text after the date.
	// We might need heuristics here if simple parsing fails.
	// For now, try parsing the whole string, or the first token if it looks like a date.

	// First pass: try parsing the entire captured string
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, dateStr); err == nil {
			return &t
		}
	}

	// Second pass: try parsing the first word (some formats are "2023-10-26 (some comment)")
	parts := strings.Fields(dateStr)
	if len(parts) > 0 {
		firstPart := parts[0]
		for _, layout := range dateLayouts {
			if t, err := time.Parse(layout, firstPart); err == nil {
				return &t
			}
		}
	}

	return nil
}
