package flow_sources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"strings"
	"time"
)

const (
	// nerdGraphRelCacheNamespace is the in-memory cache for fetched relationships.
	// TTL aligned with the 1h kg_update.go publish debounce — see plan D5.
	nerdGraphRelCacheNamespace = "nr_entity_relationships"

	// nerdGraphEntitySearchPagesCap bounds the entitySearch pagination loop so a
	// runaway tenant can't stall a build. 50 pages × 200 entities per page = 10k.
	nerdGraphEntitySearchPagesCap = 50

	// nerdGraphBulkFetchBatchSize is the maximum number of GUIDs accepted by
	// actor.entities(guids: [...]) per call. NewRelic caps this at 25.
	nerdGraphBulkFetchBatchSize = 25

	// nerdGraphEntityTypeFilter is the entitySearch filter for OTel-instrumented
	// services. See plan D3 + the "out of scope" notes — broadening to include
	// APM_APPLICATION_ENTITY is deferred until a tenant with NR APM agents asks.
	nerdGraphEntityTypeFilter = "THIRD_PARTY_SERVICE_ENTITY"
)

// NerdGraphRelationship is a single CALLS edge harvested from
// `entity.relatedEntities` via NerdGraph. Both endpoints carry the entity GUID,
// name, and entityType, so the consumer can decide how to resolve them against
// local graph nodes (service-name match, FQDN parse for EBPFSERVER, etc.).
type NerdGraphRelationship struct {
	CallerGUID string
	CallerName string
	CallerType string // entityType, e.g. THIRD_PARTY_SERVICE_ENTITY
	TargetGUID string
	TargetName string
	TargetType string // entityType, e.g. EBPFSERVER, KUBERNETES_POD
}

// nerdGraphHTTPDoer is the abstraction we hit for the NerdGraph endpoint. The
// production implementation goes through common.HttpPost; tests inject a
// custom doer to redirect to httptest.Server.
type nerdGraphHTTPDoer func(url string, apiKey string, query string) (*http.Response, error)

// defaultNerdGraphHTTPDoer is the prod path. Overridable in tests.
var defaultNerdGraphHTTPDoer nerdGraphHTTPDoer = func(url, apiKey, query string) (*http.Response, error) {
	return common.HttpPost(url,
		common.HttpWithHeaders(map[string]string{
			"Content-Type": "application/json",
			"API-Key":      apiKey,
		}),
		common.HttpWithJsonBody(map[string]string{"query": query}),
	)
}

func init() {
	common.CacheCreateNamespace(nerdGraphRelCacheNamespace,
		common.CacheNamespaceWithExpiration(1*time.Hour),
		common.CacheNamespaceWithMaxEntries(500),
	)
}

// fetchNerdGraphRelationships harvests CALLS edges between OTel-instrumented
// services for one tenant + NR-account pair, with caching.
//
// Flow:
//  1. Build a cache key that incorporates the entity-type filter and an
//     apiKey fingerprint (so future filter changes or credential rotations
//     don't serve stale results).
//  2. Cache lookup. On hit, return immediately.
//  3. On miss, paginate `entitySearch` for matching GUIDs (capped at 50 pages).
//  4. Bulk-fetch via `actor.entities(guids:[...])` in batches of 25, inlining
//     `relatedEntities`.
//  5. Filter to type=CALLS, flatten into NerdGraphRelationship slice.
//  6. Populate cache, return.
//
// Returns the slice and a non-nil error only on hard fetch failure
// (HTTP/parse/transport). Empty slice + nil error means "tenant has no
// THIRD_PARTY_SERVICE_ENTITY in NR yet" — the caller decides whether to
// fall back to NRQL.
func fetchNerdGraphRelationships(
	apiKey, nrAccountID, region, tenantID string,
	logger *slog.Logger,
) ([]NerdGraphRelationship, error) {
	return fetchNerdGraphRelationshipsWith(apiKey, nrAccountID, region, tenantID, logger, defaultNerdGraphHTTPDoer)
}

// fetchNerdGraphRelationshipsWith is the testable seam — accepts an HTTP doer.
func fetchNerdGraphRelationshipsWith(
	apiKey, nrAccountID, region, tenantID string,
	logger *slog.Logger,
	doer nerdGraphHTTPDoer,
) ([]NerdGraphRelationship, error) {
	cacheKey := nerdGraphCacheKey(tenantID, nrAccountID, apiKey, nerdGraphEntityTypeFilter)
	if cached, ok := common.CacheGet(nerdGraphRelCacheNamespace, cacheKey); ok && len(cached) > 0 {
		var rels []NerdGraphRelationship
		if err := json.Unmarshal(cached, &rels); err == nil {
			logger.Debug("nerdgraph relationships served from cache",
				"tenant_id", tenantID,
				"nr_account_id", nrAccountID,
				"count", len(rels))
			return rels, nil
		}
		logger.Warn("nerdgraph cache decode failed; refetching",
			"tenant_id", tenantID, "key_prefix", cacheKey[:8])
	}

	endpoint := integrations.GetNewRelicEndpoint(region)
	url := fmt.Sprintf("https://%s/graphql", endpoint)

	guids, err := paginateEntitySearch(url, apiKey, nrAccountID, doer, logger)
	if err != nil {
		return nil, fmt.Errorf("entitySearch: %w", err)
	}
	if len(guids) == 0 {
		// Cache the empty result too — saves repeat work for the next build
		// during the 1h window. Caller treats empty as "fall back to NRQL".
		_ = cachePutRelationships(cacheKey, nil)
		return nil, nil
	}

	rels, err := bulkFetchRelationships(url, apiKey, guids, doer, logger)
	if err != nil {
		return nil, fmt.Errorf("bulk fetch: %w", err)
	}

	if err := cachePutRelationships(cacheKey, rels); err != nil {
		logger.Warn("failed to cache nerdgraph relationships", "err", err)
	}

	logger.Info("nerdgraph relationships fetched",
		"tenant_id", tenantID,
		"nr_account_id", nrAccountID,
		"guids", len(guids),
		"calls_edges", len(rels))
	return rels, nil
}

// nerdGraphCacheKey hashes the tenant scope + filter + apiKey fingerprint so
// future filter changes or credential rotations naturally invalidate. The full
// apiKey is never persisted in the key — only its first 8 hex chars of SHA-256.
func nerdGraphCacheKey(tenantID, nrAccountID, apiKey, filter string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(apiKey))
	apiKeyFP := hex.EncodeToString(h.Sum(nil))[:8]

	fh := sha256.New()
	_, _ = fh.Write([]byte(filter))
	filterFP := hex.EncodeToString(fh.Sum(nil))[:8]

	return fmt.Sprintf("%s:%s:%s:%s", tenantID, nrAccountID, filterFP, apiKeyFP)
}

func cachePutRelationships(key string, rels []NerdGraphRelationship) error {
	data, err := json.Marshal(rels)
	if err != nil {
		return err
	}
	return common.CacheSet(nerdGraphRelCacheNamespace, key, data)
}

// paginateEntitySearch walks entitySearch results until the cursor exhausts
// or the page cap is reached. Returns the discovered GUIDs.
func paginateEntitySearch(
	url, apiKey, nrAccountID string,
	doer nerdGraphHTTPDoer,
	logger *slog.Logger,
) ([]string, error) {
	guids := make([]string, 0, 256)
	cursor := ""
	for page := 0; page < nerdGraphEntitySearchPagesCap; page++ {
		query := buildEntitySearchQuery(nrAccountID, cursor)
		body, err := postAndDecodeJSON(url, apiKey, query, doer)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", page, err)
		}
		results := digEntitySearchResults(body)
		if results == nil {
			break
		}
		entities, _ := results["entities"].([]any)
		for _, e := range entities {
			ent, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if g, _ := ent["guid"].(string); g != "" {
				guids = append(guids, g)
			}
		}
		next, _ := results["nextCursor"].(string)
		if next == "" {
			return guids, nil
		}
		cursor = next
	}
	logger.Warn("nerdgraph entitySearch hit pagination cap; truncating",
		"cap", nerdGraphEntitySearchPagesCap, "guids_so_far", len(guids))
	return guids, nil
}

// bulkFetchRelationships fetches inlined relatedEntities for every GUID,
// batching at the NerdGraph 25-per-call cap. Returns flattened CALLS edges.
func bulkFetchRelationships(
	url, apiKey string,
	guids []string,
	doer nerdGraphHTTPDoer,
	logger *slog.Logger,
) ([]NerdGraphRelationship, error) {
	rels := make([]NerdGraphRelationship, 0, len(guids))
	for i := 0; i < len(guids); i += nerdGraphBulkFetchBatchSize {
		end := i + nerdGraphBulkFetchBatchSize
		if end > len(guids) {
			end = len(guids)
		}
		batch := guids[i:end]
		query := buildEntitiesBulkQuery(batch)
		body, err := postAndDecodeJSON(url, apiKey, query, doer)
		if err != nil {
			return nil, fmt.Errorf("batch %d-%d: %w", i, end, err)
		}
		entities := digBulkEntities(body)
		for _, ent := range entities {
			extractCallsFromEntity(ent, &rels)
		}
		logger.Debug("nerdgraph bulk fetch batch done",
			"batch_start", i, "batch_size", len(batch), "rels_so_far", len(rels))
	}
	return rels, nil
}

// extractCallsFromEntity walks the relatedEntities.results array on one entity
// and appends every type=CALLS edge to out. Other relationship types (MANAGES,
// CONTAINS, …) are ignored.
func extractCallsFromEntity(ent map[string]any, out *[]NerdGraphRelationship) {
	rel, _ := ent["relatedEntities"].(map[string]any)
	if rel == nil {
		return
	}
	results, _ := rel["results"].([]any)
	for _, r := range results {
		row, ok := r.(map[string]any)
		if !ok {
			continue
		}
		t, _ := row["type"].(string)
		if t != "CALLS" {
			continue
		}
		src := digRelEndpoint(row["source"])
		tgt := digRelEndpoint(row["target"])
		if src.GUID == "" || tgt.GUID == "" {
			continue
		}
		*out = append(*out, NerdGraphRelationship{
			CallerGUID: src.GUID,
			CallerName: src.Name,
			CallerType: src.EntityType,
			TargetGUID: tgt.GUID,
			TargetName: tgt.Name,
			TargetType: tgt.EntityType,
		})
	}
}

type relEndpoint struct {
	GUID, Name, EntityType string
}

func digRelEndpoint(v any) relEndpoint {
	m, ok := v.(map[string]any)
	if !ok {
		return relEndpoint{}
	}
	e, ok := m["entity"].(map[string]any)
	if !ok {
		return relEndpoint{}
	}
	guid, _ := e["guid"].(string)
	name, _ := e["name"].(string)
	et, _ := e["entityType"].(string)
	return relEndpoint{GUID: guid, Name: name, EntityType: et}
}

// digEntitySearchResults extracts the data.actor.entitySearch.results object,
// returning nil when the path is absent (graceful for malformed responses).
func digEntitySearchResults(body map[string]any) map[string]any {
	data, _ := body["data"].(map[string]any)
	actor, _ := data["actor"].(map[string]any)
	search, _ := actor["entitySearch"].(map[string]any)
	results, _ := search["results"].(map[string]any)
	return results
}

// digBulkEntities extracts the data.actor.entities array from a bulk-fetch
// response. Returns nil when absent.
func digBulkEntities(body map[string]any) []map[string]any {
	data, _ := body["data"].(map[string]any)
	actor, _ := data["actor"].(map[string]any)
	rawList, _ := actor["entities"].([]any)
	out := make([]map[string]any, 0, len(rawList))
	for _, x := range rawList {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// postAndDecodeJSON sends a GraphQL query and decodes the JSON response body.
// Surfaces non-2xx responses as errors so callers can distinguish transport
// failures from "0 results" cleanly.
func postAndDecodeJSON(url, apiKey, query string, doer nerdGraphHTTPDoer) (map[string]any, error) {
	resp, err := doer(url, apiKey, query)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	var body map[string]any
	if err := json.Unmarshal(respBody, &body); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// buildEntitySearchQuery constructs a paginated entitySearch GraphQL query.
// nrAccountID is interpolated as-is — it must already be validated as numeric
// by integrations.ExecuteNRQL's existing guard or the integration save path.
func buildEntitySearchQuery(nrAccountID, cursor string) string {
	cursorClause := ""
	if cursor != "" {
		// Escape any double-quote in the cursor before interpolation. NewRelic
		// cursors are opaque base64-ish strings in practice, but we don't
		// trust the response.
		safe := strings.ReplaceAll(cursor, `"`, `\"`)
		cursorClause = fmt.Sprintf(`, cursor: "%s"`, safe)
	}
	return fmt.Sprintf(`{
  actor {
    entitySearch(query: "accountId = %s AND entityType = '%s'") {
      results%s {
        entities { guid name }
        nextCursor
      }
    }
  }
}`, nrAccountID, nerdGraphEntityTypeFilter, cursorClause)
}

// buildEntitiesBulkQuery constructs an actor.entities(guids: [...]) query
// with inline relatedEntities selection on the ThirdPartyServiceEntity type.
func buildEntitiesBulkQuery(guids []string) string {
	quoted := make([]string, 0, len(guids))
	for _, g := range guids {
		// GUIDs from NerdGraph are base64; we still escape defensively.
		safe := strings.ReplaceAll(g, `"`, `\"`)
		quoted = append(quoted, fmt.Sprintf(`"%s"`, safe))
	}
	return fmt.Sprintf(`{
  actor {
    entities(guids: [%s]) {
      name guid type entityType
      ... on ThirdPartyServiceEntity {
        relatedEntities(filter: {direction: BOTH}) {
          results {
            type
            source { entity { name guid type entityType } }
            target { entity { name guid type entityType } }
          }
        }
      }
    }
  }
}`, strings.Join(quoted, ", "))
}
