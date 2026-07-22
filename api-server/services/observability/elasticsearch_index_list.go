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

// listESIndexTargets returns the stable, queryable index targets for a signal
// type ("logs", "metrics") on an Elasticsearch backend.
//
// Modern deployments store telemetry in data streams ({type}-{dataset}-{namespace},
// e.g. "metrics-kubeletstatsreceiver.otel-default") whose physical storage is a set
// of rolled-over ".ds-*" backing indices. Listing the backing indices directly is
// wrong: each is one rollover generation, so pinning a query to ".ds-...-000001"
// silently drops everything that has since rolled into a newer generation, and the
// picker shows N confusing rows for one logical stream. This returns the stable
// data-stream name instead.
//
// Resolution order:
//  1. GET /_data_stream/<type>-* — the canonical, type-scoped list (preferred).
//  2. Fallback (no data streams matched, or the API is unavailable): enumerate
//     concrete indices via _cat/indices, collapsing ".ds-*" backing indices to
//     their parent stream name (type-filtered) and passing through legacy
//     non-data-stream indices, while excluding system indices ("."-prefixed).
//     This preserves behaviour for pre-data-stream (e.g. Fluent-Bit) deployments.
func listESIndexTargets(typePrefix string, cfg *ElasticsearchConfig) ([]string, error) {
	streams, dsErr := listESDataStreams(typePrefix, cfg)
	if dsErr == nil && len(streams) > 0 {
		return streams, nil
	}
	return listESConcreteIndexTargets(typePrefix, cfg, dsErr)
}

// listESDataStreams returns data-stream names matching "<typePrefix>-*".
func listESDataStreams(typePrefix string, cfg *ElasticsearchConfig) ([]string, error) {
	resp, err := esRequest("GET", fmt.Sprintf("%s/_data_stream/%s-*", cfg.Url, typePrefix), "", cfg) //nolint:bodyclose
	if err != nil {
		return nil, fmt.Errorf("failed to query elasticsearch data streams: %w", err)
	}
	bodyBytes, err := readResponse(resp, "elasticsearch data streams query")
	if err != nil {
		return nil, err
	}

	var parsed dataStreamsResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data streams response: %w", err)
	}
	return filterDataStreamNames(parsed), nil
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

// listESConcreteIndexTargets enumerates _cat/indices and reduces it via
// collapseConcreteIndexTargets. dsErr is the error (if any) from the preferred
// data-stream path, surfaced only if this fallback also fails.
func listESConcreteIndexTargets(typePrefix string, cfg *ElasticsearchConfig, dsErr error) ([]string, error) {
	resp, err := esRequest("GET", fmt.Sprintf("%s/_cat/indices?format=json", cfg.Url), "", cfg) //nolint:bodyclose
	if err != nil {
		return nil, joinIndexListErr(dsErr, fmt.Errorf("failed to query elasticsearch indices: %w", err))
	}
	bodyBytes, err := readResponse(resp, "elasticsearch indices query")
	if err != nil {
		return nil, joinIndexListErr(dsErr, err)
	}

	var indices []map[string]any
	if err := json.Unmarshal(bodyBytes, &indices); err != nil {
		return nil, joinIndexListErr(dsErr, fmt.Errorf("failed to unmarshal indices response: %w", err))
	}
	return collapseConcreteIndexTargets(typePrefix, indices), nil
}

// collapseConcreteIndexTargets reduces a _cat/indices listing to stable targets:
// ".ds-*" backing indices collapse to their parent data-stream name (kept only
// for the requested signal type, so metrics don't leak into the logs picker and
// vice-versa); legacy non-data-stream indices pass through; system indices
// ("."-prefixed) are dropped.
func collapseConcreteIndexTargets(typePrefix string, indices []map[string]any) []string {
	prefix := typePrefix + "-"
	seen := make(map[string]bool, len(indices))
	targets := make([]string, 0, len(indices))
	for _, idx := range indices {
		name, ok := idx["index"].(string)
		if !ok || name == "" {
			continue
		}
		if stream := backingIndexStream(name); stream != "" {
			if strings.HasPrefix(stream, prefix) && !seen[stream] {
				seen[stream] = true
				targets = append(targets, stream)
			}
			continue
		}
		if strings.HasPrefix(name, ".") || seen[name] {
			continue // system index, or already added
		}
		seen[name] = true
		targets = append(targets, name)
	}
	return targets
}

// joinIndexListErr combines the optional preferred-path error with the fallback error.
func joinIndexListErr(primary, fallback error) error {
	if primary == nil {
		return fallback
	}
	return fmt.Errorf("%w; data stream listing also failed: %v", fallback, primary)
}
