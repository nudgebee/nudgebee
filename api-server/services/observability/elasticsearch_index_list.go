package observability

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// backingIndexSuffix matches the trailing "-<yyyy.MM.dd>-<6-digit-generation>"
// of a data-stream backing index name (e.g. ".ds-logs-generic.otel-default-2026.06.18-000004").
var backingIndexSuffix = regexp.MustCompile(`-\d{4}\.\d{2}\.\d{2}-\d{6}$`)

// dataStreamsResponse models GET /_data_stream.
type dataStreamsResponse struct {
	DataStreams []struct {
		Name string `json:"name"`
	} `json:"data_streams"`
}

// backingIndexStream derives the parent data-stream name from a hidden backing
// index name. ".ds-logs-generic.otel-default-2026.06.18-000004" -> "logs-generic.otel-default".
// Returns "" if the name is not a backing index.
func backingIndexStream(index string) string {
	name := strings.TrimPrefix(index, ".ds-")
	if name == index {
		return ""
	}
	return backingIndexSuffix.ReplaceAllString(name, "")
}

// filterDataStreamNames drops system ("."-prefixed) and duplicate stream names.
// The request URL already scopes results to the requested signal type.
func filterDataStreamNames(parsed dataStreamsResponse) []string {
	seen := make(map[string]bool, len(parsed.DataStreams))
	targets := make([]string, 0, len(parsed.DataStreams))
	for _, ds := range parsed.DataStreams {
		if ds.Name == "" || strings.HasPrefix(ds.Name, ".") || seen[ds.Name] {
			continue
		}
		seen[ds.Name] = true
		targets = append(targets, ds.Name)
	}
	return targets
}

// ListAllESIndexTargets returns every stable, queryable index target across all
// signal types plus any legacy non-data-stream indices — ".ds-*" backing indices
// collapsed to their parent data-stream name, system ("."-prefixed) indices dropped.
// It applies no type-prefix filter (client clusters don't necessarily name streams
// "logs-*"/"metrics-*"), backing both the per-account index picker in the
// integration form and the logs/metrics query-editor index pickers. Data streams
// and concrete indices are unioned so both modern (data-stream) and legacy
// deployments are covered.
func ListAllESIndexTargets(cfg *ElasticsearchConfig) ([]string, error) {
	seen := make(map[string]bool)
	targets := make([]string, 0)

	// Preferred: enumerate all data streams.
	if resp, err := esRequest("GET", fmt.Sprintf("%s/_data_stream", cfg.Url), "", cfg); err == nil { //nolint:bodyclose
		if body, rErr := readResponse(resp, "elasticsearch data streams query"); rErr == nil {
			var parsed dataStreamsResponse
			if json.Unmarshal(body, &parsed) == nil {
				for _, name := range filterDataStreamNames(parsed) {
					if !seen[name] {
						seen[name] = true
						targets = append(targets, name)
					}
				}
			}
		}
	}

	// Concrete indices: collapse ".ds-*" to parent stream, pass through legacy
	// non-data-stream indices, drop system indices.
	resp, err := esRequest("GET", fmt.Sprintf("%s/_cat/indices?format=json", cfg.Url), "", cfg) //nolint:bodyclose
	if err != nil {
		if len(targets) > 0 {
			return targets, nil // data streams already succeeded
		}
		return nil, fmt.Errorf("failed to query elasticsearch indices: %w", err)
	}
	body, err := readResponse(resp, "elasticsearch indices query")
	if err != nil {
		if len(targets) > 0 {
			return targets, nil
		}
		return nil, err
	}
	var indices []map[string]any
	if err := json.Unmarshal(body, &indices); err != nil {
		if len(targets) > 0 {
			return targets, nil
		}
		return nil, fmt.Errorf("failed to unmarshal indices response: %w", err)
	}
	for _, name := range collapseAllConcreteIndexTargets(indices) {
		if !seen[name] {
			seen[name] = true
			targets = append(targets, name)
		}
	}
	return targets, nil
}

// collapseAllConcreteIndexTargets is collapseConcreteIndexTargets without the
// type-prefix filter: every ".ds-*" backing index collapses to its parent stream,
// legacy non-data-stream indices pass through, system ("."-prefixed) indices drop.
func collapseAllConcreteIndexTargets(indices []map[string]any) []string {
	seen := make(map[string]bool, len(indices))
	targets := make([]string, 0, len(indices))
	for _, idx := range indices {
		name, ok := idx["index"].(string)
		if !ok || name == "" {
			continue
		}
		if stream := backingIndexStream(name); stream != "" {
			if !strings.HasPrefix(stream, ".") && !seen[stream] {
				seen[stream] = true
				targets = append(targets, stream)
			}
			continue
		}
		if strings.HasPrefix(name, ".") || seen[name] {
			continue
		}
		seen[name] = true
		targets = append(targets, name)
	}
	return targets
}
