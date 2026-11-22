package observability

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type foragerCall struct {
	account, method, url string
	header               map[string][]string
	body                 []byte
}

// patchSignozForager answers SigNoz through the relay's http_request action as a legacy
// (v1 login) release: the sessions probe is a 404, the login returns a JWT, any other
// path returns queryBody. It records every call.
func patchSignozForager(t *testing.T, queryBody string, calls *[]foragerCall) {
	t.Helper()
	patches := gomonkey.NewPatches()
	patches.ApplyFunc(relay.ExecuteProxyWithAgentTypeContext,
		func(_ context.Context, account, action, _ string, params map[string]any, _ int, _ string) (map[string]any, error) {
			require.Equal(t, "http_request", action)
			call := foragerCall{account: account, method: params["method"].(string), url: params["url"].(string)}
			call.header, _ = params["header"].(map[string][]string)
			if enc, ok := params["body"].(string); ok {
				call.body, _ = base64.StdEncoding.DecodeString(enc)
			}
			*calls = append(*calls, call)

			status, body := 200, queryBody
			switch {
			case strings.HasPrefix(call.url, "/api/v2/sessions/context"):
				status, body = 404, `{}`
			case call.url == "/api/v1/login":
				body = `{"accessJwt":"jwt-1"}`
			}
			return map[string]any{
				"status_code": float64(status),
				"body":        base64.StdEncoding.EncodeToString([]byte(body)),
			}, nil
		})
	t.Cleanup(patches.Reset)
}

const signozListResponse = `{"data":{"result":[{"list":[` +
	`{"timestamp":"2024-01-01T00:00:00Z","data":{"body":"boom failed","severity_text":"ERROR"}}]}]}}`

func signozForagerDto(extra map[string]string) []core.IntegrationDto {
	values := map[string]string{
		"connection_mode": "vm_agent",
		"signoz_mode":     "self_hosted",
		"signoz_url":      "http://10.0.0.9:3301",
	}
	for k, v := range extra {
		values[k] = v
	}
	dtos := lokiIntegrationDto(values)
	dtos[0].Id, dtos[0].Type = "int-signoz", "signoz"
	return dtos
}

func queryOnce(t *testing.T, account string) []OutputLog {
	t.Helper()
	logs, err := (&SignozSaasSource{}).QueryLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: account,
		Query:     `[{"key":{"key":"host"},"op":"=","value":"vm-1"}]`,
		StartTime: 1_700_000_000_000,
		EndTime:   1_700_000_600_000,
	})
	require.NoError(t, err)
	return logs
}

// A vm_agent SigNoz must log in and query through the forager (never dial signoz_url),
// hand it paths rather than absolute URLs, and send the JWT it just obtained.
func TestSignozSaasQueryLogs_ViaForager_Password(t *testing.T) {
	patchLokiIntegration(t, signozForagerDto(map[string]string{
		"signoz_username": "ops@corp.example",
		"signoz_password": "pw-viaforager",
	}))
	var calls []foragerCall
	patchSignozForager(t, signozListResponse, &calls)

	require.Len(t, queryOnce(t, "acc-1"), 1)

	require.NotEmpty(t, calls)
	var login, query *foragerCall
	for i := range calls {
		switch calls[i].url {
		case "/api/v1/login":
			login = &calls[i]
		case "/api/v4/query_range":
			query = &calls[i]
		}
		assert.True(t, strings.HasPrefix(calls[i].url, "/"), "the forager owns the base URL and rejects absolute URLs: %q", calls[i].url)
	}
	require.NotNil(t, login)
	require.NotNil(t, query)

	var creds map[string]string
	require.NoError(t, json.Unmarshal(login.body, &creds))
	assert.Equal(t, "ops@corp.example", creds["email"])
	assert.Equal(t, "pw-viaforager", creds["password"])

	assert.Equal(t, "POST", query.method)
	assert.Equal(t, []string{"Bearer jwt-1"}, query.header["Authorization"])
	assert.NotContains(t, string(query.body), "pw-viaforager", "the password must not ride along on queries")
}

// An API key never logs in: the key goes on the single query request.
func TestSignozSaasQueryLogs_ViaForager_APIKey(t *testing.T) {
	patchLokiIntegration(t, signozForagerDto(map[string]string{
		"signoz_auth_type": "api_key",
		"signoz_api_key":   "key-1",
	}))
	var calls []foragerCall
	patchSignozForager(t, signozListResponse, &calls)

	require.Len(t, queryOnce(t, "acc-1"), 1)

	require.Len(t, calls, 1)
	assert.Equal(t, "/api/v4/query_range", calls[0].url)
	assert.Equal(t, []string{"key-1"}, calls[0].header["Signoz-Api-Key"])
}

// Two accounts behind two foragers can share a private URL and credentials. A token
// cached for one must not authenticate queries for the other.
func TestSignozSaasQueryLogs_ViaForager_TokenNotSharedAcrossAccounts(t *testing.T) {
	patchLokiIntegration(t, signozForagerDto(map[string]string{
		"signoz_username": "admin@signoz.local",
		"signoz_password": "pw-shared-across-accounts",
	}))
	var calls []foragerCall
	patchSignozForager(t, signozListResponse, &calls)

	queryOnce(t, "acc-a")
	queryOnce(t, "acc-b")

	logins := map[string]int{}
	for _, c := range calls {
		if c.url == "/api/v1/login" {
			logins[c.account]++
		}
	}
	assert.Equal(t, map[string]int{"acc-a": 1, "acc-b": 1}, logins, "each account logs in to its own SigNoz")
}
