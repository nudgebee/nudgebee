package event

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The evidence types an automation can attach. This set is exactly what the
// Investigate page knows how to draw, so a card that passes validation here
// always appears. Anything else is refused at run time rather than stored as a
// card that silently never renders — the defect this form replaces.
const (
	EvidenceTypeMarkdown = "markdown"
	EvidenceTypeJson     = "json"
	EvidenceTypeTable    = "table"
)

// authoredEvidenceRenderer is the dispatch key app/src/pages/investigate.jsx
// matches to pick a card for automation-written evidence. One key for every
// type, with the page switching on `type` from there: the alternative is each
// type borrowing the action name of an enricher that happens to render the same
// way, which is how a missing `text_enricher` silently blanked a card before.
const authoredEvidenceRenderer = "workflow_evidence"

const defaultEvidenceSeverity = "Info"
const defaultEvidenceTitle = "Automation Evidence"

var evidenceSeverities = []string{"Info", "High", "Critical"}

var evidenceFilenameUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// AuthoredEvidence is one card written by an automation, as the
// events.add_evidence task sends it.
type AuthoredEvidence struct {
	Type     string   `json:"type"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Severity string   `json:"severity"`
	Content  string   `json:"content"`
	JsonData string   `json:"json_data"`
	Headers  []string `json:"headers"`
	Rows     string   `json:"rows"`
}

// BuildAuthoredEvidence turns the fields an automation author filled in into the
// evidence element the Investigate page renders.
//
// The envelope's keys are not interchangeable, and each is read by something:
//   - additional_info.actual_action_name is the dispatch key the page switches
//     on first, and `type` is what it switches on second.
//   - title is read top-level by the markdown card, and from data.table_name by
//     the table card.
//   - data is a markdown string, a JSON string, or the table's {headers, rows}
//     bag, depending on the type — the card for each reads a different shape.
//   - insight is the highlight, present only when the author wrote one;
//     additional_info.severity carries the severity either way.
func BuildAuthoredEvidence(input AuthoredEvidence) (map[string]any, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = defaultEvidenceTitle
	}

	severity := strings.TrimSpace(input.Severity)
	if severity == "" {
		severity = defaultEvidenceSeverity
	}
	if !slices.Contains(evidenceSeverities, severity) {
		return nil, fmt.Errorf("event: severity must be one of: %s", strings.Join(evidenceSeverities, ", "))
	}

	evidence := map[string]any{
		"type":  input.Type,
		"title": title,
		"additional_info": map[string]any{
			"actual_action_name": authoredEvidenceRenderer,
			"title":              title,
			"severity":           severity,
		},
	}

	// An insight is a highlight: the event page lifts every one of them into its
	// own insights panel, and the RCA prompt reads them. So one is emitted only
	// when the author actually wrote a highlight — echoing the card's title into
	// that panel, as an earlier version did, turns every attached card into a
	// finding that says nothing.
	if summary := strings.TrimSpace(input.Summary); summary != "" {
		evidence["insight"] = []any{map[string]any{
			"message":  summary,
			"severity": severity,
		}}
	}

	switch input.Type {
	case EvidenceTypeMarkdown:
		body := strings.TrimSpace(input.Content)
		if body == "" {
			return nil, fmt.Errorf("event: content is required for %s evidence", EvidenceTypeMarkdown)
		}
		evidence["data"] = body
		evidence["filename"] = evidenceFilename(title) + ".md"

	case EvidenceTypeJson:
		data, err := normalizeAuthoredJson(input.JsonData)
		if err != nil {
			return nil, err
		}
		// The card parses this back out of a string, so it is stored as text
		// rather than as a nested object.
		evidence["data"] = data
		evidence["filename"] = evidenceFilename(title) + ".json"

	case EvidenceTypeTable:
		rows, err := authoredTableRows(input.Rows)
		if err != nil {
			return nil, err
		}
		headers := []any{}
		for _, header := range input.Headers {
			if trimmed := strings.TrimSpace(header); trimmed != "" {
				headers = append(headers, trimmed)
			}
		}
		if len(headers) == 0 {
			return nil, fmt.Errorf("event: headers are required for %s evidence", EvidenceTypeTable)
		}
		evidence["data"] = map[string]any{
			"headers": headers,
			"rows":    rows,
			// The card reads the heading from here, and calls .includes on it,
			// so it must always be a string.
			"table_name":       title,
			"column_renderers": map[string]any{},
		}

	default:
		return nil, fmt.Errorf("event: %q is not an evidence type that can be displayed (expected one of: %s)",
			input.Type, strings.Join([]string{EvidenceTypeMarkdown, EvidenceTypeJson, EvidenceTypeTable}, ", "))
	}

	return evidence, nil
}

// normalizeAuthoredJson checks the author's JSON and returns it compacted. Only
// an object or an array is accepted: the card renders entries as key/value
// rows, so a bare string or number would draw an empty card.
func normalizeAuthoredJson(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("event: json_data is required for %s evidence", EvidenceTypeJson)
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return "", fmt.Errorf("event: json_data is not valid JSON: %w", err)
	}
	// Empty is rejected for the same reason an empty table is: it draws a card
	// with a title and nothing under it, which is the "attached but shows
	// nothing" outcome this form exists to prevent.
	switch value := parsed.(type) {
	case map[string]any:
		if len(value) == 0 {
			return "", fmt.Errorf("event: json_data must not be empty")
		}
	case []any:
		if len(value) == 0 {
			return "", fmt.Errorf("event: json_data must not be empty")
		}
	default:
		return "", fmt.Errorf("event: json_data must be a JSON object or array")
	}
	compacted, err := json.Marshal(parsed)
	if err != nil {
		return "", fmt.Errorf("event: unable to store json_data: %w", err)
	}
	return string(compacted), nil
}

// authoredTableRows checks the author's rows. The table card reads each row by
// column index and destructures it, so every row has to be an array.
func authoredTableRows(raw string) ([]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("event: rows are required for %s evidence", EvidenceTypeTable)
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, fmt.Errorf("event: rows are not valid JSON: %w", err)
	}
	rows, ok := parsed.([]any)
	if !ok {
		return nil, fmt.Errorf("event: rows must be a JSON array of arrays, for example [[\"pod-a\", \"CrashLoopBackOff\"]]")
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("event: rows must not be empty")
	}
	for i, row := range rows {
		cells, ok := row.([]any)
		if !ok {
			return nil, fmt.Errorf("event: row %d must be an array of cells, for example [\"pod-a\", \"CrashLoopBackOff\"]", i+1)
		}
		// The table card puts each cell straight into the page. A nested object
		// or array cannot be rendered as a React child: it throws, and takes the
		// whole event page down with it for everyone who opens that event.
		for j, cell := range cells {
			switch cell.(type) {
			case map[string]any, []any:
				encoded, err := json.Marshal(cell)
				if err != nil {
					return nil, fmt.Errorf("event: row %d, cell %d cannot be displayed: %w", i+1, j+1, err)
				}
				cells[j] = string(encoded)
			}
		}
	}
	return rows, nil
}

func evidenceFilename(title string) string {
	slug := evidenceFilenameUnsafe.ReplaceAllString(strings.ToLower(title), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "evidence"
	}
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	return slug
}
