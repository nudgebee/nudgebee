package azure

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
)

// forbiddenTransport answers every request with the 403 ARM returns to a
// Reader-only principal, and remembers which paths were asked for.
type forbiddenTransport struct {
	mu    sync.Mutex
	paths []string
}

func (f *forbiddenTransport) Do(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.paths = append(f.paths, req.URL.Path)
	f.mu.Unlock()
	body := `{"error":{"code":"AuthorizationFailed","message":"does not have authorization to perform action 'Microsoft.Web/sites/config/list/action'"}}`
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Status:     "403 Forbidden",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

type staticCredential struct{}

func (staticCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// captureRecords swaps recordPermissionError for the duration of the test.
func captureRecords(t *testing.T) *[]providers.PermissionAuditRecord {
	t.Helper()
	var mu sync.Mutex
	var got []providers.PermissionAuditRecord
	orig := recordPermissionError
	recordPermissionError = func(r providers.PermissionAuditRecord) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, r)
	}
	t.Cleanup(func() { recordPermissionError = orig })
	return &got
}

func auditPolicyClientOptions(transport policy.Transporter) policy.ClientOptions {
	return policy.ClientOptions{
		Transport:       transport,
		PerCallPolicies: []policy.Policy{&permissionAuditPolicy{info: &AzureAuditInfo{CloudAccountID: "acct-1", ServiceName: "microsoft.web/sites"}}},
	}
}

func TestPermissionAuditPolicy_RecordsForbiddenResponse(t *testing.T) {
	got := captureRecords(t)
	opts := auditPolicyClientOptions(&forbiddenTransport{})
	pipeline := runtime.NewPipeline("test", "v1.0.0", runtime.PipelineOptions{}, &opts)

	req, err := runtime.NewRequest(context.Background(), http.MethodGet,
		"https://management.azure.com/subscriptions/sub-1/providers/Microsoft.Web/sites?api-version=2023-01-01")
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := pipeline.Do(req)
	if err != nil {
		t.Fatalf("pipeline.Do: %v", err)
	}
	_ = resp.Body.Close()

	if len(*got) != 1 {
		t.Fatalf("recorded %d permission errors, want 1", len(*got))
	}
	if (*got)[0].ErrorCode != "AuthorizationFailed" {
		t.Fatalf("ErrorCode = %q, want AuthorizationFailed", (*got)[0].ErrorCode)
	}
}

// App settings and connection strings need config/list, which Reader lacks.
// The enrichment is best-effort, so its 403s must not become permission
// errors that tell the account owner to grant secret-reading access.
func TestFetchAppResourceRefs_DoesNotAuditExpectedForbidden(t *testing.T) {
	got := captureRecords(t)
	transport := &forbiddenTransport{}
	opts := auditPolicyClientOptions(transport)
	client, err := armappservice.NewWebAppsClient("sub-1", staticCredential{}, &arm.ClientOptions{ClientOptions: opts})
	if err != nil {
		t.Fatalf("NewWebAppsClient: %v", err)
	}

	id := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Web/sites/app-1"
	name := "app-1"
	refs := fetchAppResourceRefs(providers.NewCloudProviderContext(context.Background()), client, &armappservice.Site{ID: &id, Name: &name})

	if len(refs) != 0 {
		t.Fatalf("refs = %v, want none", refs)
	}
	var settings, connStrings bool
	for _, p := range transport.paths {
		settings = settings || strings.HasSuffix(p, "/config/appsettings/list")
		connStrings = connStrings || strings.HasSuffix(p, "/config/connectionstrings/list")
	}
	if !settings || !connStrings {
		t.Fatalf("expected both settings calls to be attempted, got paths %v", transport.paths)
	}
	if len(*got) != 0 {
		t.Fatalf("recorded %d permission errors for best-effort calls, want 0: %+v", len(*got), *got)
	}
}
