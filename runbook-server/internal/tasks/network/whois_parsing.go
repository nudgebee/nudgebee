package network

import (
	"regexp"
	"strings"
	"time"
)

// expiryPatterns matches common labels for expiration dates in WHOIS output.
// The regex looks for the label at the start of a line (ignoring case) followed by a colon or space, and captures the rest of the line.
var expiryPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:Registry Expiry Date|Expiration Date|paid-till|expire|Expires on|Expiry date|Domain Expiration Date|Renewal date)(?:\s*:)?\s+(.*)`),
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
