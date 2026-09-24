package core

import (
	"net/http"
	"net/http/httptest"
	"nudgebee/llm/config"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withRAGServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	previous := config.Config.RAGServerUrl
	config.Config.RAGServerUrl = server.URL
	t.Cleanup(func() { config.Config.RAGServerUrl = previous })
}

func TestGetRAGDecodesDataEnvelope(t *testing.T) {
	withRAGServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/collections/int-1_knowledge_base/documents", r.URL.Path)
		assert.Equal(t, "cursor/1", r.URL.Query().Get("offset"))
		_, _ = w.Write([]byte(`{"data":{"items":[{"id":"a","title":null,"url":"https://wiki/a"}],"next_offset":"b"}}`))
	})

	var page struct {
		Items      []ragKBDocument `json:"items"`
		NextOffset *string         `json:"next_offset"`
	}
	found, err := getRAG("/collections/int-1_knowledge_base/documents?offset=cursor%2F1", &page)
	require.NoError(t, err)
	assert.True(t, found)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "Runbooks", documentTitle(page.Items[0], "Runbooks"))
	require.NotNil(t, page.NextOffset)
	assert.Equal(t, "b", *page.NextOffset)
}

func TestGetRAGNotFoundAndServerError(t *testing.T) {
	status := http.StatusNotFound
	withRAGServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"detail":"nope"}`))
	})

	var out map[string]any
	found, err := getRAG("/collections/kb_1/documents", &out)
	require.NoError(t, err)
	assert.False(t, found)

	status = http.StatusInternalServerError
	_, err = getRAG("/collections/kb_1/documents", &out)
	assert.Error(t, err)
}

func TestDocumentTitlePrefersStoredTitle(t *testing.T) {
	title := "Reset VPN"
	assert.Equal(t, "Reset VPN", documentTitle(ragKBDocument{Title: &title}, "ServiceNow KB"))
	empty := ""
	assert.Equal(t, "ServiceNow KB", documentTitle(ragKBDocument{Title: &empty}, "ServiceNow KB"))
}
