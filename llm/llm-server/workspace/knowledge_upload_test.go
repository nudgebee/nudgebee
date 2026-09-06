package workspace

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

func TestWorkspaceStreamingKeepsJSONRequestContract(t *testing.T) {
	old := config.Config.LlmServerWorkspaceLocalUrl
	t.Cleanup(func() { config.Config.LlmServerWorkspaceLocalUrl = old })
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		if calls == 0 {
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			require.Equal(t, int64(len(body)), r.ContentLength)
			require.JSONEq(t, `{"query":"analyze"}`, string(body))
		} else {
			require.Equal(t, "application/octet-stream", r.Header.Get("Content-Type"))
			require.Equal(t, "complete streamed body", string(body))
		}
		calls++
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	config.Config.LlmServerWorkspaceLocalUrl = server.URL
	manager := NewWorkspaceManager()
	ctx := security.NewRequestContextForSuperAdmin()
	_, err := manager.CallAPI(ctx, "account", "POST", "/analyze", nil, map[string]string{"query": "analyze"})
	require.NoError(t, err)
	stream := strings.NewReader("complete streamed body")
	_, err = stream.Seek(8, io.SeekStart)
	require.NoError(t, err)
	_, err = manager.CallAPI(ctx, "account", "PUT", "/api/v1/files/knowledge", nil, stream)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}
