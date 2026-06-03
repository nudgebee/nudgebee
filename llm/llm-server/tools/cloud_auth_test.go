package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string {
	return &s
}

// =============================================================================
// BuildAwsAuth tests
// =============================================================================

func TestBuildAwsAuth_StaticCredentials(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-account-1",
		AccessKey:     strPtr("AKIAIOSFODNN7EXAMPLE"),
		AccessSecret:  strPtr("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
		Region:        strPtr("us-east-1"),
		AccountNumber: "123456789012",
		CloudProvider: "aws",
	}

	auth, err := BuildAwsAuth(t.Context(), creds)
	require.NoError(t, err)
	require.NotNil(t, auth)

	assert.Equal(t, "us-east-1", auth.Env["AWS_DEFAULT_REGION"])
	assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", auth.Env["AWS_ACCESS_KEY_ID"])
	assert.Equal(t, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", auth.Env["AWS_SECRET_ACCESS_KEY"])
	assert.Empty(t, auth.Env["AWS_SESSION_TOKEN"], "no session token without role assumption")
	assert.Empty(t, auth.CommandPrefix, "AWS auth uses only env vars, no command prefix")
	assert.Empty(t, auth.CommandSuffix, "AWS auth uses only env vars, no cleanup needed")
}

func TestBuildAwsAuth_NoRegion(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-account-2",
		AccessKey:     strPtr("AKIAIOSFODNN7EXAMPLE"),
		AccessSecret:  strPtr("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
		AccountNumber: "123456789012",
		CloudProvider: "aws",
	}

	auth, err := BuildAwsAuth(t.Context(), creds)
	require.NoError(t, err)
	require.NotNil(t, auth)

	assert.Empty(t, auth.Env["AWS_DEFAULT_REGION"])
	assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", auth.Env["AWS_ACCESS_KEY_ID"])
}

func TestBuildAwsAuth_NoCreds_FallsBackToEnvironment(t *testing.T) {
	// When no access key or secret is provided, AWS SDK falls back to
	// environment/pod identity. BuildAwsAuth should still succeed.
	creds := CloudAccountCredentials{
		ID:            "test-account-3",
		AccountNumber: "123456789012",
		CloudProvider: "aws",
	}

	auth, err := BuildAwsAuth(t.Context(), creds)
	require.NoError(t, err)
	require.NotNil(t, auth)

	// No static creds injected — env will rely on workspace's own identity
	assert.Empty(t, auth.Env["AWS_ACCESS_KEY_ID"])
	assert.Empty(t, auth.Env["AWS_SECRET_ACCESS_KEY"])
}

// =============================================================================
// BuildGcpAuth tests
// =============================================================================

func TestBuildGcpAuth_Success(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-gcp-1",
		AccessSecret:  strPtr(`{"type":"service_account","project_id":"my-project"}`),
		AccountNumber: "my-project-id",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)
	require.NotNil(t, auth)

	assert.Equal(t, `{"type":"service_account","project_id":"my-project"}`, auth.Env["GCP_SA_KEY"])
	assert.Equal(t, "my-project-id", auth.Env["GCP_PROJECT_ID"])
	assert.Equal(t, "my-project-id", auth.Env["CLOUDSDK_CORE_PROJECT"])
	assert.Equal(t, "1", auth.Env["CLOUDSDK_CORE_DISABLE_PROMPTS"])
	assert.Equal(t, "xterm", auth.Env["TERM"])
	assert.NotEmpty(t, auth.Env["GOOGLE_APPLICATION_CREDENTIALS"])
	assert.Contains(t, auth.CommandPrefix, "gcloud auth activate-service-account")
	assert.Empty(t, auth.CommandSuffix, "GCP key file is reused across commands, no cleanup needed")
}

func TestBuildGcpAuth_KeyFileIsStablePerAccount(t *testing.T) {
	// Key file path should be stable for the same account ID — enables caching
	// across commands within the same workspace session.
	creds := CloudAccountCredentials{
		ID:            "test-gcp-stable",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth1, err := BuildGcpAuth(creds)
	require.NoError(t, err)
	auth2, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	keyFile1 := auth1.Env["GOOGLE_APPLICATION_CREDENTIALS"]
	keyFile2 := auth2.Env["GOOGLE_APPLICATION_CREDENTIALS"]
	assert.Equal(t, keyFile1, keyFile2, "GCP key file path must be stable for caching")
	assert.Equal(t, "/tmp/gcp_config_test-gcp-stable/sa_key.json", keyFile1)
}

func TestBuildGcpAuth_AuthIsCached(t *testing.T) {
	// GCP auth prefix should only write key + authenticate if marker doesn't exist
	creds := CloudAccountCredentials{
		ID:            "test-gcp-cache",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	assert.Contains(t, auth.CommandPrefix, "if [ ! -f", "GCP auth must check for cached sentinel")
	assert.Contains(t, auth.CommandPrefix, ".auth_complete", "GCP auth must use .auth_complete sentinel, not key file existence")
	assert.Contains(t, auth.CommandPrefix, "touch", "GCP auth must create sentinel after successful activation")
	assert.Contains(t, auth.CommandPrefix, "fi", "GCP auth cache check must have closing fi")
}

func TestBuildGcpAuth_AuthMarkerCreatedAfterSuccess(t *testing.T) {
	// The .auth_ok marker must be created AFTER successful auth, not before.
	// This prevents the stale key file bug where auth is skipped after a failure.
	creds := CloudAccountCredentials{
		ID:            "test-gcp-marker",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	// Marker file is checked (not the key file)
	assert.Contains(t, auth.CommandPrefix, ".auth_ok", "must check .auth_ok marker, not key file")
	// Marker is created after auth succeeds (touch follows activate-service-account)
	authIdx := strings.Index(auth.CommandPrefix, "activate-service-account")
	touchIdx := strings.Index(auth.CommandPrefix, "touch")
	assert.Greater(t, touchIdx, authIdx, ".auth_ok touch must come after gcloud auth activate-service-account")
	// On failure, marker is cleaned up
	assert.Contains(t, auth.CommandPrefix, "rm -f", "marker and key must be removed on auth failure")
}

func TestBuildGcpAuth_PerAccountCloudSdkConfig(t *testing.T) {
	// Each GCP account must get its own CLOUDSDK_CONFIG directory to prevent
	// cross-account auth interference.
	creds1 := CloudAccountCredentials{
		ID: "gcp-account-a", AccessSecret: strPtr(`{"type":"sa"}`),
		AccountNumber: "proj-a", CloudProvider: "gcp",
	}
	creds2 := CloudAccountCredentials{
		ID: "gcp-account-b", AccessSecret: strPtr(`{"type":"sa"}`),
		AccountNumber: "proj-b", CloudProvider: "gcp",
	}

	auth1, err := BuildGcpAuth(creds1)
	require.NoError(t, err)
	auth2, err := BuildGcpAuth(creds2)
	require.NoError(t, err)

	// Both must have CLOUDSDK_CONFIG set
	assert.NotEmpty(t, auth1.Env["CLOUDSDK_CONFIG"])
	assert.NotEmpty(t, auth2.Env["CLOUDSDK_CONFIG"])
	// Must be different directories
	assert.NotEqual(t, auth1.Env["CLOUDSDK_CONFIG"], auth2.Env["CLOUDSDK_CONFIG"],
		"different GCP accounts must have different CLOUDSDK_CONFIG dirs")
	// Key file must be inside the config dir
	assert.True(t, strings.HasPrefix(auth1.Env["GOOGLE_APPLICATION_CREDENTIALS"], auth1.Env["CLOUDSDK_CONFIG"]),
		"key file must be inside the per-account config dir")
}

func TestBuildGcpAuth_KeyFileRestrictivePermissions(t *testing.T) {
	// Key file must be written with umask 077 to prevent world-readable credentials in /tmp
	creds := CloudAccountCredentials{
		ID:            "test-gcp-perms",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	assert.Contains(t, auth.CommandPrefix, "umask 077", "GCP key file must be created with restrictive permissions")
}

func TestBuildGcpAuth_MissingSecret(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-gcp-2",
		AccountNumber: "my-project-id",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	assert.Error(t, err)
	assert.Nil(t, auth)
	assert.Contains(t, err.Error(), "service_account_key")
}

func TestBuildGcpAuth_MissingProjectId(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-gcp-3",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	assert.Error(t, err)
	assert.Nil(t, auth)
	assert.Contains(t, err.Error(), "project_id")
}

// =============================================================================
// BuildAzureAuth tests
// =============================================================================

func TestBuildAzureAuth_Success(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-azure-1",
		AccessKey:     strPtr("client-id-123"),
		AccessSecret:  strPtr("client-secret-456"),
		AssumeRole:    strPtr("subscription-id-789"),
		AccountNumber: "azure-tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)
	require.NotNil(t, auth)

	assert.Equal(t, "client-id-123", auth.Env["AZURE_CLIENT_ID"])
	assert.Equal(t, "client-secret-456", auth.Env["AZURE_CLIENT_SECRET"])
	assert.Equal(t, "subscription-id-789", auth.Env["AZURE_SUBSCRIPTION_ID"])
	assert.Equal(t, "azure-tenant-id", auth.Env["AZURE_TENANT_ID"])
	assert.Equal(t, "true", auth.Env["AZURE_CORE_NO_COLOR"])
	assert.True(t, strings.HasPrefix(auth.Env["AZURE_CONFIG_DIR"], "/tmp/azure_auth_"),
		"Azure config dir should use absolute path in /tmp")
	assert.Equal(t, "/opt/azure-cli-extensions", auth.Env["AZURE_EXTENSION_DIR"],
		"AZURE_EXTENSION_DIR must decouple extension storage from per-session AZURE_CONFIG_DIR")
	assert.Contains(t, auth.CommandPrefix, "az login --service-principal")
	assert.Contains(t, auth.CommandPrefix, "az account set --subscription")
	assert.Contains(t, auth.CommandPrefix, "azure: login failed", "Azure login must have descriptive error on failure")
	assert.Contains(t, auth.CommandPrefix, "azure: subscription set failed", "Azure subscription set must have descriptive error on failure")
	assert.Empty(t, auth.CommandSuffix, "Azure persists login state, no cleanup needed")
}

func TestBuildAzureAuth_CachesLogin(t *testing.T) {
	// Azure auth prefix should only login if profile doesn't exist (performance)
	creds := CloudAccountCredentials{
		ID:            "test-azure-cache",
		AccessKey:     strPtr("client-id"),
		AccessSecret:  strPtr("secret"),
		AssumeRole:    strPtr("sub-id"),
		AccountNumber: "tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)

	// Should use flock to serialize the auth block across parallel commands
	assert.Contains(t, auth.CommandPrefix, "flock -x")
	// Should check for sentinel file before re-logging in
	assert.Contains(t, auth.CommandPrefix, ".auth_complete")
	assert.Contains(t, auth.CommandPrefix, "if [ ! -f")

	// Config dir should be absolute, per-account, and per-subscription
	assert.Equal(t, "/tmp/azure_auth_test-azure-cache_sub-id", auth.Env["AZURE_CONFIG_DIR"])
}

func TestBuildAzureAuth_AccountSetInsideGuard(t *testing.T) {
	// az account set MUST be inside the if/fi guard to prevent race conditions
	// when parallel commands share a workspace pod. Previously, az account set
	// was outside the guard, causing concurrent login+set collisions.
	creds := CloudAccountCredentials{
		ID:            "test-azure-guard",
		AccessKey:     strPtr("client-id"),
		AccessSecret:  strPtr("secret"),
		AssumeRole:    strPtr("sub-id"),
		AccountNumber: "tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)

	// az account set must appear BEFORE fi, not after it
	fiIdx := strings.LastIndex(auth.CommandPrefix, "fi")
	setIdx := strings.LastIndex(auth.CommandPrefix, "az account set")
	require.True(t, fiIdx > 0, "command prefix must contain fi")
	require.True(t, setIdx > 0, "command prefix must contain az account set")
	assert.True(t, setIdx < fiIdx,
		"az account set (at %d) must be inside the if/fi guard (fi at %d) to prevent race conditions", setIdx, fiIdx)

	// Sentinel file must be created AFTER both login and account set succeed
	assert.Contains(t, auth.CommandPrefix, "touch")
	assert.Contains(t, auth.CommandPrefix, ".auth_complete")
	touchIdx := strings.LastIndex(auth.CommandPrefix, "touch")
	assert.True(t, touchIdx > setIdx,
		"sentinel file must be created after az account set")
	assert.True(t, touchIdx < fiIdx,
		"sentinel file creation must be inside the if/fi guard")
}

func TestBuildAzureAuth_ConfigDirIncludesSubscription(t *testing.T) {
	// Config dir must include subscription ID to isolate parallel sessions
	creds := CloudAccountCredentials{
		ID:            "test-azure-subdir",
		AccessKey:     strPtr("client-id"),
		AccessSecret:  strPtr("secret"),
		AssumeRole:    strPtr("sub-abc-123"),
		AccountNumber: "tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)

	configDir := auth.Env["AZURE_CONFIG_DIR"]
	assert.Equal(t, "/tmp/azure_auth_test-azure-subdir_sub-abc-123", configDir,
		"config dir must include both account ID and subscription ID")
}

func TestBuildAzureAuth_DifferentSubscriptionsDifferentDirs(t *testing.T) {
	// Two calls with the same account but different subscriptions must produce
	// different config dirs to prevent auth collisions.
	baseCreds := CloudAccountCredentials{
		ID:            "test-azure-parallel",
		AccessKey:     strPtr("client-id"),
		AccessSecret:  strPtr("secret"),
		AccountNumber: "tenant-id",
		CloudProvider: "azure",
	}

	creds1 := baseCreds
	creds1.AssumeRole = strPtr("sub-aaa")
	auth1, err := BuildAzureAuth(creds1)
	require.NoError(t, err)

	creds2 := baseCreds
	creds2.AssumeRole = strPtr("sub-bbb")
	auth2, err := BuildAzureAuth(creds2)
	require.NoError(t, err)

	assert.NotEqual(t, auth1.Env["AZURE_CONFIG_DIR"], auth2.Env["AZURE_CONFIG_DIR"],
		"different subscriptions must produce different config dirs")
}

func TestBuildAzureAuth_MissingClientId(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-azure-2",
		AccessSecret:  strPtr("secret"),
		AssumeRole:    strPtr("sub-id"),
		AccountNumber: "tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	assert.Error(t, err)
	assert.Nil(t, auth)
	assert.Contains(t, err.Error(), "client_id")
}

func TestBuildAzureAuth_MissingClientSecret(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-azure-3",
		AccessKey:     strPtr("client-id"),
		AssumeRole:    strPtr("sub-id"),
		AccountNumber: "tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	assert.Error(t, err)
	assert.Nil(t, auth)
	assert.Contains(t, err.Error(), "client_secret")
}

func TestBuildAzureAuth_MissingSubscriptionId(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-azure-4",
		AccessKey:     strPtr("client-id"),
		AccessSecret:  strPtr("secret"),
		AccountNumber: "tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	assert.Error(t, err)
	assert.Nil(t, auth)
	assert.Contains(t, err.Error(), "subscription_id")
}

func TestBuildAzureAuth_MissingTenantId(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-azure-5",
		AccessKey:     strPtr("client-id"),
		AccessSecret:  strPtr("secret"),
		AssumeRole:    strPtr("sub-id"),
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	assert.Error(t, err)
	assert.Nil(t, auth)
	assert.Contains(t, err.Error(), "tenant_id")
}

// =============================================================================
// WrapCommandWithAuth tests (strict mode — for dedicated cloud tools)
// =============================================================================

func TestWrapCommandWithAuth_NoPrefix_AWS(t *testing.T) {
	// AWS: env vars only, command passes through unchanged
	auth := &CloudAuthResult{
		Env: map[string]string{"AWS_ACCESS_KEY_ID": "test"},
	}
	result := WrapCommandWithAuth("aws s3 ls", auth)
	assert.Equal(t, "aws s3 ls", result)
}

func TestWrapCommandWithAuth_PrefixOnly_Azure(t *testing.T) {
	// Azure: prefix is mandatory (az login), no cleanup
	auth := &CloudAuthResult{
		Env:           map[string]string{},
		CommandPrefix: "az login --service-principal -u $AZURE_CLIENT_ID",
	}
	result := WrapCommandWithAuth("az vm list", auth)
	assert.Equal(t, "az login --service-principal -u $AZURE_CLIENT_ID && az vm list", result)
}

func TestWrapCommandWithAuth_PrefixAndSuffix_GCP(t *testing.T) {
	// GCP: prefix (auth) + suffix (key file cleanup)
	auth := &CloudAuthResult{
		Env:           map[string]string{},
		CommandPrefix: "gcloud auth activate-service-account --key-file=/tmp/key.json",
		CommandSuffix: "rm -f /tmp/key.json",
	}
	result := WrapCommandWithAuth("gcloud compute instances list", auth)
	assert.Contains(t, result, "gcloud auth activate-service-account")
	assert.Contains(t, result, "gcloud compute instances list")
	assert.Contains(t, result, "rm -f /tmp/key.json")
	assert.Contains(t, result, "status=$?")
}

func TestWrapCommandWithAuth_AuthFailureBlocksCommand(t *testing.T) {
	// For strict mode (cloud tools), auth failure MUST block the command.
	// The && ensures the command only runs if auth succeeds.
	auth := &CloudAuthResult{
		CommandPrefix: "false", // simulates auth failure
	}
	result := WrapCommandWithAuth("az vm list", auth)
	assert.Equal(t, "false && az vm list", result)
	// Shell execution of "false && az vm list" will NOT run "az vm list"
}

// =============================================================================
// WrapCommandWithBestEffortAuth tests (non-fatal mode — for shell tool)
// =============================================================================

func TestWrapCommandWithBestEffortAuth_NoPrefix_AWS(t *testing.T) {
	// AWS has no prefix — command passes through unchanged
	auth := &CloudAuthResult{
		Env: map[string]string{"AWS_ACCESS_KEY_ID": "test"},
	}
	result := WrapCommandWithBestEffortAuth("ls -la", auth)
	assert.Equal(t, "ls -la", result)
}

func TestWrapCommandWithBestEffortAuth_PrefixOnly_Azure(t *testing.T) {
	// Azure auth failure should NOT block the shell command
	auth := &CloudAuthResult{
		Env:           map[string]string{},
		CommandPrefix: "az login --service-principal",
	}
	result := WrapCommandWithBestEffortAuth("curl https://example.com", auth)
	assert.Contains(t, result, "|| true", "auth failure must be suppressed with || true")
	assert.Contains(t, result, "curl https://example.com")
	assert.Contains(t, result, "2>/dev/null", "auth stderr should be suppressed")
}

func TestWrapCommandWithBestEffortAuth_PrefixAndSuffix_GCP(t *testing.T) {
	// GCP auth failure should NOT block the shell command, but cleanup must still happen
	auth := &CloudAuthResult{
		Env:           map[string]string{},
		CommandPrefix: "gcloud auth activate-service-account --key-file=/tmp/key.json",
		CommandSuffix: "rm -f /tmp/key.json",
	}
	result := WrapCommandWithBestEffortAuth("cat /tmp/report.txt", auth)
	assert.Contains(t, result, "|| true", "auth failure must be suppressed")
	assert.Contains(t, result, "cat /tmp/report.txt")
	assert.Contains(t, result, "rm -f /tmp/key.json", "cleanup must still run")
}

func TestWrapCommandWithBestEffortAuth_NonCloudCommand_StillRuns(t *testing.T) {
	// Key side-effect test: a simple "ls" on a GCP account should still work
	// even if gcloud auth fails
	auth := &CloudAuthResult{
		Env:           map[string]string{"GCP_SA_KEY": "invalid"},
		CommandPrefix: "false", // simulates auth failure
		CommandSuffix: "rm -f /tmp/key.json",
	}
	result := WrapCommandWithBestEffortAuth("ls -la /workspace", auth)
	// "false" will fail but || true absorbs it, so "ls -la /workspace" still runs
	assert.Contains(t, result, "|| true")
	assert.Contains(t, result, "ls -la /workspace")
}

// =============================================================================
// Shell safety: trailing comment protection
// =============================================================================

func TestWrapCommandWithAuth_TrailingCommentDoesNotBreakCleanup(t *testing.T) {
	// If an LLM-generated command ends with a shell comment (e.g. "ls # list files"),
	// the closing ) and cleanup must not be commented out.
	auth := &CloudAuthResult{
		CommandPrefix: "auth-prefix",
		CommandSuffix: "cleanup-suffix",
	}
	result := WrapCommandWithAuth("gcloud compute instances list # check instances", auth)
	// The newline before ) ensures the comment doesn't eat the wrapper syntax
	assert.Contains(t, result, "# check instances\n)")
	assert.Contains(t, result, "cleanup-suffix")
}

func TestWrapCommandWithBestEffortAuth_TrailingCommentDoesNotBreakCleanup(t *testing.T) {
	auth := &CloudAuthResult{
		CommandPrefix: "auth-prefix",
		CommandSuffix: "cleanup-suffix",
	}
	result := WrapCommandWithBestEffortAuth("ls # just listing", auth)
	assert.Contains(t, result, "# just listing\n;")
	assert.Contains(t, result, "cleanup-suffix")
}

// =============================================================================
// Shell safety: path quoting
// =============================================================================

func TestBuildGcpAuth_KeyFilePathIsQuoted(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-gcp-quoting",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	// Key file path must be quoted in shell commands to prevent word-splitting.
	// Inside the sh -c '...' block, paths are double-quoted.
	keyFile := auth.Env["GOOGLE_APPLICATION_CREDENTIALS"]
	assert.Contains(t, auth.CommandPrefix, "\""+keyFile+"\"", "GCP key file path must be quoted in command prefix")
	assert.Empty(t, auth.CommandSuffix, "GCP key file is reused, no cleanup suffix")
}

func TestBuildGcpAuth_UsesFlock(t *testing.T) {
	// GCP auth prefix must serialize key-write + activate with flock to prevent
	// concurrent gcloud commands racing on the key file.
	creds := CloudAccountCredentials{
		ID:            "test-gcp-flock",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	assert.Contains(t, auth.CommandPrefix, "flock -x", "GCP auth must use flock to serialize concurrent auth")
}

func TestBuildAzureAuth_EnvVarsAreQuotedInPrefix(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "test-azure-quoting",
		AccessKey:     strPtr("client-id"),
		AccessSecret:  strPtr("secret"),
		AssumeRole:    strPtr("sub-id"),
		AccountNumber: "tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)

	// Env var references should be double-quoted in the shell command
	assert.Contains(t, auth.CommandPrefix, "\"$AZURE_CLIENT_ID\"")
	assert.Contains(t, auth.CommandPrefix, "\"$AZURE_CLIENT_SECRET\"")
	assert.Contains(t, auth.CommandPrefix, "\"$AZURE_TENANT_ID\"")
	assert.Contains(t, auth.CommandPrefix, "\"$AZURE_SUBSCRIPTION_ID\"")
}

// =============================================================================
// Side-effect: env variable content tests
// =============================================================================

func TestAwsAuth_NoSecretsInCommandPrefix(t *testing.T) {
	// AWS credentials should ONLY be in env vars, never in command prefix
	creds := CloudAccountCredentials{
		ID:            "secret-test-aws",
		AccessKey:     strPtr("AKIAIOSFODNN7EXAMPLE"),
		AccessSecret:  strPtr("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
		Region:        strPtr("us-east-1"),
		AccountNumber: "123456789012",
		CloudProvider: "aws",
	}

	auth, err := BuildAwsAuth(t.Context(), creds)
	require.NoError(t, err)

	// Command prefix must be empty (no credentials in shell commands)
	assert.Empty(t, auth.CommandPrefix)
	assert.Empty(t, auth.CommandSuffix)
}

func TestGcpAuth_KeyWrittenViaEnvVarReference(t *testing.T) {
	// GCP key file should be written via $GCP_SA_KEY env var reference,
	// NOT with the literal key content in the command prefix
	creds := CloudAccountCredentials{
		ID:            "secret-test-gcp",
		AccessSecret:  strPtr(`{"type":"service_account","private_key":"-----BEGIN PRIVATE KEY-----"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	// The command prefix should reference the env var, NOT contain the actual key
	assert.Contains(t, auth.CommandPrefix, "$GCP_SA_KEY")
	assert.NotContains(t, auth.CommandPrefix, "BEGIN PRIVATE KEY")
}

func TestAzureAuth_SecretsViaEnvVarReferences(t *testing.T) {
	// Azure login command should use env var references, not literal secrets
	creds := CloudAccountCredentials{
		ID:            "secret-test-azure",
		AccessKey:     strPtr("my-client-id"),
		AccessSecret:  strPtr("super-secret-value"),
		AssumeRole:    strPtr("sub-123"),
		AccountNumber: "tenant-456",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)

	// The command prefix should reference env vars, not literal secrets
	assert.Contains(t, auth.CommandPrefix, "$AZURE_CLIENT_ID")
	assert.Contains(t, auth.CommandPrefix, "$AZURE_CLIENT_SECRET")
	assert.Contains(t, auth.CommandPrefix, "$AZURE_TENANT_ID")
	assert.Contains(t, auth.CommandPrefix, "$AZURE_SUBSCRIPTION_ID")
	assert.NotContains(t, auth.CommandPrefix, "super-secret-value")
}

// =============================================================================
// Side-effect: env var conflict tests
// =============================================================================

func TestAwsAuth_EnvKeysDoNotConflictWithGcpOrAzure(t *testing.T) {
	// Verify AWS env keys are namespaced correctly and don't overlap with GCP/Azure
	creds := CloudAccountCredentials{
		ID:            "conflict-test-aws",
		AccessKey:     strPtr("key"),
		AccessSecret:  strPtr("secret"),
		Region:        strPtr("us-east-1"),
		AccountNumber: "123456789012",
		CloudProvider: "aws",
	}

	auth, err := BuildAwsAuth(t.Context(), creds)
	require.NoError(t, err)

	for k := range auth.Env {
		assert.True(t, strings.HasPrefix(k, "AWS_"),
			"AWS env key %q should be namespaced with AWS_ prefix", k)
	}
}

func TestGcpAuth_EnvKeysDoNotConflictWithAwsOrAzure(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "conflict-test-gcp",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	awsKeys := []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_DEFAULT_REGION"}
	azureKeys := []string{"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID", "AZURE_SUBSCRIPTION_ID"}

	for _, k := range awsKeys {
		assert.Empty(t, auth.Env[k], "GCP auth should not set AWS key %q", k)
	}
	for _, k := range azureKeys {
		assert.Empty(t, auth.Env[k], "GCP auth should not set Azure key %q", k)
	}
}

func TestAzureAuth_EnvKeysDoNotConflictWithAwsOrGcp(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "conflict-test-azure",
		AccessKey:     strPtr("client-id"),
		AccessSecret:  strPtr("secret"),
		AssumeRole:    strPtr("sub-id"),
		AccountNumber: "tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)

	awsKeys := []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_DEFAULT_REGION"}
	gcpKeys := []string{"GCP_SA_KEY", "GCP_PROJECT_ID", "GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_CORE_PROJECT"}

	for _, k := range awsKeys {
		assert.Empty(t, auth.Env[k], "Azure auth should not set AWS key %q", k)
	}
	for _, k := range gcpKeys {
		assert.Empty(t, auth.Env[k], "Azure auth should not set GCP key %q", k)
	}
}

// =============================================================================
// Side-effect: non-cloud provider handling
// =============================================================================

func TestBuildCloudAuth_UnknownProvider_ReturnsNil(t *testing.T) {
	// buildCloudAuthEnv is a method on ShellTool that requires DB access.
	// Instead, we test the provider dispatch logic directly:
	// If cloud_provider is "kubernetes" or unknown, no auth should be built.
	// This is covered by the switch statement in buildCloudAuthEnv,
	// but we can verify the individual builders reject bad input.
	creds := CloudAccountCredentials{
		ID:            "k8s-only",
		CloudProvider: "kubernetes",
	}

	// None of the builders should be called for kubernetes,
	// but verify they handle missing creds gracefully
	_, err := BuildGcpAuth(creds)
	assert.Error(t, err, "GCP auth should fail for non-GCP creds")

	_, err = BuildAzureAuth(creds)
	assert.Error(t, err, "Azure auth should fail for non-Azure creds")
}

// =============================================================================
// ScrubCredentials tests — prevent accidental secret exposure in shell output
// =============================================================================

func TestScrubCredentials_RedactsSecretValues(t *testing.T) {
	env := map[string]string{
		"AWS_SECRET_ACCESS_KEY": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"AWS_ACCESS_KEY_ID":     "AKIAIOSFODNN7EXAMPLE",
	}

	output := "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE\nAWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n"
	scrubbed := ScrubCredentials(output, env)

	assert.Contains(t, scrubbed, "[REDACTED]", "secret value must be redacted")
	assert.NotContains(t, scrubbed, "wJalrXUtnFEMI", "secret key material must not appear in output")
	// Non-sensitive values (access key ID) should NOT be scrubbed
	assert.Contains(t, scrubbed, "AKIAIOSFODNN7EXAMPLE", "non-secret values should remain")
}

func TestScrubCredentials_RedactsAzureSecret(t *testing.T) {
	env := map[string]string{
		"AZURE_CLIENT_SECRET": "super-secret-client-value-12345",
		"AZURE_CLIENT_ID":     "my-client-id",
	}

	output := "AZURE_CLIENT_SECRET=super-secret-client-value-12345"
	scrubbed := ScrubCredentials(output, env)

	assert.Contains(t, scrubbed, "[REDACTED]")
	assert.NotContains(t, scrubbed, "super-secret-client-value-12345")
}

func TestScrubCredentials_RedactsGcpKey(t *testing.T) {
	env := map[string]string{
		"GCP_SA_KEY": `{"type":"service_account","private_key":"-----BEGIN PRIVATE KEY-----\nMIIE..."}`,
	}

	output := `Env dump: GCP_SA_KEY={"type":"service_account","private_key":"-----BEGIN PRIVATE KEY-----\nMIIE..."}`
	scrubbed := ScrubCredentials(output, env)

	assert.Contains(t, scrubbed, "[REDACTED]")
	assert.NotContains(t, scrubbed, "BEGIN PRIVATE KEY")
}

func TestScrubCredentials_SkipsShortValues(t *testing.T) {
	// Values <= 8 chars are not scrubbed (avoids false positives on common strings)
	env := map[string]string{
		"AWS_SECRET_ACCESS_KEY": "short",
	}

	output := "value is short"
	scrubbed := ScrubCredentials(output, env)
	assert.Equal(t, output, scrubbed, "short values should not be scrubbed")
}

func TestScrubCredentials_NoEnvVars(t *testing.T) {
	output := "hello world"
	assert.Equal(t, output, ScrubCredentials(output, map[string]string{}))
	assert.Equal(t, output, ScrubCredentials(output, nil))
}

func TestScrubCredentials_SessionToken(t *testing.T) {
	token := "FwoGZXIvYXdzEBYaDHt0MXlKS2VJdmRLdiLAAiL1234567890abcdef"
	env := map[string]string{
		"AWS_SESSION_TOKEN": token,
	}

	output := "AWS_SESSION_TOKEN=" + token
	scrubbed := ScrubCredentials(output, env)

	assert.Contains(t, scrubbed, "[REDACTED]")
	assert.NotContains(t, scrubbed, token)
}

// =============================================================================
// sanitizePathComponent tests — prevent path traversal in creds.ID
// =============================================================================

func TestSanitizePathComponent_UUID(t *testing.T) {
	// Normal UUID — should pass through unchanged
	assert.Equal(t, "abc-123-def", sanitizePathComponent("abc-123-def"))
}

func TestSanitizePathComponent_PathTraversal(t *testing.T) {
	// Path traversal attempt — dots and slashes become underscores
	assert.Equal(t, "______etc_passwd", sanitizePathComponent("../../etc/passwd"))
}

func TestSanitizePathComponent_ShellMetachars(t *testing.T) {
	// Shell metacharacters — replaced with underscores
	assert.Equal(t, "id_rm_-rf___", sanitizePathComponent("id;rm -rf /;"))
}

func TestSanitizePathComponent_Empty(t *testing.T) {
	assert.Equal(t, "", sanitizePathComponent(""))
}

func TestSanitizePathComponent_AlphanumericOnly(t *testing.T) {
	assert.Equal(t, "abc123", sanitizePathComponent("abc123"))
}

func TestSanitizePathComponent_HyphensAndUnderscores(t *testing.T) {
	assert.Equal(t, "my-account_id-123", sanitizePathComponent("my-account_id-123"))
}

func TestSanitizePathComponent_UsedInGcpKeyFile(t *testing.T) {
	// Verify GCP key file path uses sanitized ID
	creds := CloudAccountCredentials{
		ID:            "../evil",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	keyFile := auth.Env["GOOGLE_APPLICATION_CREDENTIALS"]
	assert.Contains(t, keyFile, "__evil", "path-traversal chars must be sanitized")
	assert.NotContains(t, keyFile, "..", "path-traversal sequences must not appear in key file path")
}

func TestSanitizePathComponent_UsedInAzureConfigDir(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "../evil",
		AccessKey:     strPtr("cid"),
		AccessSecret:  strPtr("cs"),
		AssumeRole:    strPtr("../sub-evil"),
		AccountNumber: "tenant",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)

	configDir := auth.Env["AZURE_CONFIG_DIR"]
	assert.Contains(t, configDir, "__evil")
	assert.NotContains(t, configDir, "..", "path-traversal sequences must not appear in Azure config dir")
	// Both account ID and subscription ID should be sanitized
	// "../evil" → "__evil" (dot→underscore, dot→underscore, slash→underscore)
	// "../sub-evil" → "__sub-evil"
	// Joined with "_" separator
	assert.Equal(t, "/tmp/azure_auth____evil____sub-evil", configDir)
}
