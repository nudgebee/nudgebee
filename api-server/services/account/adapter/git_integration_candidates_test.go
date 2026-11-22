package adapter

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const candidatesTestTenant = "00000000-0000-0000-0000-0000000000a1"

func tokenIntegration(name, token string) gitIntegrationConfig {
	return gitIntegrationConfig{Name: name, Values: map[string]string{"password": token, "auth_type": "token"}}
}

// fakeGitHubPR serves GET /repos/acme/infra/pulls/42 and answers per bearer
// token, recording which tokens asked.
func fakeGitHubPR(t *testing.T, answers map[string]int) *[]string {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	withGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		asked = append(asked, token)
		mu.Unlock()
		code, ok := answers[token]
		if !ok {
			code = http.StatusNotFound
		}
		w.WriteHeader(code)
		if code == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "open"})
			return
		}
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	})
	return &asked
}

func candidatesTestCtx() *testAccountAdapterContext {
	return &testAccountAdapterContext{securityContext: security.NewSecurityContextForAuditRelay(candidatesTestTenant, "test-user")}
}

// The pull request was raised with another integration than the one picked, so
// the picked one gets a 404. The next one that can see it answers.
func TestGitHubPRStatusAsksTheNextIntegrationWhenOneCannotSeeThePR(t *testing.T) {
	asked := fakeGitHubPR(t, map[string]int{"tok-picked": http.StatusNotFound, "tok-other": http.StatusOK})

	got := githubPRStatusFromIntegrations(candidatesTestCtx(),
		[]gitIntegrationConfig{tokenIntegration("Picked", "tok-picked"), tokenIntegration("Other", "tok-other")},
		"acme", "infra", "42")

	assert.False(t, got.Unverified)
	assert.Equal(t, RecommendationResolutionStatusInProgress, got.Status)
	assert.Equal(t, "PR is open and awaiting review/merge", got.StatusMessage)
	assert.Equal(t, []string{"tok-picked", "tok-other"}, *asked)
}

func TestGitHubPRStatusReportsThePickedIntegrationsAnswerWhenNoneCanSeeThePR(t *testing.T) {
	asked := fakeGitHubPR(t, map[string]int{"tok-picked": http.StatusNotFound, "tok-other": http.StatusForbidden})

	got := githubPRStatusFromIntegrations(candidatesTestCtx(),
		[]gitIntegrationConfig{tokenIntegration("Picked", "tok-picked"), tokenIntegration("Other", "tok-other")},
		"acme", "infra", "42")

	assert.True(t, got.Unverified)
	assert.Contains(t, got.StatusMessage, "GitHub returned 404")
	assert.Equal(t, []string{"tok-picked", "tok-other"}, *asked)
}

// A 5xx says nothing about access; asking another integration would only hide it.
func TestGitHubPRStatusStopsAtAnAnswerThatIsNotAboutAccess(t *testing.T) {
	asked := fakeGitHubPR(t, map[string]int{"tok-picked": http.StatusBadGateway, "tok-other": http.StatusOK})

	got := githubPRStatusFromIntegrations(candidatesTestCtx(),
		[]gitIntegrationConfig{tokenIntegration("Picked", "tok-picked"), tokenIntegration("Other", "tok-other")},
		"acme", "infra", "42")

	assert.True(t, got.Unverified)
	assert.Contains(t, got.StatusMessage, "GitHub returned 502")
	assert.Equal(t, []string{"tok-picked"}, *asked)
}

// An App integration whose installation token cannot be minted cannot see the
// pull request either; the next integration is asked.
func TestGitHubPRStatusAsksTheNextIntegrationWhenAnAppTokenCannotBeMinted(t *testing.T) {
	asked := fakeGitHubPR(t, map[string]int{"tok-other": http.StatusOK})
	app := gitIntegrationConfig{Name: "App", Values: map[string]string{"password": "12345", "auth_type": "application"}}

	got := githubPRStatusFromIntegrations(candidatesTestCtx(),
		[]gitIntegrationConfig{app, tokenIntegration("Other", "tok-other")},
		"acme", "infra", "42")

	assert.False(t, got.Unverified)
	assert.Equal(t, RecommendationResolutionStatusInProgress, got.Status)
	assert.Equal(t, []string{"tok-other"}, *asked)
}

func TestErrCredentialCannotSee(t *testing.T) {
	assert.True(t, errCredentialCannotSee(&providerStatusError{endpoint: "x", status: 404}))
	assert.True(t, errCredentialCannotSee(&providerStatusError{endpoint: "x", status: 401}))
	assert.True(t, errCredentialCannotSee(fmt.Errorf("wrapped: %w", &providerStatusError{endpoint: "x", status: 403})))
	assert.True(t, errCredentialCannotSee(fmt.Errorf("%w: boom", errAppTokenFailed)))
	assert.False(t, errCredentialCannotSee(&providerStatusError{endpoint: "x", status: 500}))
	assert.False(t, errCredentialCannotSee(fmt.Errorf("GitHub did not return the diff of values.yaml")))
	assert.Equal(t, "https://api.github.com/x returned 404", (&providerStatusError{endpoint: "https://api.github.com/x", status: 404}).Error())
}

// candidatesTestManager opens a throwaway schema holding the two integration
// tables with the columns the loader reads.
func candidatesTestManager(t *testing.T) *database.DatabaseManager {
	t.Helper()
	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("TEST_POSTGRES_DSN")
	}
	if dsn == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but neither APP_DATABASE_URL nor TEST_POSTGRES_DSN is: " +
				"this suite must not be skipped in the job that exists to run it")
		}
		t.Skip("no database configured; export APP_DATABASE_URL to run this suite")
	}
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	// One connection, so SET search_path below holds for every later statement.
	db.SetMaxOpenConns(1)

	schema := fmt.Sprintf("git_candidates_test_%d", os.Getpid())
	_, err = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s; SET search_path TO %s`, schema, schema, schema))
	require.NoError(t, err)
	_, err = db.Exec(`
		CREATE TABLE integrations (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id uuid NOT NULL,
			name text NOT NULL,
			type text NOT NULL,
			status text NOT NULL
		);
		CREATE TABLE integration_config_values (
			integration_id uuid NOT NULL REFERENCES integrations(id),
			name text NOT NULL,
			value text NOT NULL,
			is_encrypted boolean NOT NULL DEFAULT false
		)`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})
	return &database.DatabaseManager{Db: db}
}

func seedIntegration(t *testing.T, dbms *database.DatabaseManager, tenantID, name, typ, status string, values map[string]string, encrypted bool) {
	t.Helper()
	var id string
	require.NoError(t, dbms.Db.QueryRow(`INSERT INTO integrations (tenant_id, name, type, status) VALUES ($1, $2, $3, $4) RETURNING id`,
		tenantID, name, typ, status).Scan(&id))
	for k, v := range values {
		_, err := dbms.Db.Exec(`INSERT INTO integration_config_values (integration_id, name, value, is_encrypted) VALUES ($1, $2, $3, $4)`,
			id, k, v, encrypted && k == "password")
		require.NoError(t, err)
	}
}

func seedCandidateIntegrations(t *testing.T, dbms *database.DatabaseManager) {
	t.Helper()
	seedIntegration(t, dbms, candidatesTestTenant, "Alpha", "github", "enabled", map[string]string{"password": "tok-alpha", "auth_type": "token"}, false)
	seedIntegration(t, dbms, candidatesTestTenant, "Picked", "github", "enabled", map[string]string{"password": "tok-picked", "auth_type": "token"}, false)
	seedIntegration(t, dbms, candidatesTestTenant, "Beta", "github", "enabled", map[string]string{"password": "tok-beta", "auth_type": "token"}, false)
	seedIntegration(t, dbms, candidatesTestTenant, "Disabled", "github", "disabled", map[string]string{"password": "tok-disabled"}, false)
	seedIntegration(t, dbms, candidatesTestTenant, "NoCredential", "github", "enabled", map[string]string{"auth_type": "token"}, false)
	seedIntegration(t, dbms, candidatesTestTenant, "Undecryptable", "github", "enabled", map[string]string{"password": "not-hex"}, true)
	seedIntegration(t, dbms, candidatesTestTenant, "Lab", "gitlab", "enabled", map[string]string{"password": "tok-lab"}, false)
	seedIntegration(t, dbms, "00000000-0000-0000-0000-0000000000b2", "Elsewhere", "github", "enabled", map[string]string{"password": "tok-elsewhere"}, false)
}

func TestLoadGitIntegrationConfigsPutsThePickedIntegrationFirst_DB(t *testing.T) {
	dbms := candidatesTestManager(t)
	seedCandidateIntegrations(t, dbms)

	got, err := loadGitIntegrationConfigs(slog.Default(), dbms, candidatesTestTenant, "github", "Picked")
	require.NoError(t, err)
	var names []string
	for _, c := range got {
		names = append(names, c.Name)
	}
	assert.Equal(t, []string{"Picked", "Alpha", "Beta"}, names)
	assert.Equal(t, "tok-picked", got[0].Values["password"])
	assert.Equal(t, "token", got[0].Values["auth_type"])

	// No pick, or a pick that no longer exists: every usable one, by name.
	for _, pick := range []string{"", "Deleted"} {
		got, err = loadGitIntegrationConfigs(slog.Default(), dbms, candidatesTestTenant, "github", pick)
		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, "Alpha", got[0].Name)
	}

	_, err = loadGitIntegrationConfigs(slog.Default(), dbms, "00000000-0000-0000-0000-0000000000c3", "github", "")
	require.Error(t, err)
}

func TestResolvePRStateAsksTheNextIntegration_DB(t *testing.T) {
	dbms := candidatesTestManager(t)
	seedCandidateIntegrations(t, dbms)
	asked := fakeGitHubPR(t, map[string]int{"tok-beta": http.StatusOK})

	meta := githubTestMeta()
	meta.ProviderConfig.Name = "Picked"
	_, known := resolvePRState(security.NewRequestContextForSuperAdmin(nil, nil, nil), dbms, candidatesTestTenant, meta)

	assert.True(t, known)
	assert.Equal(t, []string{"tok-picked", "tok-alpha", "tok-beta"}, *asked)
}

func TestFetchPRAddedLinesAsksTheNextIntegration_DB(t *testing.T) {
	dbms := candidatesTestManager(t)
	seedCandidateIntegrations(t, dbms)
	var asked []string
	withGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		asked = append(asked, token)
		if token != "tok-alpha" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode([]githubPRFile{{Filename: "deploy/values.yaml", Changes: 1, Patch: "+    cpu: 265m"}})
	})

	meta := githubTestMeta()
	meta.ProviderConfig.Name = "Picked"
	added, err := fetchPRAddedLines(security.NewRequestContextForSuperAdmin(nil, nil, nil), dbms, candidatesTestTenant, meta)

	require.NoError(t, err)
	assert.Equal(t, []string{"    cpu: 265m"}, added)
	assert.Equal(t, []string{"tok-picked", "tok-alpha"}, asked)
}

// The resolution row's data carries the picked integration under provider_config.
func TestPRMetadataReadsThePickedIntegration(t *testing.T) {
	var meta prMetadata
	require.NoError(t, json.Unmarshal([]byte(`{"pr_url":"https://github.com/acme/infra/pull/42","provider_config":{"name":"Picked","resize_policy":"in-place"}}`), &meta))
	assert.Equal(t, "Picked", meta.ProviderConfig.Name)
}
