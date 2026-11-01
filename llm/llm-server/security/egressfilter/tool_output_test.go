package egressfilter

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
)

// All secrets below are synthetic.

var placeholderPattern = regexp.MustCompile(`\[REDACTED:[a-z0-9-]+\]`)

const plainMarker = "[REDACTED]"

// redactPlain redacts with no tenant and folds every placeholder to
// [REDACTED], so cases read the same whatever rule fired.
func redactPlain(in string) string {
	return placeholderPattern.ReplaceAllString(redactToolOutput(context.Background(), "test_tool", in), plainMarker)
}

func TestRedactToolOutput(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"db url in env", "APP_DATABASE_URL=postgresql://postgres:s3cr3t-Value@10.0.0.5:5432/appdb",
			"APP_DATABASE_URL=postgresql://postgres:[REDACTED]@10.0.0.5:5432/appdb"},
		{"db url in a log line", `INFO dbms: opening connection url="postgresql://app:p%40ss%24word@db.example.com:5432/x?connect_timeout=10"`,
			`INFO dbms: opening connection url="postgresql://app:[REDACTED]@db.example.com:5432/x?connect_timeout=10"`},
		{"masked url kept", "url=postgresql://postgres:xxxxx@10.0.0.5:5432/appdb", "url=postgresql://postgres:xxxxx@10.0.0.5:5432/appdb"},
		{"env secret", "GOOGLE_CLIENT_SECRET=abcdEFGH1234ijkl\nBASE_URL=https://app.example.com",
			"GOOGLE_CLIENT_SECRET=[REDACTED]\nBASE_URL=https://app.example.com"},
		{"env token and password", "SLACK_BOT_TOKEN=fake-token-value-1\nREDIS_USER_PASSWORD=fakepass99",
			"SLACK_BOT_TOKEN=[REDACTED]\nREDIS_USER_PASSWORD=[REDACTED]"},
		{"json secret", `{"client_secret": "abcd1234efgh", "client_id": "app-123"}`,
			`{"client_secret": "[REDACTED]", "client_id": "app-123"}`},
		{"yaml secret", "password: hunter2hunter2\nusername: admin", "password: [REDACTED]\nusername: admin"},
		{"k8s env entry", "- name: DB_PASSWORD\n  value: plain-text-pass\n- name: DB_HOST\n  value: db.local",
			"- name: DB_PASSWORD\n  value: [REDACTED]\n- name: DB_HOST\n  value: db.local"},
		{"secret reference kept", "- name: DB_PASSWORD\n  valueFrom:\n    secretKeyRef:\n      name: app-secrets",
			"- name: DB_PASSWORD\n  valueFrom:\n    secretKeyRef:\n      name: app-secrets"},
		{"names that only mention secret kept", "secretName: app-tls\nLLM_SERVER_TOKEN_HEADER=Authorization\ntokenUrl: https://auth.example.com/token",
			"secretName: app-tls\nLLM_SERVER_TOKEN_HEADER=Authorization\ntokenUrl: https://auth.example.com/token"},
		{"references and placeholders kept", "PGPASSWORD=$DB_PASS\nAPI_KEY=${API_KEY}\npassword: <your-password>\ntoken: true",
			"PGPASSWORD=$DB_PASS\nAPI_KEY=${API_KEY}\npassword: <your-password>\ntoken: true"},
		{"aws key", "found AKIAABCDEFGHIJKLMNOP in config", "found [REDACTED] in config"},
		{"google oauth secret", "x GOCSPX-Abcdefghijklmnopqrstuvw y", "x [REDACTED] y"},
		{"bearer header", "Authorization: Bearer abc.def.ghi-123456", "Authorization: Bearer [REDACTED]"},
		{"upper-case bearer header", "AUTHORIZATION: Bearer abc.def.ghi-123456", "AUTHORIZATION: Bearer [REDACTED]"},
		{"private key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEfake\n-----END RSA PRIVATE KEY-----", "[REDACTED]"},
		{"plain text untouched", "pod/api-0 Running 3 restarts; see https://example.com/docs", "pod/api-0 Running 3 restarts; see https://example.com/docs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, redactPlain(tc.in))
		})
	}
}

// The placeholder names the rule, so disabled_rules and support triage can
// use it.
func TestRedactToolOutputPlaceholderNamesRule(t *testing.T) {
	cases := map[string]string{
		"APP_DATABASE_URL=postgresql://u:s3cr3t-Value@h/db":               "APP_DATABASE_URL=postgresql://u:[REDACTED:url-password]@h/db",
		"DB_PASSWORD=fakepass99":                                          "DB_PASSWORD=[REDACTED:secret-assignment]",
		"- name: DB_PASSWORD\n  value: plain-text-pass":                   "- name: DB_PASSWORD\n  value: [REDACTED:k8s-env-secret]",
		"key AKIAABCDEFGHIJKLMNOP":                                        "key [REDACTED:aws-access-key-id]",
		"Authorization: Bearer abc.def.ghi-123456":                        "Authorization: Bearer [REDACTED:authorization-header]",
		"x GOCSPX-Abcdefghijklmnopqrstuvw":                                "x [REDACTED:google-oauth-client-secret]",
		"-----BEGIN PRIVATE KEY-----\nMIIfake\n-----END PRIVATE KEY-----": "[REDACTED:private-key-block]",
	}
	for in, want := range cases {
		assert.Equal(t, want, redactToolOutput(context.Background(), "t", in), in)
	}
}

func TestRedactToolOutputIsIdempotent(t *testing.T) {
	in := "APP_DATABASE_URL=postgresql://postgres:s3cr3t-Value@10.0.0.5/db\nGOOGLE_CLIENT_SECRET=abcdEFGH1234\n- name: DB_PASSWORD\n  value: plain-text-pass"
	once := redactToolOutput(context.Background(), "t", in)
	assert.Equal(t, once, redactToolOutput(context.Background(), "t", once))
	assert.False(t, strings.Contains(once, "s3cr3t") || strings.Contains(once, "abcdEFGH1234") || strings.Contains(once, "plain-text-pass"))
}

// The redacted output is fed to the outbound scan on the next LLM call; the
// placeholders must not be reported again as secrets.
func TestRedactToolOutputPlaceholdersDoNotTripOutboundScan(t *testing.T) {
	in := "APP_DATABASE_URL=postgresql://u:s3cr3t-Value@h/db\nDB_PASSWORD=fakepass99\nAuthorization: Bearer abc.def.ghi-123456\nkey AKIAABCDEFGHIJKLMNOP"
	out := redactToolOutput(context.Background(), "t", in)
	assert.Empty(t, Scan(out).Hits, out)
}

// Redaction runs on JSON tool output (tables, event evidence), which callers
// still parse afterwards.
func TestRedactToolOutputKeepsJSONValid(t *testing.T) {
	in := `[{"log":"PASSWORD=ab\"cd and APP_DATABASE_URL=postgresql://u:p4ss\"w0rd@h/db","env":{"API_KEY":"k3y-value-1","name":"x"},"msg":"Authorization: Bearer abc.def.ghi-123456"}]`
	out := redactToolOutput(context.Background(), "t", in)
	var parsed []map[string]any
	assert.NoError(t, json.Unmarshal([]byte(out), &parsed), out)
	assert.NotContains(t, out, "k3y-value-1")
	assert.NotContains(t, out, "abc.def.ghi-123456")
}

// Shapes that look like secret settings but are not, taken from real tool
// output (values are synthetic).
func TestRedactToolOutputKeepsNonSecrets(t *testing.T) {
	for _, in := range []string{
		"failed to decrypt pagerduty token: cipher: message authentication failed",
		"get identity: get credentials: failed to refresh cached credentials",
		"INFO license: valid until 2027",
		"api_secret = os.environ['API_SECRET']",
		"GET https://proxy.golang.org:443/github.com/acme/lib/@v/list",
		"PWD=/home/appuser/workspace\nOLDPWD=/tmp",
		"client = Client(api_key=api_key, token=self.token)",
		"\t\tClientSecret:   clientSecret,\n\t\tAccessSecret:  account.Config.AccessKey,",
		"password=os.getenv(\"DB_PASSWORD\")",
		"api_key=secrets[name]",
		"GOOGLE_APPLICATION_CREDENTIALS: /var/run/secrets/gcp/key.json",
		"token: GITHUB_TOKEN_KEY",
	} {
		assert.Equal(t, in, redactToolOutput(context.Background(), "t", in), in)
	}
}

// Large single-line JSON (logs with escaped newlines) is redacted in bounded
// segments, with the same results.
func TestRedactToolOutputEscapedNewlinesAndLongLines(t *testing.T) {
	in := `{"logs":"line one\nAPP_DATABASE_URL=postgresql://u:s3cr3t-Value@h/db\n- name: DB_PASSWORD\n  value: plain-text-pass\n-----BEGIN PRIVATE KEY-----\nMIIfake\n-----END PRIVATE KEY-----"}`
	out := redactPlain(in)
	assert.NotContains(t, out, "s3cr3t-Value")
	assert.NotContains(t, out, "plain-text-pass")
	assert.NotContains(t, out, "MIIfake")

	long := strings.Repeat("ok, ", 20000) + "GOOGLE_CLIENT_SECRET=abcdEFGH1234 " + strings.Repeat("ok, ", 20000)
	assert.Equal(t, strings.Replace(long, "abcdEFGH1234", plainMarker, 1), redactPlain(long))

	// Quotes escaped inside a JSON string are kept and the JSON stays valid.
	for _, in := range []string{
		`{"log":"DB_PASSWORD=\"hunter2hunter2\" started"}`,
		`{"log":"db:\n  password: \"hunter2hunter2\"\n"}`,
		`{"log":"- name: DB_PASSWORD\n  value: \"hunter2hunter2\""}`,
	} {
		out := redactPlain(in)
		assert.Equal(t, strings.Replace(in, "hunter2hunter2", plainMarker, 1), out)
		assert.True(t, json.Valid([]byte(out)), out)
	}

	// A value longer than the scan window is redacted whole, not cut.
	longValue := strings.Repeat("Ab3", 1500)
	for _, in := range []string{"SIGNING_KEY=" + longValue + " next", `{"signing_key":"` + longValue + `"}`} {
		assert.Equal(t, strings.Replace(in, longValue, plainMarker, 1), redactPlain(in))
	}
}

// Runes whose lower case is longer must not shift offsets (this panicked when
// the windows were found in strings.ToLower output).
func TestRedactToolOutputNonASCIIBeforeSecret(t *testing.T) {
	for _, prefix := range []string{strings.Repeat("\u023a", 300), strings.Repeat("\u212a", 3000)} {
		in := prefix + " password=Fakepass99xyz"
		assert.Equal(t, prefix+" password=[REDACTED:secret-assignment]", redactToolOutput(context.Background(), "t", in))
	}
}

// The egress filter's settings govern tool-output redaction.
func TestRedactToolOutputFollowsSettings(t *testing.T) {
	const in = "DB_PASSWORD=fakepass99 url=postgresql://u:s3cr3t-Value@h/db"
	tenant := uuid.New()
	ctx := WithTenantID(context.Background(), tenant)
	withTenant := func(cfg *TenantConfig) {
		installFakeLoaderForTest(t, func(context.Context, uuid.UUID) (*TenantConfig, error) { return cfg, nil })
	}

	withTenant(&TenantConfig{TenantID: tenant, Enabled: false})
	assert.Equal(t, in, redactToolOutput(ctx, "t", in), "tenant enabled=false")

	withTenant(&TenantConfig{TenantID: tenant, Enabled: true, DisabledAgents: []string{"k8s_orchestrator"}})
	assert.Equal(t, in, redactToolOutput(WithAgentName(ctx, "k8s_orchestrator"), "t", in), "agent excluded")
	assert.NotEqual(t, in, redactToolOutput(WithAgentName(ctx, "logs"), "t", in), "other agent")

	withTenant(&TenantConfig{TenantID: tenant, Enabled: true, DisabledRules: []string{RuleIDURLPassword}})
	assert.Equal(t, "DB_PASSWORD=[REDACTED:secret-assignment] url=postgresql://u:s3cr3t-Value@h/db", redactToolOutput(ctx, "t", in), "disabled rule")

	withTenant(&TenantConfig{TenantID: tenant, Enabled: true, Allowlist: []string{"fakepass99"}})
	assert.Equal(t, "DB_PASSWORD=fakepass99 url=postgresql://u:[REDACTED:url-password]@h/db", redactToolOutput(ctx, "t", in), "allowlisted value")

	// The mode governs the outbound request only: detect still redacts.
	withTenant(&TenantConfig{TenantID: tenant, Enabled: true, Mode: ModeDetect})
	assert.NotContains(t, redactToolOutput(ctx, "t", in), "fakepass99")
}

func TestRedactToolOutputSwitches(t *testing.T) {
	const in = "DB_PASSWORD=fakepass99"
	prev := config.Config
	t.Cleanup(func() { config.Config = prev })
	set := func(master, secrets, toolOutput bool) {
		config.Config.LlmServerEgressFilterEnabled = master
		config.Config.LlmServerEgressFilterSecretsEnabled = secrets
		config.Config.LlmServerEgressFilterToolOutputRedactEnabled = toolOutput
	}

	set(true, true, true)
	assert.Equal(t, "DB_PASSWORD=[REDACTED:secret-assignment]", RedactToolOutput(nil, "", "t", in))
	for _, off := range [][3]bool{{false, true, true}, {true, false, true}, {true, true, false}} {
		set(off[0], off[1], off[2])
		assert.Equal(t, in, RedactToolOutput(nil, "", "t", in), off)
	}
}

// Each redaction is reported like an outbound hit, with the tool as source.
func TestRedactToolOutputReportsEvent(t *testing.T) {
	var events []FilterEvent
	ctx := WithFilterEventReporter(WithAgentName(context.Background(), "logs"), func(e FilterEvent) { events = append(events, e) })
	redactToolOutput(ctx, "kubectl_execute", "DB_PASSWORD=fakepass99\nurl=postgresql://u:s3cr3t-Value@h/db")
	require.Len(t, events, 1)
	e := events[0]
	assert.Equal(t, ModeRedact, e.Mode)
	assert.Equal(t, "logs", e.AgentName)
	assert.ElementsMatch(t, []string{RuleIDSecretAssignment, RuleIDURLPassword}, e.RuleIDs)
	assert.Equal(t, []Source{SourceToolResult}, e.HitSources)
	require.Len(t, e.Redactions, 2)
	assert.Equal(t, "[REDACTED:secret-assignment]", e.Redactions[0].Placeholder)
	for _, h := range e.Hits {
		assert.Equal(t, "kubectl_execute", h.Tool)
		assert.Empty(t, h.Value, "raw values are never reported")
	}
}

// A large log-like output: realistic lines with an occasional secret.
func BenchmarkRedactToolOutput1MB(b *testing.B) {
	line := `2026-10-02T06:07:35Z INFO handler: request served path=/api/v1/items status=200 duration_ms=12 user=svc-account region=us-east-1` + "\n"
	secret := `APP_DATABASE_URL=postgresql://app:s3cr3t-Value@10.0.0.5:5432/appdb` + "\n"
	var sb strings.Builder
	for sb.Len() < 1<<20 {
		sb.WriteString(line)
		if sb.Len()%50000 < len(line) {
			sb.WriteString(secret)
		}
	}
	input := sb.String()
	b.SetBytes(int64(len(input)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		redactToolOutput(context.Background(), "t", input)
	}
}
