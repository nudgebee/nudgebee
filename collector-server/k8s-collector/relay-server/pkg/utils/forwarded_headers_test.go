package utils

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"nudgebee/relay-server/pkg/models"
)

func TestProxySerializersDoNotForwardRelayAuthHeader(t *testing.T) {
	serializers := []struct {
		name      string
		path      string
		serialize func(*http.Request) (*models.HTTPRequest, error)
	}{
		{name: "grafana", path: "http://relay.test/grafana/api", serialize: SerializeHTTPRequest},
		{name: "prometheus", path: "http://relay.test/prometheus-v2/api", serialize: SerializePrometheusHTTPRequest},
		{name: "api proxy", path: "http://relay.test/api/proxy/v1", serialize: SerializeAPIProxyRequest},
	}

	for _, tc := range serializers {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, strings.NewReader(""))
			req.Header.Set("X-SECRET-KEY", "relay-secret")
			req.Header["x-secret-key"] = []string{"noncanonical-relay-secret"}
			req.Header.Set("Authorization", "Bearer upstream-token")
			req.Header.Set("Content-Type", "application/json")

			got, err := tc.serialize(req)
			require.NoError(t, err)
			forwarded := http.Header(got.Header)
			require.Empty(t, forwarded.Get("X-SECRET-KEY"))
			for key := range forwarded {
				require.False(t, strings.EqualFold(key, "X-SECRET-KEY"))
			}
			require.Equal(t, "Bearer upstream-token", forwarded.Get("Authorization"))
			require.Equal(t, "application/json", forwarded.Get("Content-Type"))
			require.Equal(t, "relay-secret", req.Header.Get("X-SECRET-KEY"))
		})
	}
}
