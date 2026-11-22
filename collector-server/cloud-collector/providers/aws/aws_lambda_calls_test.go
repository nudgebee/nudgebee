package aws

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLambdaAPI serves the Lambda REST calls the lister makes and records them as
// "<METHOD> <path>". Function "tagged" has tags and reserved concurrency 5,
// function "plain" has neither. ListTags and GetFunctionConcurrency fail, so a
// value that still arrives must have come from GetFunction.
type fakeLambdaAPI struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeLambdaAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+path)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	fn := func(name string) string {
		return fmt.Sprintf(`{"FunctionName":%q,"FunctionArn":"arn:aws:lambda:us-east-1:123456789012:function:%s","Runtime":"python3.12"}`, name, name)
	}
	switch {
	case path == "/2015-03-31/functions" || path == "/2015-03-31/functions/":
		_, _ = fmt.Fprintf(w, `{"Functions":[%s,%s]}`, fn("tagged"), fn("plain"))
	case path == "/2015-03-31/functions/tagged":
		_, _ = fmt.Fprintf(w, `{"Configuration":%s,"Tags":{"team":"sre"},"Concurrency":{"ReservedConcurrentExecutions":5}}`, fn("tagged"))
	case path == "/2015-03-31/functions/plain":
		_, _ = fmt.Fprintf(w, `{"Configuration":%s}`, fn("plain"))
	case strings.HasSuffix(path, "/urls"):
		_, _ = w.Write([]byte(`{"FunctionUrlConfigs":[]}`))
	case strings.HasSuffix(path, "/provisioned-concurrency"):
		_, _ = w.Write([]byte(`{"ProvisionedConcurrencyConfigs":[]}`))
	default:
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"__type":"UnexpectedCall"}`))
	}
}

func (f *fakeLambdaAPI) count(match func(call string) bool) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if match(c) {
			n++
		}
	}
	return n
}

func isGetFunction(call string) bool {
	return call == "GET /2015-03-31/functions/tagged" || call == "GET /2015-03-31/functions/plain"
}

func isTagsOrConcurrencyCall(call string) bool {
	return strings.Contains(call, "/tags/") || strings.HasSuffix(call, "/concurrency")
}

// A full sync takes each function's tags and reserved concurrency from one
// GetFunction, not from a ListTags and a GetFunctionConcurrency call: those two
// count against Lambda's 15-per-second control-plane quota, which the sync shares
// with the customer's own tooling.
func TestLambdaGetResourcesTakesTagsAndConcurrencyFromGetFunction(t *testing.T) {
	api := &fakeLambdaAPI{}
	pointAwsSdkAt(t, api)
	ctx := &targetedFetchTestCtx{ctx: context.Background()}

	got, err := (&awsLambda{}).GetResources(ctx, providers.Account{AccountNumber: fakeAwsAccount}, fakeAwsRegion)
	require.NoError(t, err)
	require.Len(t, got, 2)

	byName := map[string]providers.Resource{}
	for _, r := range got {
		byName[r.Name] = r
	}
	assert.Equal(t, map[string][]string{"team": {"sre"}}, byName["tagged"].Tags)
	assert.EqualValues(t, 5, *(byName["tagged"].Meta["Concurrency"].(*int32)))
	assert.Empty(t, byName["plain"].Tags)
	require.Contains(t, byName["plain"].Meta, "Concurrency", "no reserved concurrency is recorded as nil, not left out")
	assert.Nil(t, byName["plain"].Meta["Concurrency"])

	assert.Equal(t, 2, api.count(isGetFunction), "one GetFunction per function")
	assert.Zero(t, api.count(isTagsOrConcurrencyCall))
}

// The targeted lookup already has the GetFunction output and must not fetch it again.
func TestLambdaGetResourcesByIdsReusesItsGetFunction(t *testing.T) {
	api := &fakeLambdaAPI{}
	pointAwsSdkAt(t, api)
	ctx := &targetedFetchTestCtx{ctx: context.Background()}

	got, err := (&awsLambda{}).GetResourcesByIds(ctx, providers.Account{AccountNumber: fakeAwsAccount}, fakeAwsRegion, []string{"tagged"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, map[string][]string{"team": {"sre"}}, got[0].Tags)
	assert.EqualValues(t, 5, *(got[0].Meta["Concurrency"].(*int32)))

	assert.Equal(t, 1, api.count(isGetFunction))
	assert.Zero(t, api.count(isTagsOrConcurrencyCall))
}
