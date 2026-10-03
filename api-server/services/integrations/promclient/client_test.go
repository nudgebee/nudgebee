package promclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPrometheusProbeServer answers the label-values probe with 200 and hands the
// request to observe, so a test can assert on what the client sent.
func newPrometheusProbeServer(t *testing.T, observe func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observe(w, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":["up"]}`))
	}))
}

// probeWith issues the request the integration's Test Connection makes, through
// the client alone.
func probeWith(values map[string]string) error {
	cfg, err := NewPrometheusUserConfig(values)
	if err != nil {
		return err
	}
	resp, err := cfg.DoGet(context.Background(), "/api/v1/label/__name__/values", neturl.Values{"limit": []string{"1"}})
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// resetAzureTokenCache clears cached tokens so cases don't leak into each other.
func resetAzureTokenCache(t *testing.T) {
	t.Helper()
	azureTokenMu.Lock()
	azureTokenCache = map[string]azureTokenEntry{}
	azureTokenMu.Unlock()
}

// ----- header parsing -------------------------------------------------------

func TestParsePrometheusHeaders(t *testing.T) {
	t.Run("multiline", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders("X-Scope-OrgID: tenant-1\nX-Extra: value")
		require.NoError(t, err)
		assert.Equal(t, "tenant-1", headers.Get("X-Scope-OrgID"))
		assert.Equal(t, "value", headers.Get("X-Extra"))
	})

	// The agent takes this form from PROMETHEUS_HEADERS, so a config copied
	// across from an agent install must parse.
	t.Run("comma separated", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders("X-Scope-OrgID: tenant-1, X-Extra: value")
		require.NoError(t, err)
		assert.Equal(t, "tenant-1", headers.Get("X-Scope-OrgID"))
		assert.Equal(t, "value", headers.Get("X-Extra"))
	})

	// A value may legitimately contain a comma, so a line that already parses
	// as one header is never re-split.
	t.Run("value containing comma is kept whole", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders("X-List: a, b, c")
		require.NoError(t, err)
		assert.Equal(t, "a, b, c", headers.Get("X-List"))
	})

	t.Run("blank input", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders("  \n\n ")
		require.NoError(t, err)
		assert.Nil(t, headers)
	})

	t.Run("malformed line", func(t *testing.T) {
		_, err := ParsePrometheusHeaders("not-a-header")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected")
	})

	// The JSON form matches the shape llm_extra_headers already uses, so an
	// operator who learned one field does not have to learn a second syntax.
	t.Run("json object", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders(`{"X-Scope-OrgID": "tenant-1", "X-Extra": "value"}`)
		require.NoError(t, err)
		assert.Equal(t, "tenant-1", headers.Get("X-Scope-OrgID"))
		assert.Equal(t, "value", headers.Get("X-Extra"))
	})

	// A repeated header is what the line form gets from writing the name twice.
	t.Run("json array value repeats the header", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders(`{"X-Repeat": ["a", "b"]}`)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, headers.Values("X-Repeat"))
	})

	t.Run("json values are trimmed", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders(`{"X-Scope-OrgID": "  tenant-1  "}`)
		require.NoError(t, err)
		assert.Equal(t, "tenant-1", headers.Get("X-Scope-OrgID"))
	})

	t.Run("empty json object", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders("{}")
		require.NoError(t, err)
		assert.Nil(t, headers)
	})

	// A leading brace commits to JSON: the error must name the real problem
	// rather than the line parser's "expected \"Header: value\"".
	t.Run("malformed json is not retried as lines", func(t *testing.T) {
		_, err := ParsePrometheusHeaders(`{"X-Scope-OrgID": }`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid JSON header object")
	})

	t.Run("json non-string value", func(t *testing.T) {
		_, err := ParsePrometheusHeaders(`{"X-Count": 3}`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be a string or an array of strings")
	})

	t.Run("json array with non-string item", func(t *testing.T) {
		_, err := ParsePrometheusHeaders(`{"X-Repeat": ["a", 2]}`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "array values must be strings")
	})
}

func TestParsePrometheusAdditionalLabels(t *testing.T) {
	t.Run("single label", func(t *testing.T) {
		got, err := ParsePrometheusAdditionalLabels(`{"cluster": "prod"}`)
		require.NoError(t, err)
		assert.Equal(t, `cluster="prod"`, got)
	})

	// Go map order is randomised, so the generated fragment must be sorted or the
	// same config would produce a different query on every call.
	t.Run("multiple labels are sorted", func(t *testing.T) {
		got, err := ParsePrometheusAdditionalLabels(`{"region": "us", "cluster": "prod"}`)
		require.NoError(t, err)
		assert.Equal(t, `cluster="prod",region="us"`, got)
	})

	t.Run("empty", func(t *testing.T) {
		got, err := ParsePrometheusAdditionalLabels("  ")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	// A configured value lands inside every query the builders produce, so a
	// quote must not be able to close the matcher and append arbitrary PromQL.
	t.Run("value quotes are escaped", func(t *testing.T) {
		got, err := ParsePrometheusAdditionalLabels(`{"cluster": "pr\"od"}`)
		require.NoError(t, err)
		assert.Equal(t, `cluster="pr\"od"`, got)
	})

	t.Run("rejects invalid label name", func(t *testing.T) {
		_, err := ParsePrometheusAdditionalLabels(`{"cluster\"} or up{": "x"}`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid label name")
	})

	t.Run("rejects non-object", func(t *testing.T) {
		_, err := ParsePrometheusAdditionalLabels(`cluster="prod"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be a JSON object")
	})

	t.Run("rejects non-string value", func(t *testing.T) {
		_, err := ParsePrometheusAdditionalLabels(`{"cluster": 3}`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be a JSON object")
	})
}

func TestExpandClusterPlaceholder(t *testing.T) {
	// The templates write __CLUSTER__ with no comma after it, so the expansion
	// supplies the separator -- matching the relay-server byte for byte.
	t.Run("configured", func(t *testing.T) {
		got := ExpandClusterPlaceholder(`node_cpu_seconds_total{__CLUSTER__ mode!="idle"}`, `cluster="prod"`)
		// Two spaces before `mode`: one from the " , " separator, one already in the
		// template. The relay-server produces exactly the same string, and PromQL
		// ignores the whitespace -- asserted verbatim to keep the paths identical.
		assert.Equal(t, `node_cpu_seconds_total{cluster="prod" ,  mode!="idle"}`, got)
	})

	// The unconfigured case is the actual bug fix: the token must not survive
	// into the request, or the backend rejects the query outright.
	t.Run("unconfigured drops the token", func(t *testing.T) {
		got := ExpandClusterPlaceholder(`node_cpu_seconds_total{__CLUSTER__ mode!="idle"}`, "")
		assert.Equal(t, `node_cpu_seconds_total{ mode!="idle"}`, got)
		assert.NotContains(t, got, ClusterPlaceholder)
	})

	t.Run("bare selector unconfigured", func(t *testing.T) {
		assert.Equal(t, `kube_node_info{}`, ExpandClusterPlaceholder(`kube_node_info{__CLUSTER__}`, ""))
	})

	// An `or`-joined query carries the token once per selector; injectPromQLMatchers
	// only rewrites the first, so this replacement has to cover them all.
	t.Run("every occurrence is replaced", func(t *testing.T) {
		in := `sum(a{__CLUSTER__ x="1"}) or sum(b{__CLUSTER__ y="2"})`
		got := ExpandClusterPlaceholder(in, `cluster="prod"`)
		assert.NotContains(t, got, ClusterPlaceholder)
		assert.Equal(t, 2, strings.Count(got, `cluster="prod"`))
	})
}

// ----- azure ad -------------------------------------------------------------

func TestAzureAD_MintsAndCachesToken(t *testing.T) {
	resetAzureTokenCache(t)
	t.Cleanup(func() { resetAzureTokenCache(t) })

	var tokenRequests int
	var lastForm neturl.Values
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		require.NoError(t, r.ParseForm())
		lastForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"azure-token","expires_in":"3600"}`))
	}))
	defer tokenServer.Close()

	original := azureTokenEndpointBase
	azureTokenEndpointBase = tokenServer.URL
	t.Cleanup(func() { azureTokenEndpointBase = original })

	var got *http.Request
	promServer := newPrometheusProbeServer(t, func(_ http.ResponseWriter, r *http.Request) { got = r.Clone(r.Context()) })
	defer promServer.Close()

	config := map[string]string{
		PrometheusURLKey:               promServer.URL,
		PrometheusAuthTypeKey:          PrometheusAuthAzureAD,
		PrometheusAzureClientIDKey:     "client-id",
		PrometheusAzureClientSecretKey: "client-secret",
		PrometheusAzureTenantIDKey:     "tenant-id",
	}

	require.NoError(t, probeWith(config))
	require.NotNil(t, got)
	assert.Equal(t, "Bearer azure-token", got.Header.Get("Authorization"))
	assert.Equal(t, "client_credentials", lastForm.Get("grant_type"))
	assert.Equal(t, "client-id", lastForm.Get("client_id"))
	assert.Equal(t, "client-secret", lastForm.Get("client_secret"))
	assert.Equal(t, PrometheusDefaultAzureResource, lastForm.Get("resource"))
	assert.Equal(t, 1, tokenRequests)

	// A second call reuses the cached token rather than minting another.
	require.NoError(t, probeWith(config))
	assert.Equal(t, 1, tokenRequests, "token should be served from cache")
}

func TestAzureAD_RemintsExpiredToken(t *testing.T) {
	resetAzureTokenCache(t)
	t.Cleanup(func() { resetAzureTokenCache(t) })

	var tokenRequests int
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		// expires_in below the 60s refresh margin, so the token is never reusable.
		_, _ = w.Write([]byte(`{"access_token":"short-lived","expires_in":30}`))
	}))
	defer tokenServer.Close()

	original := azureTokenEndpointBase
	azureTokenEndpointBase = tokenServer.URL
	t.Cleanup(func() { azureTokenEndpointBase = original })

	promServer := newPrometheusProbeServer(t, func(http.ResponseWriter, *http.Request) {})
	defer promServer.Close()

	config := map[string]string{
		PrometheusURLKey:               promServer.URL,
		PrometheusAuthTypeKey:          PrometheusAuthAzureAD,
		PrometheusAzureClientIDKey:     "client-id",
		PrometheusAzureClientSecretKey: "client-secret",
		PrometheusAzureTenantIDKey:     "tenant-id",
	}

	require.NoError(t, probeWith(config))
	require.NoError(t, probeWith(config))
	assert.Equal(t, 2, tokenRequests, "an expiring token must be re-minted")
}

// azureTokenMu is process-wide, so holding it across the Azure AD round trip would
// serialise token minting across every tenant — one slow response stalling all of
// them. Distinct tenants are used deliberately: they occupy different cache keys
// and share nothing but the mutex, so if they cannot mint concurrently the lock is
// being held across the network call again.
func TestAzureAD_MintsConcurrentlyAcrossTenants(t *testing.T) {
	resetAzureTokenCache(t)
	t.Cleanup(func() { resetAzureTokenCache(t) })

	const callers = 4

	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	allArrived := make(chan struct{})
	var once sync.Once

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		reached := inFlight == callers
		mu.Unlock()

		// Hold every request open until all of them have arrived. If the mutex were
		// still held across the mint, only one could ever be in flight and this
		// would time out instead of releasing.
		if reached {
			once.Do(func() { close(allArrived) })
		}
		select {
		case <-allArrived:
		case <-time.After(5 * time.Second):
		}

		mu.Lock()
		inFlight--
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"azure-token","expires_in":"3600"}`))
	}))
	defer tokenServer.Close()

	original := azureTokenEndpointBase
	azureTokenEndpointBase = tokenServer.URL
	t.Cleanup(func() { azureTokenEndpointBase = original })

	var wg sync.WaitGroup
	errs := make([]error, callers)
	tokens := make([]string, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := PrometheusUserConfig{
				AzureTenantID:     fmt.Sprintf("tenant-%d", i),
				AzureClientID:     "client-id",
				AzureClientSecret: "client-secret",
				AzureResource:     PrometheusDefaultAzureResource,
			}
			tokens[i], errs[i] = cfg.azureBearer(context.Background())
		}(i)
	}
	wg.Wait()

	for i := 0; i < callers; i++ {
		require.NoError(t, errs[i])
		assert.Equal(t, "azure-token", tokens[i])
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, callers, maxInFlight, "tenants must mint concurrently; the cache lock must not be held across the token request")
}

func TestAzureTokenExpiry(t *testing.T) {
	t.Run("prefers absolute expires_on", func(t *testing.T) {
		want := time.Now().Add(2 * time.Hour).Unix()
		got := azureTokenExpiry(json.Number(strconv.FormatInt(want, 10)), json.Number("60"))
		assert.Equal(t, want, got.Unix())
	})
	t.Run("falls back to expires_in", func(t *testing.T) {
		got := azureTokenExpiry(json.Number("0"), json.Number("600"))
		assert.WithinDuration(t, time.Now().Add(600*time.Second), got, time.Minute)
	})
	t.Run("defaults when neither parses", func(t *testing.T) {
		got := azureTokenExpiry("", "")
		assert.WithinDuration(t, time.Now().Add(5*time.Minute), got, time.Minute)
	})
}

func TestNewPrometheusUserConfig_Defaults(t *testing.T) {
	cfg, err := NewPrometheusUserConfig(map[string]string{
		PrometheusURLKey: "https://prometheus.example.com/prometheus/",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://prometheus.example.com/prometheus", cfg.URL, "trailing slash trimmed, path kept")
	assert.Equal(t, PrometheusAuthNone, cfg.AuthType)
	assert.Equal(t, PrometheusDefaultAWSService, cfg.AWSService)
	assert.Equal(t, PrometheusDefaultAzureResource, cfg.AzureResource)
}

// A ruler is opt-in: an integration that never mentions one has none, and one that
// declares a ruler without its own URL shares the query endpoint.
func TestNewPrometheusUserConfig_RulerDefaults(t *testing.T) {
	cfg, err := NewPrometheusUserConfig(map[string]string{PrometheusURLKey: "https://mimir.example.com/prometheus"})
	require.NoError(t, err)
	assert.Equal(t, PrometheusRulerNone, cfg.RulerType)
	assert.False(t, cfg.HasRuler())
	assert.Equal(t, "https://mimir.example.com/prometheus", cfg.RulerURL)

	cfg, err = NewPrometheusUserConfig(map[string]string{
		PrometheusURLKey:       "https://query.example.com/prometheus",
		PrometheusRulerTypeKey: PrometheusRulerMimirCortex,
		PrometheusRulerURLKey:  "https://ruler.example.com/prometheus/",
	})
	require.NoError(t, err)
	assert.True(t, cfg.HasRuler())
	ruler := cfg.Ruler()
	assert.Equal(t, "https://ruler.example.com/prometheus", ruler.URL, "ruler calls go to the ruler base, trailing slash trimmed")
	assert.Equal(t, cfg.AuthType, ruler.AuthType, "same connection, only the base URL differs")
	assert.Equal(t, "https://query.example.com/prometheus", cfg.URL, "the query endpoint is untouched")
}

// A ruler write is a signed POST with a body: the body and content type must
// arrive, the request must be signed, and the payload hash the signature covers
// must be the body's — the empty-payload hash only verifies a body-less request.
func TestDo_PostsBodyAndSignsIt(t *testing.T) {
	var gotAuth, gotContentType, gotBody, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	cfg, err := NewPrometheusUserConfig(map[string]string{
		PrometheusURLKey:                server.URL,
		PrometheusAuthTypeKey:           PrometheusAuthAWSSigV4,
		PrometheusAWSAccessKeyIDKey:     "AKIAEXAMPLE",
		PrometheusAWSSecretAccessKeyKey: "secret",
		PrometheusAWSRegionKey:          "us-east-1",
	})
	require.NoError(t, err)
	resp, err := cfg.Do(context.Background(), http.MethodPost, "/rules", nil, "application/yaml", []byte("name: x"))
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "name: x", gotBody)
	assert.Equal(t, "application/yaml", gotContentType)
	assert.Contains(t, gotAuth, "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/")

	assert.Equal(t, emptyPayloadHash, payloadHash(nil))
	assert.NotEqual(t, emptyPayloadHash, payloadHash([]byte("name: x")))
}
