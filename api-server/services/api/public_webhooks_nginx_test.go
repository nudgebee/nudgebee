package api

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The app's nginx sidecar forwards /api/webhooks/* to services-server only for
// the names in its location regex; anything else falls through to Next.js and
// returns a 404 HTML page. A webhook registered here but missing from that
// regex is therefore unreachable in every deployment while still passing every
// handler test — which is how /api/webhooks/cubeapm shipped broken.
var nginxWebhookLocation = regexp.MustCompile(`location\s+~\s+(\^/api/webhooks/\([^)]*\)/\?\$)`)

func TestNginxRoutesEveryPublicWebhook(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handlePublicWebhooksApis(r, nil, nil, slog.Default())

	var paths []string
	for _, route := range r.Routes() {
		if strings.HasPrefix(route.Path, "/api/webhooks/") {
			paths = append(paths, route.Path)
		}
	}
	require.NotEmpty(t, paths, "no /api/webhooks routes registered")

	// Only the compose nginx routes webhooks by regex here; the Helm chart lists
	// each webhook path in the app Ingress instead.
	for _, file := range []string{
		"deploy/compose/nginx.conf",
	} {
		t.Run(file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", file))
			require.NoError(t, err)

			m := nginxWebhookLocation.FindSubmatch(raw)
			require.NotNil(t, m, "no /api/webhooks location regex found")
			location := regexp.MustCompile(string(m[1]))

			for _, path := range paths {
				require.True(t, location.MatchString(path),
					"%s is registered in handlePublicWebhooksApis but not routed by %s — add it to the location regex", path, file)
			}
		})
	}
}
