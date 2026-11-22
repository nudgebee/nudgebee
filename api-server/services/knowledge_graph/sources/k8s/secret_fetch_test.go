package k8s

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/config"
	"nudgebee/services/knowledge_graph/core"
)

// Helm release secrets each carry a full compressed release manifest and were
// fetched only to be dropped: on a cluster with many releases the Secrets list
// ran past the message broker's 16 MiB limit. They must be excluded in the
// request itself, not just after the transfer.
func TestFetchK8sSecretsFromRelay_ExcludesHelmReleasesAtSource(t *testing.T) {
	var params map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Body struct {
				ActionParams map[string]any `json:"action_params"`
			} `json:"body"`
		}
		_ = json.Unmarshal(body, &req)
		params = req.Body.ActionParams
		_, _ = w.Write([]byte(`{"data":{"findings":[]}}`))
	}))
	defer ts.Close()
	prev := config.Config.RelayServerEndpoint
	config.Config.RelayServerEndpoint = ts.URL
	defer func() { config.Config.RelayServerEndpoint = prev }()

	src := newTestSource(t)
	_, err := src.fetchK8sSecretsFromRelay(context.Background(), &core.SourceBuildRequest{CloudAccountID: "acct-1"})
	require.NoError(t, err)

	require.NotNil(t, params, "no request reached the relay")
	assert.Equal(t, "secrets", params["resource_type"])
	assert.Equal(t, true, params["all_namespaces"])
	assert.Equal(t, "type!=helm.sh/release.v1", params["field_selector"])
}
