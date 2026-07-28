// Package productupdates serves the "Product Updates" changelog.
//
// Updates are sourced entirely from a JSON file in nudgebee-docs (fetched +
// cached, mirroring the agent-version "latest" lookup), so editing the
// changelog is a docs commit — no api-server release and no database. When the
// remote is unreachable the embedded product_updates.json is the fallback; it
// ships as an empty array, so an unconfigured / air-gapped install shows an
// empty drawer rather than placeholder content (point PRODUCT_UPDATES_URL at a
// reachable mirror to serve content offline). Per-user "last seen" state is
// tracked client-side, so there is no read-state persisted here.
package productupdates

import (
	_ "embed"
	"fmt"
	"io"
	"nudgebee/services/common"
	"nudgebee/services/security"
	"os"
	"sort"
	"sync"
	"time"
)

// listLimit caps how many updates are returned in one call.
const listLimit = 100

// cacheTTL matches the agent-version lookup's refresh cadence.
const cacheTTL = 30 * time.Minute

// defaultUpdatesURL points at the changelog JSON in nudgebee-docs. Override with
// PRODUCT_UPDATES_URL (e.g. a private mirror for air-gapped on-prem).
const defaultUpdatesURL = "https://raw.githubusercontent.com/nudgebee/nudgebee-docs/main/product-updates/product_updates.json"

// embeddedSeed is the fallback used when the remote is unreachable. It ships as
// an empty array (`[]`) so a missing/unreachable source yields an empty drawer,
// not placeholder content.
//
//go:embed product_updates.json
var embeddedSeed []byte

// ProductUpdate is one changelog entry returned to the frontend.
type ProductUpdate struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Body     string  `json:"body"`
	Category *string `json:"category,omitempty"`
	URL      *string `json:"url,omitempty"`
	// Highlight=false => shown in the drawer but never contributes to the unread
	// badge / "New" tag (used for historical back-catalog entries).
	Highlight   bool      `json:"highlight"`
	PublishedAt time.Time `json:"published_at"`
}

// jsonEntry mirrors one entry in product_updates.json.
type jsonEntry struct {
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Category    *string   `json:"category"`
	URL         *string   `json:"url"`
	Highlight   *bool     `json:"highlight"` // default true
	IsActive    *bool     `json:"is_active"` // default true
	PublishedAt time.Time `json:"published_at"`
}

var (
	cacheMu    sync.Mutex
	syncedAt   *time.Time
	cachedList []ProductUpdate
)

// ListProductUpdates returns the changelog (from nudgebee-docs, cached), newest
// first.
func ListProductUpdates(ctx *security.RequestContext) ([]ProductUpdate, error) {
	updates := append([]ProductUpdate{}, getUpdates(ctx)...)

	sort.SliceStable(updates, func(i, j int) bool {
		return updates[i].PublishedAt.After(updates[j].PublishedAt)
	})
	if len(updates) > listLimit {
		updates = updates[:listLimit]
	}
	return updates, nil
}

// getUpdates returns the cached changelog, refreshing from the remote JSON when
// the cache is stale. Falls back to the embedded copy when the remote is
// unreachable or unparseable.
func getUpdates(ctx *security.RequestContext) []ProductUpdate {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	if cachedList != nil && syncedAt != nil && time.Since(*syncedAt) <= cacheTTL {
		return cachedList
	}

	url := os.Getenv("PRODUCT_UPDATES_URL")
	if url == "" {
		url = defaultUpdatesURL
	}

	data, err := fetchRemote(url)
	if err != nil {
		ctx.GetLogger().Warn("product_updates: remote fetch failed; using embedded fallback", "url", url, "error", err)
		data = embeddedSeed
	}

	parsed, perr := parseEntries(data)
	if perr != nil {
		ctx.GetLogger().Warn("product_updates: parse failed; using embedded fallback", "error", perr)
		parsed, perr = parseEntries(embeddedSeed)
		if perr != nil {
			ctx.GetLogger().Error("product_updates: embedded fallback parse failed", "error", perr)
			return cachedList // keep whatever we last had (possibly nil)
		}
	}

	now := time.Now()
	cachedList = parsed
	syncedAt = &now
	return cachedList
}

func fetchRemote(url string) ([]byte, error) {
	resp, err := common.HttpGet(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func parseEntries(data []byte) ([]ProductUpdate, error) {
	var entries []jsonEntry
	if err := common.UnmarshalJson(data, &entries); err != nil {
		return nil, err
	}
	out := make([]ProductUpdate, 0, len(entries))
	for _, e := range entries {
		if e.IsActive != nil && !*e.IsActive {
			continue
		}
		highlight := true
		if e.Highlight != nil {
			highlight = *e.Highlight
		}
		out = append(out, ProductUpdate{
			ID:          e.Slug,
			Title:       e.Title,
			Body:        e.Body,
			Category:    e.Category,
			URL:         e.URL,
			Highlight:   highlight,
			PublishedAt: e.PublishedAt,
		})
	}
	return out, nil
}
