package middleware

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/relay-server/pkg/db"

	"github.com/gin-gonic/gin"
)

// fakeAgentStore embeds db.AgentStore so it satisfies the interface; only
// ValidateAgent is exercised by AgentAuthMiddleware, so the other methods are
// left unimplemented (they would panic if called, which they are not).
type fakeAgentStore struct {
	db.AgentStore
	validate func(ctx context.Context, accessKey, secret string) (bool, string, string, error)
}

func (f *fakeAgentStore) ValidateAgent(ctx context.Context, accessKey, secret string) (bool, string, string, error) {
	return f.validate(ctx, accessKey, secret)
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// TestAgentAuthMiddleware locks in the accepted Authorization header forms.
// Regression guard for #34684: the nudgebee-agent runner sends the credential
// as a bare base64 token with no "Basic " prefix; that form MUST be accepted.
func TestAgentAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Store accepts only "goodkey:goodsecret".
	store := &fakeAgentStore{
		validate: func(_ context.Context, accessKey, secret string) (bool, string, string, error) {
			if accessKey == "goodkey" && secret == "goodsecret" {
				return true, "acct-1", "k8s", nil
			}
			return false, "", "", nil
		},
	}

	valid := b64("goodkey:goodsecret")

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{"bare token (runner form) is accepted", valid, http.StatusOK},
		{"Basic prefix is accepted", "Basic " + valid, http.StatusOK},
		{"lowercase basic prefix is accepted", "basic " + valid, http.StatusOK},
		{"uppercase BASIC prefix is accepted", "BASIC " + valid, http.StatusOK},
		{"leading/trailing whitespace is tolerated", "  Basic " + valid + "  ", http.StatusOK},
		{"wrong scheme is rejected", "Bearer " + valid, http.StatusUnauthorized},
		{"missing header is rejected", "", http.StatusUnauthorized},
		{"invalid base64 is rejected", "!!!not-base64!!!", http.StatusUnauthorized},
		{"token without colon is rejected", b64("nocolon"), http.StatusUnauthorized},
		{"unknown credentials are rejected", b64("goodkey:wrongsecret"), http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/register", AgentAuthMiddleware(store), func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/register", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d", w.Code, tt.want)
			}
		})
	}
}
