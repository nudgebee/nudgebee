package integrations

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/require"
)

const testHost = "https://confluence.example.com"

func TestConfluence_ParsePageRef(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantID    string
		wantSpace string
		wantTitle string
		wantErr   string
	}{
		{name: "bare id", input: "123456789", wantID: "123456789"},
		{name: "data center path", input: testHost + "/spaces/SRE/pages/123456789/Runbook+-+Cache+Restart", wantID: "123456789"},
		{name: "cloud path", input: testHost + "/wiki/spaces/SRE/pages/42/Title", wantID: "42"},
		{name: "path with anchor and query noise", input: testHost + "/spaces/SRE/pages/42/Title?focusedCommentId=7#comment-7", wantID: "42"},
		{name: "viewpage action", input: testHost + "/pages/viewpage.action?pageId=99", wantID: "99"},
		{name: "display url", input: testHost + "/display/SRE/SOP+-+Cache+Restart", wantSpace: "SRE", wantTitle: "SOP - Cache Restart"},
		{name: "display url keeps a literal plus", input: testHost + "/display/SRE/C%2B%2B+Guide", wantSpace: "SRE", wantTitle: "C++ Guide"},
		{name: "short link", input: testHost + "/x/AbCdEf", wantErr: "short link"},
		{name: "other host", input: "https://other.example.com/spaces/SRE/pages/42/Title", wantErr: "configured for confluence.example.com"},
		{name: "not a page url", input: testHost + "/spaces/SRE/overview", wantErr: "does not look like a Confluence page URL"},
		{name: "garbage", input: "Runbooks", wantErr: "neither a page ID nor a Confluence page URL"},
		{name: "empty", input: "  ", wantErr: "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := confluenceParsePageRef(tc.input, testHost)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantID, ref.ID)
			require.Equal(t, tc.wantSpace, ref.SpaceKey)
			require.Equal(t, tc.wantTitle, ref.Title)
		})
	}
}

// fakeConfluence serves the handful of endpoints the page-tree code touches.
// Page 100 is "Runbooks" in SRE; page 200 is "Other" in OPS; the SRE
// homepage 1 has one child, 100.
func fakeConfluence(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/space":
			if key := r.URL.Query().Get("spaceKey"); key != "" && key != "SRE" {
				_, _ = w.Write([]byte(`{"results":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"key":"SRE"}]}`))
		case "/rest/api/space/SRE":
			_, _ = w.Write([]byte(`{"key":"SRE","homepage":{"id":"1","title":"Site Reliability"}}`))
		case "/rest/api/content/1/child/page":
			_, _ = w.Write([]byte(`{"results":[{"id":"100","title":"Runbooks"}]}`))
		case "/rest/api/content/100":
			_, _ = w.Write([]byte(`{"id":"100","title":"Runbooks","space":{"key":"SRE"}}`))
		case "/rest/api/content/200":
			_, _ = w.Write([]byte(`{"id":"200","title":"Other","space":{"key":"OPS"}}`))
		case "/rest/api/content":
			if r.URL.Query().Get("title") == "Runbooks" {
				_, _ = w.Write([]byte(`{"results":[{"id":"100","title":"Runbooks","space":{"key":"SRE"}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"no content"}`))
		}
	}))
}

func TestConfluence_ResolvePage(t *testing.T) {
	srv := fakeConfluence(t, nil)
	defer srv.Close()
	apiBase := srv.URL + "/rest/api"

	byID, err := confluenceResolvePage(apiBase, "Bearer x", confluencePageRef{ID: "100", raw: "100"})
	require.NoError(t, err)
	require.Equal(t, confluencePage{ID: "100", Title: "Runbooks", SpaceKey: "SRE"}, byID)

	byTitle, err := confluenceResolvePage(apiBase, "Bearer x", confluencePageRef{SpaceKey: "SRE", Title: "Runbooks", raw: "url"})
	require.NoError(t, err)
	require.Equal(t, "100", byTitle.ID)

	_, err = confluenceResolvePage(apiBase, "Bearer x", confluencePageRef{SpaceKey: "SRE", Title: "Missing", raw: "url"})
	require.ErrorContains(t, err, `no page titled "Missing"`)

	_, err = confluenceResolvePage(apiBase, "Bearer x", confluencePageRef{ID: "404", raw: "404"})
	require.ErrorContains(t, err, "does not exist or is not accessible")
}

func TestConfluence_ResolvePageTrees_RejectsPageOutsideConfiguredSpace(t *testing.T) {
	srv := fakeConfluence(t, nil)
	defer srv.Close()

	_, err := confluenceResolvePageTrees(srv.URL+"/rest/api", "Bearer x", srv.URL, "SRE", "100,200")
	require.ErrorContains(t, err, `is in space "OPS", not the configured space "SRE"`)

	pages, err := confluenceResolvePageTrees(srv.URL+"/rest/api", "Bearer x", srv.URL, "", "100, 200, 100")
	require.NoError(t, err)
	require.Len(t, pages, 2)
}

func TestConfluence_NormalizeConfig_RewritesURLsToIDs(t *testing.T) {
	srv := fakeConfluence(t, nil)
	defer srv.Close()

	values := []core.IntegrationConfigValue{
		{Name: "auth_type", Value: ConfluenceAuthDataCenterPAT},
		{Name: "host", Value: srv.URL},
		{Name: "token", Value: "pat"},
		{Name: "namespace", Value: "SRE"},
		{Name: confluencePageTreesField, Value: srv.URL + "/spaces/SRE/pages/100/Runbooks, " + srv.URL + "/display/SRE/Runbooks"},
	}
	require.NoError(t, Confluence{}.NormalizeConfig(nil, values))
	require.Equal(t, "100", values[4].Value)
}

func TestConfluence_NormalizeConfig_LeavesIDsAloneWithoutNetwork(t *testing.T) {
	var hits int32
	srv := fakeConfluence(t, &hits)
	defer srv.Close()

	values := []core.IntegrationConfigValue{
		{Name: "auth_type", Value: ConfluenceAuthDataCenterPAT},
		{Name: "host", Value: srv.URL},
		{Name: "token", Value: "pat"},
		{Name: confluencePageTreesField, Value: " 100 ,200,100"},
	}
	require.NoError(t, Confluence{}.NormalizeConfig(nil, values))
	require.Equal(t, "100,200", values[3].Value)
	require.Zero(t, atomic.LoadInt32(&hits))
}

func TestConfluence_NormalizeConfig_ReportsUnresolvablePaste(t *testing.T) {
	srv := fakeConfluence(t, nil)
	defer srv.Close()

	values := []core.IntegrationConfigValue{
		{Name: "auth_type", Value: ConfluenceAuthDataCenterPAT},
		{Name: "host", Value: srv.URL},
		{Name: "token", Value: "pat"},
		{Name: confluencePageTreesField, Value: srv.URL + "/x/AbCdEf"},
	}
	require.ErrorContains(t, Confluence{}.NormalizeConfig(nil, values), "short link")
}

func TestConfluence_ValidateConfig_ChecksPageTrees(t *testing.T) {
	srv := fakeConfluence(t, nil)
	defer srv.Close()

	base := []core.IntegrationConfigValue{
		{Name: "auth_type", Value: ConfluenceAuthDataCenterPAT},
		{Name: "host", Value: srv.URL},
		{Name: "token", Value: "pat"},
		{Name: "namespace", Value: "SRE"},
	}

	ok := append(append([]core.IntegrationConfigValue{}, base...), core.IntegrationConfigValue{Name: confluencePageTreesField, Value: "100"})
	require.Empty(t, Confluence{}.ValidateConfig(nil, ok, ""))

	unreadable := append(append([]core.IntegrationConfigValue{}, base...), core.IntegrationConfigValue{Name: confluencePageTreesField, Value: "404"})
	errs := Confluence{}.ValidateConfig(nil, unreadable, "")
	require.Len(t, errs, 1)
	require.ErrorContains(t, errs[0], "does not exist or is not accessible")

	otherSpace := append(append([]core.IntegrationConfigValue{}, base...), core.IntegrationConfigValue{Name: confluencePageTreesField, Value: "200"})
	errs = Confluence{}.ValidateConfig(nil, otherSpace, "")
	require.Len(t, errs, 1)
	require.ErrorContains(t, errs[0], `not the configured space "SRE"`)
}

func TestConfluence_ListTopLevelPages(t *testing.T) {
	srv := fakeConfluence(t, nil)
	defer srv.Close()

	pages, err := confluenceListTopLevelPages(srv.URL+"/rest/api", "Bearer x", "SRE")
	require.NoError(t, err)
	require.Equal(t, []confluencePage{
		{ID: "1", Title: "Site Reliability", SpaceKey: "SRE"},
		{ID: "100", Title: "Runbooks", SpaceKey: "SRE"},
	}, pages)
}

func TestConfluence_ListPagesAutogen(t *testing.T) {
	srv := fakeConfluence(t, nil)
	defer srv.Close()

	form := map[string]any{
		"host":      srv.URL,
		"auth_type": ConfluenceAuthDataCenterPAT,
		"namespace": "SRE",
	}
	res, err := listConfluencePages(nil, form)
	require.NoError(t, err)
	require.Empty(t, res.Options)
	require.Contains(t, res.Message, "Re-enter the token")
	// Every guidance message leads with what the field does, because the
	// description itself is only reachable through a tooltip.
	require.Contains(t, res.Message, confluencePageTreesHint)

	// Only pages inside the configured space: no space suffix, and the hint
	// stays on the default guidance.
	pastedURL := srv.URL + "/spaces/SRE/pages/100/x"
	form["token"] = "pat"
	form[confluencePageTreesField] = pastedURL
	res, err = listConfluencePages(nil, form)
	require.NoError(t, err)
	require.Equal(t, []core.AutoGenOption{
		{Label: "Runbooks", Value: pastedURL},
		{Label: "Site Reliability", Value: "1"},
	}, res.Options)
	require.Contains(t, res.Message, confluencePageTreesHint)
	require.Contains(t, res.Message, "paste a URL for anything deeper")

	// A page from another space is what the save will reject, so the picker
	// names it immediately and tags the option with the space it came from.
	form[confluencePageTreesField] = "200," + pastedURL
	res, err = listConfluencePages(nil, form)
	require.NoError(t, err)
	require.Equal(t, []core.AutoGenOption{
		{Label: "Other (OPS)", Value: "200"},
		{Label: "Runbooks", Value: pastedURL},
		{Label: "Site Reliability", Value: "1"},
	}, res.Options)
	require.Equal(t,
		`"Other" (OPS) is not in the configured space "SRE". `+
			"Saving will fail until it is removed, or the space key is cleared.",
		res.Message)

	// An unlistable space must not hide the titles already resolved.
	form["namespace"] = "NOPE"
	res, err = listConfluencePages(nil, form)
	require.NoError(t, err)
	require.Equal(t, []core.AutoGenOption{
		{Label: "Other (OPS)", Value: "200"},
		{Label: "Runbooks (SRE)", Value: pastedURL},
	}, res.Options)
	// Two offenders: the message counts them and pluralises the fix, rather
	// than comma-splicing one clause per page onto a singular pronoun.
	require.Equal(t,
		`2 pages are not in the configured space "NOPE": "Other" (OPS), "Runbooks" (SRE). `+
			"Saving will fail until they are removed, or the space key is cleared.",
		res.Message)

	// With no space key there is nothing to compare against, so every option
	// carries its space and the guidance points at both ways in.
	form["namespace"] = ""
	res, err = listConfluencePages(nil, form)
	require.NoError(t, err)
	require.Equal(t, []core.AutoGenOption{
		{Label: "Other (OPS)", Value: "200"},
		{Label: "Runbooks (SRE)", Value: pastedURL},
	}, res.Options)
	require.Contains(t, res.Message, confluencePageTreesHint)
	require.Contains(t, res.Message, "Enter a space key above")
}

func TestConfluence_ConfigSchema_PageTreesIsAdvanced(t *testing.T) {
	prop, ok := Confluence{}.ConfigSchema().Properties[confluencePageTreesField]
	require.True(t, ok)
	require.True(t, prop.Advanced)
	require.Equal(t, core.ToolSchemaTypeArray, prop.Type)
	require.Equal(t, confluenceListPagesAutogenFunc, prop.AutoGenerateFunc)
	require.Contains(t, prop.DependsOn, "token")
	require.Contains(t, prop.DependsOn, confluencePageTreesField)
	// The storage key is a contract three services read, so the clearer name
	// has to travel as a label rather than as a rename.
	require.Equal(t, "Limit to pages", prop.DisplayName)
	require.NotEmpty(t, prop.SearchPlaceholder)
}
