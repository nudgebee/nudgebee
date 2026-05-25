package flow_sources

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// ---- HTTP doer fixtures -----------------------------------------------------

// stubDoer returns canned responses keyed by query-substring match. Each entry
// is consumed in order (FIFO) so tests can encode "page 1 then page 2" flows.
type stubDoer struct {
	calls atomic.Int32

	// queue is matched in FIFO order regardless of payload. Convenient when the
	// test doesn't care which exact query each call carries.
	queue []stubResponse

	// captured stores the queries we've been asked for, in order. Tests use
	// this to assert batching/pagination shape.
	captured []string
}

type stubResponse struct {
	status int
	body   string
}

func (s *stubDoer) Do(_ string, _ string, query string) (*http.Response, error) {
	s.calls.Add(1)
	s.captured = append(s.captured, query)
	if len(s.queue) == 0 {
		return jsonResp(200, `{"data":{"actor":{}}}`), nil
	}
	r := s.queue[0]
	s.queue = s.queue[1:]
	return jsonResp(r.status, r.body), nil
}

func (s *stubDoer) doer() nerdGraphHTTPDoer { return s.Do }

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// ---- entitySearch response helpers ------------------------------------------

func entitySearchPage(guids []string, cursor string) string {
	parts := make([]string, 0, len(guids))
	for i, g := range guids {
		parts = append(parts, fmt.Sprintf(`{"guid":"%s","name":"svc-%d"}`, g, i))
	}
	cursorJSON := "null"
	if cursor != "" {
		cursorJSON = fmt.Sprintf(`"%s"`, cursor)
	}
	return fmt.Sprintf(`{"data":{"actor":{"entitySearch":{"results":{"entities":[%s],"nextCursor":%s}}}}}`,
		strings.Join(parts, ","), cursorJSON)
}

// bulkEntitiesResponse builds a faked actor.entities(...) response. Each entry
// is one entity with the given relatedEntities CALLS edges to the listed targets.
type stubEdge struct {
	relType    string // "CALLS", "MANAGES", etc.
	sourceGUID string
	sourceName string
	sourceType string
	targetGUID string
	targetName string
	targetType string
}

func bulkEntitiesResponse(entities []stubBulkEntity) string {
	entJSON := make([]string, 0, len(entities))
	for _, e := range entities {
		edgeJSON := make([]string, 0, len(e.edges))
		for _, ed := range e.edges {
			edgeJSON = append(edgeJSON, fmt.Sprintf(
				`{"type":"%s","source":{"entity":{"guid":"%s","name":"%s","entityType":"%s","type":"SERVICE"}},"target":{"entity":{"guid":"%s","name":"%s","entityType":"%s","type":"SERVICE"}}}`,
				ed.relType,
				ed.sourceGUID, ed.sourceName, ed.sourceType,
				ed.targetGUID, ed.targetName, ed.targetType,
			))
		}
		entJSON = append(entJSON, fmt.Sprintf(
			`{"guid":"%s","name":"%s","entityType":"THIRD_PARTY_SERVICE_ENTITY","type":"SERVICE","relatedEntities":{"results":[%s]}}`,
			e.guid, e.name, strings.Join(edgeJSON, ","),
		))
	}
	return fmt.Sprintf(`{"data":{"actor":{"entities":[%s]}}}`, strings.Join(entJSON, ","))
}

type stubBulkEntity struct {
	guid  string
	name  string
	edges []stubEdge
}

// ---- tests ------------------------------------------------------------------

func TestNerdGraphFetcher_PaginatesEntitySearch(t *testing.T) {
	stub := &stubDoer{
		queue: []stubResponse{
			{200, entitySearchPage([]string{"g1", "g2"}, "cursor-2")},
			{200, entitySearchPage([]string{"g3"}, "")},
			{200, bulkEntitiesResponse([]stubBulkEntity{
				{guid: "g1", name: "svc-a", edges: nil},
				{guid: "g2", name: "svc-b", edges: nil},
				{guid: "g3", name: "svc-c", edges: nil},
			})},
		},
	}
	rels, err := fetchNerdGraphRelationshipsWith(
		"key-paginate", "8031798", "us", "tenant-paginate", slog.Default(), stub.doer())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 CALLS edges (no edges in stubs), got %d", len(rels))
	}
	// 2 entitySearch pages + 1 bulk fetch = 3 calls
	if got := stub.calls.Load(); got != 3 {
		t.Errorf("expected 3 HTTP calls, got %d", got)
	}
	// Page 2 must carry the cursor returned by page 1
	if !strings.Contains(stub.captured[1], `cursor: "cursor-2"`) {
		t.Errorf("page 2 query missing cursor; got: %s", stub.captured[1])
	}
}

func TestNerdGraphFetcher_BulkFetchBatching(t *testing.T) {
	// 26 GUIDs → 1 entitySearch call + 2 bulk calls (25 + 1)
	guids := make([]string, 26)
	for i := range guids {
		guids[i] = fmt.Sprintf("guid-%02d", i)
	}
	bulk1Entities := make([]stubBulkEntity, 25)
	for i := 0; i < 25; i++ {
		bulk1Entities[i] = stubBulkEntity{guid: guids[i], name: fmt.Sprintf("s%d", i)}
	}
	stub := &stubDoer{
		queue: []stubResponse{
			{200, entitySearchPage(guids, "")},
			{200, bulkEntitiesResponse(bulk1Entities)},
			{200, bulkEntitiesResponse([]stubBulkEntity{{guid: guids[25], name: "s25"}})},
		},
	}
	_, err := fetchNerdGraphRelationshipsWith(
		"key-batching", "8031798", "us", "tenant-batching", slog.Default(), stub.doer())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := stub.calls.Load(); got != 3 {
		t.Errorf("expected 3 calls (1 search + 2 bulk), got %d", got)
	}
	// First batch query must contain 25 GUIDs; second exactly 1
	if strings.Count(stub.captured[1], `"guid-`) != 25 {
		t.Errorf("first bulk batch should hold 25 guids; got %d", strings.Count(stub.captured[1], `"guid-`))
	}
	if strings.Count(stub.captured[2], `"guid-`) != 1 {
		t.Errorf("second bulk batch should hold 1 guid; got %d", strings.Count(stub.captured[2], `"guid-`))
	}
}

func TestNerdGraphFetcher_FiltersToCallsOnly(t *testing.T) {
	stub := &stubDoer{
		queue: []stubResponse{
			{200, entitySearchPage([]string{"g1"}, "")},
			{200, bulkEntitiesResponse([]stubBulkEntity{
				{
					guid: "g1", name: "cart",
					edges: []stubEdge{
						{relType: "CALLS", sourceGUID: "g1", sourceName: "cart", sourceType: "THIRD_PARTY_SERVICE_ENTITY",
							targetGUID: "g-redis", targetName: "redis", targetType: "EBPFSERVER"},
						{relType: "MANAGES", sourceGUID: "g1", sourceName: "cart", sourceType: "THIRD_PARTY_SERVICE_ENTITY",
							targetGUID: "g-pod", targetName: "cart-pod", targetType: "KUBERNETES_POD"},
						{relType: "CONTAINS", sourceGUID: "g1", sourceName: "cart", sourceType: "THIRD_PARTY_SERVICE_ENTITY",
							targetGUID: "g-ns", targetName: "default", targetType: "KUBERNETES_NAMESPACE"},
						{relType: "CALLS", sourceGUID: "g1", sourceName: "cart", sourceType: "THIRD_PARTY_SERVICE_ENTITY",
							targetGUID: "g-checkout", targetName: "checkout", targetType: "THIRD_PARTY_SERVICE_ENTITY"},
					},
				},
			})},
		},
	}
	rels, err := fetchNerdGraphRelationshipsWith(
		"key-calls-only", "8031798", "us", "tenant-calls-only", slog.Default(), stub.doer())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 CALLS edges, got %d: %+v", len(rels), rels)
	}
	for _, r := range rels {
		if r.CallerName != "cart" {
			t.Errorf("caller should be cart, got %s", r.CallerName)
		}
	}
}

func TestNerdGraphFetcher_CacheMissThenHit(t *testing.T) {
	// First call populates cache; second call (with same key) returns from cache.
	stub := &stubDoer{
		queue: []stubResponse{
			{200, entitySearchPage([]string{"g1"}, "")},
			{200, bulkEntitiesResponse([]stubBulkEntity{
				{guid: "g1", name: "cart", edges: []stubEdge{
					{relType: "CALLS", sourceGUID: "g1", sourceName: "cart", sourceType: "THIRD_PARTY_SERVICE_ENTITY",
						targetGUID: "g-checkout", targetName: "checkout", targetType: "THIRD_PARTY_SERVICE_ENTITY"},
				}},
			})},
		},
	}
	rels1, err := fetchNerdGraphRelationshipsWith(
		"key-cache", "8031798", "us", "tenant-cache-hit", slog.Default(), stub.doer())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rels1) != 1 {
		t.Fatalf("expected 1 edge from miss, got %d", len(rels1))
	}
	firstCalls := stub.calls.Load()

	// Second call must NOT hit HTTP — same cache key.
	rels2, err := fetchNerdGraphRelationshipsWith(
		"key-cache", "8031798", "us", "tenant-cache-hit", slog.Default(), stub.doer())
	if err != nil {
		t.Fatalf("unexpected error on cache hit: %v", err)
	}
	if len(rels2) != 1 {
		t.Errorf("cache hit should return 1 edge, got %d", len(rels2))
	}
	if stub.calls.Load() != firstCalls {
		t.Errorf("cache hit should make 0 new HTTP calls; before=%d, after=%d", firstCalls, stub.calls.Load())
	}
}

func TestNerdGraphFetcher_CacheKeysIsolateTenants(t *testing.T) {
	stub := &stubDoer{
		queue: []stubResponse{
			// Tenant A
			{200, entitySearchPage([]string{"gA"}, "")},
			{200, bulkEntitiesResponse([]stubBulkEntity{{guid: "gA", name: "svc-a"}})},
			// Tenant B (different cache key — must miss too, not serve A's data)
			{200, entitySearchPage([]string{"gB"}, "")},
			{200, bulkEntitiesResponse([]stubBulkEntity{{guid: "gB", name: "svc-b"}})},
		},
	}
	if _, err := fetchNerdGraphRelationshipsWith(
		"key-isolate", "8031798", "us", "tenant-A", slog.Default(), stub.doer()); err != nil {
		t.Fatal(err)
	}
	if _, err := fetchNerdGraphRelationshipsWith(
		"key-isolate", "8031798", "us", "tenant-B", slog.Default(), stub.doer()); err != nil {
		t.Fatal(err)
	}
	// 4 calls total: 2 search + 2 bulk. If tenant-B served A's cache, we'd see 2.
	if got := stub.calls.Load(); got != 4 {
		t.Errorf("expected 4 calls (no cross-tenant cache leak); got %d", got)
	}
}

func TestNerdGraphFetcher_CacheKeyChangesOnAPIKeyRotation(t *testing.T) {
	// Same tenant, different API key → distinct cache key, no leakage.
	stub := &stubDoer{
		queue: []stubResponse{
			{200, entitySearchPage([]string{"g1"}, "")},
			{200, bulkEntitiesResponse([]stubBulkEntity{{guid: "g1", name: "svc"}})},
			{200, entitySearchPage([]string{"g1"}, "")},
			{200, bulkEntitiesResponse([]stubBulkEntity{{guid: "g1", name: "svc"}})},
		},
	}
	if _, err := fetchNerdGraphRelationshipsWith(
		"key-old", "8031798", "us", "tenant-rotate", slog.Default(), stub.doer()); err != nil {
		t.Fatal(err)
	}
	if _, err := fetchNerdGraphRelationshipsWith(
		"key-new-after-rotation", "8031798", "us", "tenant-rotate", slog.Default(), stub.doer()); err != nil {
		t.Fatal(err)
	}
	if got := stub.calls.Load(); got != 4 {
		t.Errorf("apiKey rotation should invalidate cache; expected 4 calls, got %d", got)
	}
}

func TestNerdGraphFetcher_HTTPErrorPropagates(t *testing.T) {
	stub := &stubDoer{queue: []stubResponse{{500, `{"errors":[{"message":"upstream"}]}`}}}
	_, err := fetchNerdGraphRelationshipsWith(
		"key-err", "8031798", "us", "tenant-err", slog.Default(), stub.doer())
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status 500, got: %v", err)
	}
}

func TestNerdGraphFetcher_EmptyEntitySearchReturnsNilError(t *testing.T) {
	// No matching entities — no bulk fetch — caller treats as "fall back to NRQL".
	stub := &stubDoer{queue: []stubResponse{{200, entitySearchPage(nil, "")}}}
	rels, err := fetchNerdGraphRelationshipsWith(
		"key-empty", "8031798", "us", "tenant-empty", slog.Default(), stub.doer())
	if err != nil {
		t.Fatalf("empty result should not error; got: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 rels, got %d", len(rels))
	}
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("empty entitySearch should skip bulk fetch; expected 1 call, got %d", got)
	}
}
