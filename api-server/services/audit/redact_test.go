package audit

import (
	"encoding/json"
	model "nudgebee/services/internal/database/models"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func strptr(s string) *string { return &s }

// TestRedactValue_RealAccountStruct locks that the exact models.Account struct
// published on the account-CREATE audit path (EventState = newAccount from
// GetAccount) has all of its credential fields redacted — access_key,
// access_secret, agent_access_secret, access_secret_v2 — plus any secret nested
// in the `data` JSON string. Guards against future field/tag changes.
func TestRedactValue_RealAccountStruct(t *testing.T) {
	acc := model.Account{
		Id:                "a1",
		AccountName:       "prod",
		AccessKey:         strptr("AKIAIOSFODNN7EXAMPLE"),
		AccessSecret:      strptr("top-secret"),
		AgentAccessKey:    strptr("agent-key"),
		AgentAccessSecret: strptr("agent-secret"),
		AccessSecretV2:    strptr("v2-secret"),
		Data:              strptr(`{"project_id":"p","private_key":"LEAKED-KEY"}`),
	}
	got := redactValue(acc).(map[string]any)

	for _, k := range []string{"access_key", "access_secret", "agent_access_key", "agent_access_secret", "access_secret_v2"} {
		assert.Equalf(t, redactedPlaceholder, got[k], "field %q must be redacted", k)
	}
	assert.Equal(t, "prod", got["account_name"]) // non-secret preserved

	dataStr := got["data"].(string)
	assert.NotContains(t, dataStr, "LEAKED-KEY")
	assert.Contains(t, dataStr, redactedPlaceholder)
	assert.Contains(t, dataStr, "project_id")
}

func TestIsSensitiveKey(t *testing.T) {
	sensitive := []string{
		"access_secret", "AccessSecret", "access-secret", "access_secret_v2",
		"access_key", "agent_access_key", "AWS_ACCESS_KEY_ID",
		"password", "passwd", "user_password", "passphrase",
		"api_key", "apiKey", "secret_key", "client_secret", "webhook_secret",
		"access_token", "refresh_token", "bearer_token", "session_token", "token",
		"private_key", "signing_key", "encryption_key", "private_key_id",
		"credential", "credentials", "aws_credentials", "Authorization", "cookie", "bearer",
		"access_token_v2",
	}
	for _, k := range sensitive {
		assert.Truef(t, isSensitiveKey(k), "expected %q to be sensitive", k)
	}

	// Benign identifiers, including ones a naive substring match would wrongly
	// flag (the finding: verification_token_expiration, token_count, ...).
	benign := []string{
		"id", "name", "resource_key", "primary_key", "match_value",
		"resource_type", "region", "status", "public_key", "monkey", "",
		"verification_token_expiration", "token_count", "tokens_used",
		"cookie_policy", "credential_type", "authorization_status", "bearer_name",
	}
	for _, k := range benign {
		assert.Falsef(t, isSensitiveKey(k), "expected %q to be benign", k)
	}
}

func TestRedactValue_TopLevel(t *testing.T) {
	in := map[string]any{
		"id":            "acct-1",
		"account_name":  "prod",
		"access_key":    "AKIA...",
		"access_secret": "super-secret",
		"data":          map[string]any{"cur_source": "access_keys"},
	}
	got := redactValue(in).(map[string]any)

	assert.Equal(t, "acct-1", got["id"])
	assert.Equal(t, "prod", got["account_name"])
	assert.Equal(t, redactedPlaceholder, got["access_key"])
	assert.Equal(t, redactedPlaceholder, got["access_secret"])
}

func TestRedactValue_Nested(t *testing.T) {
	in := map[string]any{
		"data": map[string]any{
			"client_secret": "shh",
			"cur_source":    "access_keys",
			"nested": map[string]any{
				"api_token": "tok",
				"keep":      "me",
			},
		},
		"agents": []any{
			map[string]any{"access_secret": "a", "kind": "k8s"},
			map[string]any{"access_secret": "b", "kind": "cloud"},
		},
	}
	got := redactValue(in).(map[string]any)

	data := got["data"].(map[string]any)
	assert.Equal(t, redactedPlaceholder, data["client_secret"])
	assert.Equal(t, "access_keys", data["cur_source"])
	nested := data["nested"].(map[string]any)
	assert.Equal(t, redactedPlaceholder, nested["api_token"])
	assert.Equal(t, "me", nested["keep"])

	agents := got["agents"].([]any)
	assert.Equal(t, redactedPlaceholder, agents[0].(map[string]any)["access_secret"])
	assert.Equal(t, "k8s", agents[0].(map[string]any)["kind"])
	assert.Equal(t, redactedPlaceholder, agents[1].(map[string]any)["access_secret"])
}

func TestRedactValue_Struct(t *testing.T) {
	type account struct {
		ID           string `json:"id"`
		Name         string `json:"account_name"`
		AccessKey    string `json:"access_key"`
		AccessSecret string `json:"access_secret"`
	}
	got := redactValue(account{ID: "1", Name: "n", AccessKey: "ak", AccessSecret: "as"}).(map[string]any)
	assert.Equal(t, "1", got["id"])
	assert.Equal(t, "n", got["account_name"])
	assert.Equal(t, redactedPlaceholder, got["access_key"])
	assert.Equal(t, redactedPlaceholder, got["access_secret"])
}

// TestRedactValue_JSONInString covers the GCP-onboarding leak: a struct field
// (models.Account.Data *string) holding serialized-JSON credentials must have
// its interior redacted even though it is a scalar string on the parent object.
func TestRedactValue_JSONInString(t *testing.T) {
	in := map[string]any{
		"account_name": "gcp-prod",
		"data":         `{"project_id":"p","private_key":"LEAKED-KEY","client_email":"x@y.iam"}`,
	}
	got := redactValue(in).(map[string]any)
	assert.Equal(t, "gcp-prod", got["account_name"])

	dataStr, ok := got["data"].(string)
	assert.True(t, ok, "data stays a JSON string")
	assert.NotContains(t, dataStr, "LEAKED-KEY")
	assert.Contains(t, dataStr, redactedPlaceholder)
	assert.Contains(t, dataStr, "project_id") // non-secret siblings preserved

	// Mixed text (JSON + trailing prose) is not treated as pure JSON, so its
	// remainder is preserved and only content-scrubbed.
	mixed := redactValue(map[string]any{"output": `{"ok":true}` + "\nWarning: partial"}).(map[string]any)
	assert.Contains(t, mixed["output"].(string), "Warning: partial")
}

// TestRedactValue_ScrubValueContent covers secret shapes echoed into free-text
// values (e.g. a CLI command's stdout), which key-based redaction cannot catch.
func TestRedactValue_ScrubValueContent(t *testing.T) {
	in := map[string]any{
		"commands": []any{
			map[string]any{
				"command": "aws iam create-access-key",
				"output":  "created key AKIAIOSFODNN7EXAMPLE for user",
			},
		},
		"pem": "prefix -----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY----- suffix",
		// AWS IAM JSON output is redacted via JSON-in-string recursion.
		"iam": `{"AccessKey":{"AccessKeyId":"AKIAIOSFODNN7EXAMPLE","SecretAccessKey":"wJalrXUtn"}}`,
	}
	got := redactValue(in).(map[string]any)

	out := got["commands"].([]any)[0].(map[string]any)["output"].(string)
	assert.NotContains(t, out, "AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, out, redactedPlaceholder)

	assert.NotContains(t, got["pem"].(string), "BEGIN RSA PRIVATE KEY")

	iam := got["iam"].(string)
	assert.NotContains(t, iam, "wJalrXUtn")
	assert.Contains(t, iam, redactedPlaceholder)
}

func TestRedactValue_DoesNotMutateOriginal(t *testing.T) {
	in := map[string]any{"access_secret": "keep-original"}
	_ = redactValue(in)
	assert.Equal(t, "keep-original", in["access_secret"], "redactValue must not mutate its input")
}

func TestRedactValue_NilAndScalars(t *testing.T) {
	assert.Nil(t, redactValue(nil))
	assert.Equal(t, "just a string", redactValue("just a string"))
	// UseNumber keeps integers exact (no float64 precision loss).
	assert.Equal(t, json.Number("42"), redactValue(42))
	assert.Equal(t, json.Number("9007199254740993"), redactValue(int64(9007199254740993)))
}

func TestRedactValue_Idempotent(t *testing.T) {
	in := map[string]any{"access_secret": "s", "name": "n", "data": `{"token":"t"}`}
	once := redactValue(in)
	twice := redactValue(once)
	assert.Equal(t, once, twice)
}

func TestRedactAudit(t *testing.T) {
	a := Audit{
		EventCategory:  EventCategoryAccount,
		EventType:      EventTypeAccountCreate,
		EventTarget:    "account",
		EventState:     map[string]any{"id": "1", "access_secret": "s"},
		EventPrevState: map[string]any{"access_key": "ak"},
		EventAttr:      map[string]any{"table": "cloud_accounts", "token": "t"},
	}
	got := redactAudit(a)

	state := got.EventState.(map[string]any)
	assert.Equal(t, "1", state["id"])
	assert.Equal(t, redactedPlaceholder, state["access_secret"])

	prev := got.EventPrevState.(map[string]any)
	assert.Equal(t, redactedPlaceholder, prev["access_key"])

	assert.Equal(t, "cloud_accounts", got.EventAttr["table"])
	assert.Equal(t, redactedPlaceholder, got.EventAttr["token"])

	assert.Equal(t, EventTypeAccountCreate, got.EventType)
	assert.Equal(t, "account", got.EventTarget)

	assert.Equal(t, "s", a.EventState.(map[string]any)["access_secret"])
}

func TestRedactAudit_NilAttr(t *testing.T) {
	a := Audit{EventState: map[string]any{"name": "n"}}
	got := redactAudit(a)
	assert.Nil(t, got.EventAttr)
}

// Guards that the round-trip does not corrupt a large-ish payload's structure.
func TestRedactValue_PreservesJSONShape(t *testing.T) {
	in := map[string]any{"nums": []any{1, 2, 3}, "flag": true, "nil": nil}
	got := redactValue(in).(map[string]any)
	b, err := json.Marshal(got)
	assert.NoError(t, err)
	assert.True(t, strings.Contains(string(b), `"nums":[1,2,3]`))
	assert.Equal(t, true, got["flag"])
}
