package integrations

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

// confluencePageTreesField narrows the RAG scrape below the space level: each
// stored page ID is indexed together with every page beneath it. Empty means
// the scraper walks the configured space (or all spaces) as before.
//
// The stored value is always a comma-separated list of page IDs. Users may
// type IDs or paste page URLs; NormalizeConfig resolves the latter at save so
// rag-server and llm-server, which read the row directly, never see a URL.
const confluencePageTreesField = "page_trees"

const confluenceListPagesAutogenFunc = "listConfluencePages"

// confluencePageTreesDeps lists the form fields the page picker reads. The
// field itself is included so already-selected IDs come back with their
// titles when the form is reopened.
var confluencePageTreesDeps = []string{"host", "auth_type", "username", "token", "namespace", confluencePageTreesField}

type confluencePage struct {
	ID       string
	Title    string
	SpaceKey string
}

// confluencePageRef is one user-supplied entry before resolution: a page ID,
// or a space key + title when the URL shape carries no ID.
type confluencePageRef struct {
	ID       string
	SpaceKey string
	Title    string
	raw      string
}

type confluencePageJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Space struct {
		Key string `json:"key"`
	} `json:"space"`
}

var (
	confluencePageIDRe      = regexp.MustCompile(`^\d+$`)
	confluencePagesPathRe   = regexp.MustCompile(`/pages/(\d+)(?:/|$)`)
	confluenceDisplayPathRe = regexp.MustCompile(`/display/([^/]+)/([^/]+)/?$`)
)

func confluenceSplitPageTrees(value string) []string {
	var out []string
	for _, entry := range strings.Split(value, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// confluenceParsePageRef accepts a bare page ID or a page URL in any shape
// Cloud or Data Center produces. host is the integration's base URL: a URL
// from another host is almost always a paste from the wrong instance.
func confluenceParsePageRef(input, host string) (confluencePageRef, error) {
	ref := confluencePageRef{raw: strings.TrimSpace(input)}
	if ref.raw == "" {
		return ref, fmt.Errorf("page reference is empty")
	}
	if confluencePageIDRe.MatchString(ref.raw) {
		ref.ID = ref.raw
		return ref, nil
	}

	u, err := url.Parse(ref.raw)
	if err != nil || u.Host == "" {
		return ref, fmt.Errorf("%q is neither a page ID nor a Confluence page URL", ref.raw)
	}
	if h, err := url.Parse(strings.TrimSpace(host)); err == nil && h.Host != "" && !strings.EqualFold(h.Hostname(), u.Hostname()) {
		return ref, fmt.Errorf("%q is on %s, but this integration is configured for %s", ref.raw, u.Hostname(), h.Hostname())
	}
	// Short links (/x/AbCdEf) are what Data Center's Share button copies. They
	// carry no page ID and only resolve through a redirect.
	if strings.Contains(u.Path, "/x/") {
		return ref, fmt.Errorf("%q is a Confluence short link. Open the page and paste the URL from the browser's address bar instead", ref.raw)
	}
	if m := confluencePagesPathRe.FindStringSubmatch(u.Path); m != nil {
		ref.ID = m[1]
		return ref, nil
	}
	if id := u.Query().Get("pageId"); confluencePageIDRe.MatchString(id) {
		ref.ID = id
		return ref, nil
	}
	// /display/KEY/Page+Title encodes spaces as "+" and a literal plus as %2B,
	// so the distinction only survives in the escaped path.
	if m := confluenceDisplayPathRe.FindStringSubmatch(u.EscapedPath()); m != nil {
		title, err := url.PathUnescape(strings.ReplaceAll(m[2], "+", " "))
		if err != nil {
			title = m[2]
		}
		ref.SpaceKey = m[1]
		ref.Title = title
		return ref, nil
	}
	return ref, fmt.Errorf("%q does not look like a Confluence page URL", ref.raw)
}

// confluenceResolvePage fetches the page a reference points at, which also
// proves it exists and is readable by these credentials.
func confluenceResolvePage(apiBase, authHeader string, ref confluencePageRef) (confluencePage, error) {
	var (
		result *confluenceProbeResult
		err    error
	)
	if ref.ID != "" {
		result, err = confluenceGet(apiBase+"/content/"+ref.ID, authHeader, map[string]string{"expand": "space"})
	} else {
		result, err = confluenceGet(apiBase+"/content", authHeader, map[string]string{
			"spaceKey": ref.SpaceKey,
			"title":    ref.Title,
			"expand":   "space",
			"limit":    "1",
		})
	}
	if err != nil {
		return confluencePage{}, fmt.Errorf("failed to look up Confluence page %q: %w", ref.raw, err)
	}
	switch result.statusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return confluencePage{}, fmt.Errorf("Confluence page %q does not exist or is not accessible to these credentials", ref.raw)
	case http.StatusUnauthorized, http.StatusForbidden:
		return confluencePage{}, fmt.Errorf("these credentials cannot read Confluence page %q (HTTP %d)", ref.raw, result.statusCode)
	default:
		return confluencePage{}, fmt.Errorf("failed to look up Confluence page %q: Confluence returned HTTP %d", ref.raw, result.statusCode)
	}

	var page confluencePageJSON
	if ref.ID != "" {
		if err := json.Unmarshal(result.body, &page); err != nil {
			return confluencePage{}, fmt.Errorf("failed to parse the Confluence response for page %q: %w", ref.raw, err)
		}
	} else {
		var list struct {
			Results []confluencePageJSON `json:"results"`
		}
		if err := json.Unmarshal(result.body, &list); err != nil {
			return confluencePage{}, fmt.Errorf("failed to parse the Confluence response for page %q: %w", ref.raw, err)
		}
		if len(list.Results) == 0 {
			return confluencePage{}, fmt.Errorf("no page titled %q exists in space %q, or it is not accessible to these credentials", ref.Title, ref.SpaceKey)
		}
		page = list.Results[0]
	}
	if page.ID == "" {
		return confluencePage{}, fmt.Errorf("Confluence returned no page ID for %q", ref.raw)
	}
	return confluencePage{ID: page.ID, Title: page.Title, SpaceKey: page.Space.Key}, nil
}

// confluenceResolvePageTrees resolves every entry of a page_trees value,
// de-duplicated, and rejects a page outside the configured space.
func confluenceResolvePageTrees(apiBase, authHeader, host, namespace, value string) ([]confluencePage, error) {
	var pages []confluencePage
	seen := map[string]bool{}
	for _, entry := range confluenceSplitPageTrees(value) {
		ref, err := confluenceParsePageRef(entry, host)
		if err != nil {
			return nil, err
		}
		page, err := confluenceResolvePage(apiBase, authHeader, ref)
		if err != nil {
			return nil, err
		}
		if namespace != "" && !strings.EqualFold(page.SpaceKey, namespace) {
			return nil, fmt.Errorf("page %q (%s) is in space %q, not the configured space %q", page.Title, page.ID, page.SpaceKey, namespace)
		}
		if seen[page.ID] {
			continue
		}
		seen[page.ID] = true
		pages = append(pages, page)
	}
	return pages, nil
}

func confluenceConfigMap(values []core.IntegrationConfigValue) map[string]string {
	configMap := make(map[string]string, len(values))
	for _, v := range values {
		configMap[v.Name] = v.Value
	}
	return configMap
}

// NormalizeConfig rewrites page_trees to bare page IDs. Entries that are
// already IDs are not re-fetched, so a canonical value passes through with no
// network calls. Missing credentials are left for ValidateConfig to report.
func (m Confluence) NormalizeConfig(_ *security.SecurityContext, values []core.IntegrationConfigValue) error {
	idx := -1
	for i, v := range values {
		if v.Name == confluencePageTreesField {
			idx = i
		}
	}
	if idx < 0 {
		return nil
	}
	entries := confluenceSplitPageTrees(values[idx].Value)
	if len(entries) == 0 {
		values[idx].Value = ""
		return nil
	}

	needsResolution := false
	for _, entry := range entries {
		if !confluencePageIDRe.MatchString(entry) {
			needsResolution = true
			break
		}
	}
	ids := make([]string, 0, len(entries))
	if !needsResolution {
		seen := map[string]bool{}
		for _, entry := range entries {
			if !seen[entry] {
				seen[entry] = true
				ids = append(ids, entry)
			}
		}
		values[idx].Value = strings.Join(ids, ",")
		return nil
	}

	configMap := confluenceConfigMap(values)
	mode := confluenceAuthType(configMap["auth_type"])
	host := strings.TrimSpace(configMap["host"])
	username := strings.TrimSpace(configMap["username"])
	token := configMap["token"]
	if host == "" || token == "" || (confluenceNeedsUsername(mode) && username == "") {
		return nil
	}
	if err := confluenceValidateHost(host, mode); err != nil {
		return nil
	}
	pages, err := confluenceResolvePageTrees(
		confluenceAPIBase(host, mode),
		confluenceAuthHeader(mode, username, token),
		host,
		strings.TrimSpace(configMap["namespace"]),
		values[idx].Value,
	)
	if err != nil {
		return err
	}
	for _, page := range pages {
		ids = append(ids, page.ID)
	}
	values[idx].Value = strings.Join(ids, ",")
	return nil
}

// confluenceListTopLevelPages returns the space homepage and its children —
// the top of the page tree as users see it in the sidebar. Orphan pages with
// no parent are not listed; they can still be pasted as URLs.
func confluenceListTopLevelPages(apiBase, authHeader, spaceKey string) ([]confluencePage, error) {
	result, err := confluenceGet(apiBase+"/space/"+url.PathEscape(spaceKey), authHeader, map[string]string{"expand": "homepage"})
	if err != nil {
		return nil, err
	}
	if result.statusCode != http.StatusOK {
		return nil, fmt.Errorf("Confluence space %q does not exist or is not accessible to these credentials (HTTP %d)", spaceKey, result.statusCode)
	}
	var space struct {
		Key      string             `json:"key"`
		Homepage confluencePageJSON `json:"homepage"`
	}
	if err := json.Unmarshal(result.body, &space); err != nil {
		return nil, fmt.Errorf("failed to parse the Confluence space response for %q: %w", spaceKey, err)
	}
	if space.Homepage.ID == "" {
		return nil, fmt.Errorf("Confluence space %q has no homepage to list pages from", spaceKey)
	}
	pages := []confluencePage{{ID: space.Homepage.ID, Title: space.Homepage.Title, SpaceKey: spaceKey}}

	result, err = confluenceGet(apiBase+"/content/"+space.Homepage.ID+"/child/page", authHeader, map[string]string{"limit": "200"})
	if err != nil {
		return nil, err
	}
	if result.statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list the pages of Confluence space %q: Confluence returned HTTP %d", spaceKey, result.statusCode)
	}
	var children struct {
		Results []confluencePageJSON `json:"results"`
	}
	if err := json.Unmarshal(result.body, &children); err != nil {
		return nil, fmt.Errorf("failed to parse the Confluence page list for %q: %w", spaceKey, err)
	}
	for _, child := range children.Results {
		pages = append(pages, confluencePage{ID: child.ID, Title: child.Title, SpaceKey: spaceKey})
	}
	return pages, nil
}

// confluencePageTreesHint states what the field does, not how it works. It
// leads every picker message because "indexes everything beneath it" is the one
// idea a user has to hold to use the field at all, and the field's own
// description is only reachable through a tooltip.
const confluencePageTreesHint = "Each page you add is indexed along with every page beneath it."

// listConfluencePages is the autogen handler behind the page_trees picker.
// It always echoes titles for the pages already selected (so a reopened form
// shows names, not IDs) and adds the configured space's top-level pages.
func listConfluencePages(_ *security.RequestContext, form map[string]any) (core.AutoGenResult, error) {
	host := stringFromForm(form, "host")
	mode := confluenceAuthType(stringFromForm(form, "auth_type"))
	username := stringFromForm(form, "username")
	token := stringFromForm(form, "token")
	namespace := stringFromForm(form, "namespace")

	if host == "" {
		return core.AutoGenResult{Message: confluencePageTreesHint + " Enter the Confluence base URL to list pages."}, nil
	}
	// Edit-existing flow: the token is never echoed back into the form, so
	// there is nothing to authenticate the listing with.
	if token == "" {
		return core.AutoGenResult{Message: confluencePageTreesHint + " Re-enter the token to list pages, or paste a page URL."}, nil
	}
	if confluenceNeedsUsername(mode) && username == "" {
		return core.AutoGenResult{Message: confluencePageTreesHint + " Enter the username to list pages, or paste a page URL."}, nil
	}
	if err := confluenceValidateHost(host, mode); err != nil {
		return core.AutoGenResult{}, err
	}
	apiBase := confluenceAPIBase(host, mode)
	authHeader := confluenceAuthHeader(mode, username, token)

	var opts []core.AutoGenOption
	var offSpace []string
	seen := map[string]bool{}
	add := func(page confluencePage, value string) {
		if seen[page.ID] {
			return
		}
		seen[page.ID] = true
		// The space is worth showing only when it is not the one already
		// configured: that is the case a user needs to notice (a pasted URL
		// that landed elsewhere). Repeating the configured key on every row
		// would be noise on the common path.
		label := page.Title
		if page.SpaceKey != "" && !strings.EqualFold(page.SpaceKey, namespace) {
			label = fmt.Sprintf("%s (%s)", page.Title, page.SpaceKey)
		}
		opts = append(opts, core.AutoGenOption{Label: label, Value: value})
	}

	// Selected entries the user has typed but not yet saved may be URLs. The
	// option keeps the typed value so the chip shows the page title before
	// NormalizeConfig turns it into an ID at save; a failing entry is left for
	// ValidateConfig to explain rather than blocking the listing.
	for _, entry := range confluenceSplitPageTrees(stringFromForm(form, confluencePageTreesField)) {
		ref, err := confluenceParsePageRef(entry, host)
		if err != nil {
			continue
		}
		if page, err := confluenceResolvePage(apiBase, authHeader, ref); err == nil {
			if namespace != "" && !strings.EqualFold(page.SpaceKey, namespace) {
				offSpace = append(offSpace, fmt.Sprintf("%q (%s)", page.Title, page.SpaceKey))
			}
			add(page, entry)
		}
	}

	if namespace == "" {
		return core.AutoGenResult{
			Options: opts,
			Message: confluencePageTreesHint + " Enter a space key above to list top-level pages, or paste a page URL here.",
		}, nil
	}
	// A failed listing is reported as a hint, not an error: the endpoint drops
	// the options on error, which would hide the titles resolved above.
	var listErr string
	pages, err := confluenceListTopLevelPages(apiBase, authHeader, namespace)
	if err != nil {
		listErr = err.Error()
	}
	for _, page := range pages {
		add(page, page.ID)
	}

	// Ordered by what the user has to act on first. A page outside the
	// configured space is rejected at save, so saying so here turns a
	// fill-in-the-whole-form-then-fail into an immediate correction.
	switch {
	case len(offSpace) > 0:
		// Singular and plural are built separately: one message shape covering
		// both ends up either listing "1 page" or pluralising a pronoun over a
		// single item, and this string is the whole point of the field's copy.
		subject := fmt.Sprintf("%s is not in the configured space %q", offSpace[0], namespace)
		fix := "Saving will fail until it is removed, or the space key is cleared."
		if len(offSpace) > 1 {
			subject = fmt.Sprintf("%d pages are not in the configured space %q: %s",
				len(offSpace), namespace, strings.Join(offSpace, ", "))
			fix = "Saving will fail until they are removed, or the space key is cleared."
		}
		return core.AutoGenResult{Options: opts, Message: subject + ". " + fix}, nil
	case listErr != "":
		return core.AutoGenResult{Options: opts, Message: listErr}, nil
	}
	return core.AutoGenResult{
		Options: opts,
		Message: confluencePageTreesHint + " The list shows top-level pages; paste a URL for anything deeper.",
	}, nil
}
