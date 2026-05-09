package integrations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFetchOAuthToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

		err := r.ParseForm()
		assert.NoError(t, err)
		assert.Equal(t, "client_credentials", r.Form.Get("grant_type"))
		assert.Equal(t, "api.read", r.Form.Get("scope"))
		// client_id/client_secret sent via Basic Auth only (RFC 6749 §2.3.1)
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "test-client", user)
		assert.Equal(t, "test-secret", pass)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oauthTokenResponse{
			AccessToken: "test-token",
			ExpiresIn:   3600,
		})
	}))
	defer server.Close()

	token, err := FetchOAuthToken(server.URL, "test-client", "test-secret", "api.read", "")
	assert.NoError(t, err)
	assert.Equal(t, "test-token", token)
}

func TestFetchOAuthToken_FreshTokenEveryCall(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oauthTokenResponse{
			AccessToken: "token",
			ExpiresIn:   3600,
		})
	}))
	defer server.Close()

	_, _ = FetchOAuthToken(server.URL, "client", "secret", "", "")
	_, _ = FetchOAuthToken(server.URL, "client", "secret", "", "")
	assert.Equal(t, 2, callCount) // No caching — each call fetches a new token
}

func TestFetchOAuthToken_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": "invalid_client"}`))
	}))
	defer server.Close()

	_, err := FetchOAuthToken(server.URL, "bad", "bad", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestFetchOAuthToken_WithAudience(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseForm()
		assert.NoError(t, err)
		assert.Equal(t, "my-audience", r.Form.Get("audience"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oauthTokenResponse{
			AccessToken: "audience-token",
			ExpiresIn:   3600,
		})
	}))
	defer server.Close()

	token, err := FetchOAuthToken(server.URL, "client", "secret", "", "my-audience")
	assert.NoError(t, err)
	assert.Equal(t, "audience-token", token)
}
