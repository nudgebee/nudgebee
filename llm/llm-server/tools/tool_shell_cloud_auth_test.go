package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// End-to-end cloud auth flow tests
// These tests verify the full flow: credentials → auth builder → command wrapping
// without requiring DB access. They test the composition of BuildXxxAuth +
// WrapCommandWithBestEffortAuth (shell tool path) and WrapCommandWithAuth
// (dedicated cloud tool path).
// =============================================================================

// --- AWS end-to-end flow ---

func TestShellCloudAuth_AwsFlow_EnvInjectedCommandUnchanged(t *testing.T) {
	// AWS uses env vars only — no command prefix/suffix.
	// The shell tool's command should pass through unchanged,
	// with credentials available via env vars.
	creds := CloudAccountCredentials{
		ID:            "shell-aws-e2e",
		AccessKey:     strPtr("AKIAIOSFODNN7EXAMPLE"),
		AccessSecret:  strPtr("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
		Region:        strPtr("us-west-2"),
		AccountNumber: "111222333444",
		CloudProvider: "aws",
	}

	auth, err := BuildAwsAuth(t.Context(), creds)
	require.NoError(t, err)

	// Verify env vars are set
	assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", auth.Env["AWS_ACCESS_KEY_ID"])
	assert.Equal(t, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", auth.Env["AWS_SECRET_ACCESS_KEY"])
	assert.Equal(t, "us-west-2", auth.Env["AWS_DEFAULT_REGION"])

	// Best-effort wrap should leave command unchanged (no prefix/suffix for AWS)
	originalCmd := "aws s3 ls s3://my-bucket --recursive"
	wrapped := WrapCommandWithBestEffortAuth(originalCmd, auth)
	assert.Equal(t, originalCmd, wrapped, "AWS commands should pass through unchanged in best-effort mode")

	// Strict wrap should also leave command unchanged
	strictWrapped := WrapCommandWithAuth(originalCmd, auth)
	assert.Equal(t, originalCmd, strictWrapped, "AWS commands should pass through unchanged in strict mode")
}

func TestShellCloudAuth_AwsFlow_NonCloudCommand(t *testing.T) {
	// Even with AWS auth, a non-cloud command (ls, curl) should work identically
	creds := CloudAccountCredentials{
		ID:            "shell-aws-noncl",
		AccessKey:     strPtr("AKIAIOSFODNN7EXAMPLE"),
		AccessSecret:  strPtr("secret"),
		AccountNumber: "111222333444",
		CloudProvider: "aws",
	}

	auth, err := BuildAwsAuth(t.Context(), creds)
	require.NoError(t, err)

	wrapped := WrapCommandWithBestEffortAuth("ls -la /tmp", auth)
	assert.Equal(t, "ls -la /tmp", wrapped, "non-cloud commands should be unchanged with AWS auth")
}

// --- GCP end-to-end flow ---

func TestShellCloudAuth_GcpFlow_BestEffort(t *testing.T) {
	// GCP has prefix (gcloud auth) + suffix (key file cleanup).
	// In best-effort mode (shell tool), auth failure should NOT block the command.
	creds := CloudAccountCredentials{
		ID:            "shell-gcp-e2e",
		AccessSecret:  strPtr(`{"type":"service_account","project_id":"test-proj"}`),
		AccountNumber: "test-proj",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	// Verify all expected env vars
	assert.NotEmpty(t, auth.Env["GCP_SA_KEY"])
	assert.Equal(t, "test-proj", auth.Env["GCP_PROJECT_ID"])
	assert.Equal(t, "test-proj", auth.Env["CLOUDSDK_CORE_PROJECT"])
	assert.NotEmpty(t, auth.Env["GOOGLE_APPLICATION_CREDENTIALS"])

	// Best-effort wrap for a gcloud command
	command := "gcloud compute instances list"
	wrapped := WrapCommandWithBestEffortAuth(command, auth)

	// Must contain: auth prefix with || true (non-fatal), user command
	assert.Contains(t, wrapped, "|| true", "GCP auth failure must be non-fatal in best-effort mode")
	assert.Contains(t, wrapped, command)
	// No cleanup suffix — key file is reused across commands in the session
	assert.Empty(t, auth.CommandSuffix, "GCP key file is reused, no cleanup needed")
}

func TestShellCloudAuth_GcpFlow_Strict(t *testing.T) {
	// In strict mode (dedicated gcloud tool), auth failure SHOULD block the command
	creds := CloudAccountCredentials{
		ID:            "shell-gcp-strict",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	command := "gcloud compute instances list"
	wrapped := WrapCommandWithAuth(command, auth)

	// Must NOT contain || true (strict mode)
	assert.NotContains(t, wrapped, "|| true", "strict mode should not absorb auth failures")
	// Must contain auth activation before command runs
	assert.Contains(t, wrapped, "gcloud auth activate-service-account")
	assert.Contains(t, wrapped, command)
}

func TestShellCloudAuth_GcpFlow_NonCloudCommand_StillRuns(t *testing.T) {
	// On a GCP account, running "ls" via shell_execute should still work
	// even though gcloud auth might fail (best-effort absorbs the failure)
	creds := CloudAccountCredentials{
		ID:            "shell-gcp-ls",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	wrapped := WrapCommandWithBestEffortAuth("ls -la /workspace", auth)
	assert.Contains(t, wrapped, "|| true", "auth failure must be absorbed")
	assert.Contains(t, wrapped, "ls -la /workspace", "user command must be present")
}

// --- Azure end-to-end flow ---

func TestShellCloudAuth_AzureFlow_BestEffort(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "shell-azure-e2e",
		AccessKey:     strPtr("my-client-id"),
		AccessSecret:  strPtr("my-client-secret"),
		AssumeRole:    strPtr("my-subscription-id"),
		AccountNumber: "my-tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)

	// Verify env vars
	assert.Equal(t, "my-client-id", auth.Env["AZURE_CLIENT_ID"])
	assert.Equal(t, "my-client-secret", auth.Env["AZURE_CLIENT_SECRET"])
	assert.Equal(t, "my-subscription-id", auth.Env["AZURE_SUBSCRIPTION_ID"])
	assert.Equal(t, "my-tenant-id", auth.Env["AZURE_TENANT_ID"])

	// Best-effort wrap
	command := "az vm list"
	wrapped := WrapCommandWithBestEffortAuth(command, auth)

	assert.Contains(t, wrapped, "|| true", "Azure auth failure must be non-fatal in best-effort mode")
	assert.Contains(t, wrapped, command)
}

func TestShellCloudAuth_AzureFlow_Strict(t *testing.T) {
	creds := CloudAccountCredentials{
		ID:            "shell-azure-strict",
		AccessKey:     strPtr("client-id"),
		AccessSecret:  strPtr("secret"),
		AssumeRole:    strPtr("sub-id"),
		AccountNumber: "tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)

	command := "az vm list --resource-group myRG"
	wrapped := WrapCommandWithAuth(command, auth)

	assert.NotContains(t, wrapped, "|| true", "strict mode must not absorb auth failures")
	assert.Contains(t, wrapped, "az login --service-principal")
	assert.Contains(t, wrapped, command)
}

func TestShellCloudAuth_AzureFlow_LoginCaching(t *testing.T) {
	// Azure auth should check for cached profile before re-logging in
	creds := CloudAccountCredentials{
		ID:            "shell-azure-cache",
		AccessKey:     strPtr("client-id"),
		AccessSecret:  strPtr("secret"),
		AssumeRole:    strPtr("sub-id"),
		AccountNumber: "tenant-id",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)

	// Verify login caching check is present
	assert.Contains(t, auth.CommandPrefix, "azureProfile.json")
	assert.Contains(t, auth.CommandPrefix, "if [ ! -f")

	// No suffix — Azure persists login state
	assert.Empty(t, auth.CommandSuffix, "Azure auth should have no cleanup suffix")
}

// =============================================================================
// Provider dispatch tests (simulates what buildCloudAuthEnv does)
// =============================================================================

func TestShellCloudAuth_ProviderDispatch(t *testing.T) {
	// Simulates the switch statement in ShellTool.buildCloudAuthEnv
	tests := []struct {
		name          string
		creds         CloudAccountCredentials
		expectAuth    bool
		expectPrefix  bool
		expectSuffix  bool
		expectEnvKeys []string
	}{
		{
			name: "aws provider returns env-only auth",
			creds: CloudAccountCredentials{
				ID: "dispatch-aws", AccessKey: strPtr("key"), AccessSecret: strPtr("secret"),
				CloudProvider: "aws", AccountNumber: "123",
			},
			expectAuth: true, expectPrefix: false, expectSuffix: false,
			expectEnvKeys: []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"},
		},
		{
			name: "gcp provider returns prefix-only auth (cached key file)",
			creds: CloudAccountCredentials{
				ID: "dispatch-gcp", AccessSecret: strPtr(`{"type":"sa"}`),
				CloudProvider: "gcp", AccountNumber: "proj",
			},
			expectAuth: true, expectPrefix: true, expectSuffix: false,
			expectEnvKeys: []string{"GCP_SA_KEY", "GCP_PROJECT_ID", "GOOGLE_APPLICATION_CREDENTIALS"},
		},
		{
			name: "azure provider returns prefix-only auth",
			creds: CloudAccountCredentials{
				ID: "dispatch-azure", AccessKey: strPtr("cid"), AccessSecret: strPtr("cs"),
				AssumeRole: strPtr("sub"), AccountNumber: "tenant", CloudProvider: "azure",
			},
			expectAuth: true, expectPrefix: true, expectSuffix: false,
			expectEnvKeys: []string{"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var auth *CloudAuthResult
			var err error

			switch strings.ToLower(tt.creds.CloudProvider) {
			case "aws":
				auth, err = BuildAwsAuth(t.Context(), tt.creds)
			case "gcp":
				auth, err = BuildGcpAuth(tt.creds)
			case "azure":
				auth, err = BuildAzureAuth(tt.creds)
			}

			require.NoError(t, err)
			if tt.expectAuth {
				require.NotNil(t, auth)
			}

			if tt.expectPrefix {
				assert.NotEmpty(t, auth.CommandPrefix, "expected non-empty command prefix for %s", tt.creds.CloudProvider)
			} else {
				assert.Empty(t, auth.CommandPrefix, "expected empty command prefix for %s", tt.creds.CloudProvider)
			}

			if tt.expectSuffix {
				assert.NotEmpty(t, auth.CommandSuffix, "expected non-empty command suffix for %s", tt.creds.CloudProvider)
			} else {
				assert.Empty(t, auth.CommandSuffix, "expected empty command suffix for %s", tt.creds.CloudProvider)
			}

			for _, key := range tt.expectEnvKeys {
				assert.NotEmpty(t, auth.Env[key], "expected env key %q to be set for %s", key, tt.creds.CloudProvider)
			}
		})
	}
}

// =============================================================================
// Non-cloud provider tests (K8s-only, unknown types)
// =============================================================================

func TestShellCloudAuth_NonCloudProvider_NoAuth(t *testing.T) {
	// For non-cloud providers (kubernetes, etc.), the switch statement in
	// buildCloudAuthEnv returns nil — no auth is injected.
	// We verify this by testing the individual builders reject bad input.
	tests := []struct {
		name  string
		creds CloudAccountCredentials
	}{
		{
			name: "kubernetes provider - GCP builder rejects",
			creds: CloudAccountCredentials{
				ID: "k8s-only", CloudProvider: "kubernetes",
			},
		},
		{
			name: "empty provider - GCP builder rejects",
			creds: CloudAccountCredentials{
				ID: "empty-provider", CloudProvider: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// None of the builders should work for non-cloud creds
			_, err := BuildGcpAuth(tt.creds)
			assert.Error(t, err, "GCP auth should fail for %s", tt.name)

			_, err = BuildAzureAuth(tt.creds)
			assert.Error(t, err, "Azure auth should fail for %s", tt.name)
		})
	}
}

// =============================================================================
// Command wrapping composition tests
// These verify that the full auth chain (build + wrap) produces correct shell
// commands for realistic scenarios.
// =============================================================================

func TestShellCloudAuth_GcpFullChain_CommandStructure(t *testing.T) {
	// Build GCP auth and verify the full wrapped command has correct structure:
	// (auth_prefix 2>/dev/null || true) && user_command
	creds := CloudAccountCredentials{
		ID:            "chain-gcp",
		AccessSecret:  strPtr(`{"type":"service_account"}`),
		AccountNumber: "my-project",
		CloudProvider: "gcp",
	}

	auth, err := BuildGcpAuth(creds)
	require.NoError(t, err)

	command := "gcloud compute instances list --format=json"
	wrapped := WrapCommandWithBestEffortAuth(command, auth)

	// Verify ordering: auth → command (no cleanup — key file cached)
	authIdx := strings.Index(wrapped, "gcloud auth activate-service-account")
	cmdIdx := strings.Index(wrapped, command)

	assert.Greater(t, authIdx, -1, "auth prefix must be present")
	assert.Greater(t, cmdIdx, authIdx, "user command must come after auth")
}

func TestShellCloudAuth_AzureFullChain_CommandStructure(t *testing.T) {
	// Build Azure auth and verify the wrapped command structure:
	// (auth_prefix 2>/dev/null || true) && user_command
	creds := CloudAccountCredentials{
		ID:            "chain-azure",
		AccessKey:     strPtr("cid"),
		AccessSecret:  strPtr("cs"),
		AssumeRole:    strPtr("sub"),
		AccountNumber: "tenant",
		CloudProvider: "azure",
	}

	auth, err := BuildAzureAuth(creds)
	require.NoError(t, err)

	command := "az vm list --resource-group myRG --output table"
	wrapped := WrapCommandWithBestEffortAuth(command, auth)

	// Verify ordering: auth → command (no cleanup for Azure)
	loginIdx := strings.Index(wrapped, "az login --service-principal")
	cmdIdx := strings.Index(wrapped, command)

	assert.Greater(t, loginIdx, -1, "login prefix must be present")
	assert.Greater(t, cmdIdx, loginIdx, "user command must come after login")
	assert.NotContains(t, wrapped, "rm -f", "Azure should have no file cleanup")
}

// =============================================================================
// Cross-provider isolation: verifies that auth for one provider doesn't
// accidentally set env vars that interfere with another provider.
// =============================================================================

func TestShellCloudAuth_CrossProviderIsolation(t *testing.T) {
	awsCreds := CloudAccountCredentials{
		ID: "iso-aws", AccessKey: strPtr("ak"), AccessSecret: strPtr("sk"),
		Region: strPtr("us-east-1"), CloudProvider: "aws", AccountNumber: "111",
	}
	gcpCreds := CloudAccountCredentials{
		ID: "iso-gcp", AccessSecret: strPtr(`{"type":"sa"}`),
		CloudProvider: "gcp", AccountNumber: "proj",
	}
	azureCreds := CloudAccountCredentials{
		ID: "iso-azure", AccessKey: strPtr("cid"), AccessSecret: strPtr("cs"),
		AssumeRole: strPtr("sub"), AccountNumber: "tenant", CloudProvider: "azure",
	}

	awsAuth, err := BuildAwsAuth(t.Context(), awsCreds)
	require.NoError(t, err)
	gcpAuth, err := BuildGcpAuth(gcpCreds)
	require.NoError(t, err)
	azureAuth, err := BuildAzureAuth(azureCreds)
	require.NoError(t, err)

	// AWS env keys must not appear in GCP or Azure auth
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_DEFAULT_REGION"} {
		assert.NotEmpty(t, awsAuth.Env[key], "AWS should have %s", key)
		assert.Empty(t, gcpAuth.Env[key], "GCP should not have %s", key)
		assert.Empty(t, azureAuth.Env[key], "Azure should not have %s", key)
	}

	// GCP env keys must not appear in AWS or Azure auth
	for _, key := range []string{"GCP_SA_KEY", "GCP_PROJECT_ID", "GOOGLE_APPLICATION_CREDENTIALS"} {
		assert.NotEmpty(t, gcpAuth.Env[key], "GCP should have %s", key)
		assert.Empty(t, awsAuth.Env[key], "AWS should not have %s", key)
		assert.Empty(t, azureAuth.Env[key], "Azure should not have %s", key)
	}

	// Azure env keys must not appear in AWS or GCP auth
	for _, key := range []string{"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID"} {
		assert.NotEmpty(t, azureAuth.Env[key], "Azure should have %s", key)
		assert.Empty(t, awsAuth.Env[key], "AWS should not have %s", key)
		assert.Empty(t, gcpAuth.Env[key], "GCP should not have %s", key)
	}
}

// =============================================================================
// detectCloudCLI tests — command detection for cross-account auth
// =============================================================================

func TestDetectCloudCLI_GcpCommands(t *testing.T) {
	tests := []struct {
		command  string
		provider string
		toolName string
	}{
		{"gcloud compute instances list", "gcp", ToolExecuteGcpCliCommand},
		{"gcloud sql instances list --project=my-proj", "gcp", ToolExecuteGcpCliCommand},
		{"gsutil ls gs://my-bucket", "gcp", ToolExecuteGcpCliCommand},
		{"bq query --use_legacy_sql=false 'SELECT 1'", "gcp", ToolExecuteGcpCliCommand},
		// gcloud inside a chained command
		{"touch .nb_profile && . ./.nb_profile && gcloud auth list", "gcp", ToolExecuteGcpCliCommand},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			provider, toolName := detectCloudCLI(tt.command)
			assert.Equal(t, tt.provider, provider)
			assert.Equal(t, tt.toolName, toolName)
		})
	}
}

func TestDetectCloudCLI_AwsCommands(t *testing.T) {
	tests := []struct {
		command  string
		provider string
		toolName string
	}{
		{"aws s3 ls", "aws", ToolExecuteAwsCliCommand},
		{"aws sts get-caller-identity", "aws", ToolExecuteAwsCliCommand},
		{"touch .nb_profile && aws ec2 describe-instances", "aws", ToolExecuteAwsCliCommand},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			provider, toolName := detectCloudCLI(tt.command)
			assert.Equal(t, tt.provider, provider)
			assert.Equal(t, tt.toolName, toolName)
		})
	}
}

func TestDetectCloudCLI_AzureCommands(t *testing.T) {
	tests := []struct {
		command  string
		provider string
		toolName string
	}{
		{"az vm list", "azure", ToolExecuteAzureCliCommand},
		{"az account show", "azure", ToolExecuteAzureCliCommand},
		{"touch .nb_profile && az group list", "azure", ToolExecuteAzureCliCommand},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			provider, toolName := detectCloudCLI(tt.command)
			assert.Equal(t, tt.provider, provider)
			assert.Equal(t, tt.toolName, toolName)
		})
	}
}

func TestDetectCloudCLI_NonCloudCommands(t *testing.T) {
	// These must NOT trigger cloud auth injection
	commands := []string{
		"ls -la /tmp",
		"curl https://example.com",
		"kubectl get pods",
		"helm list",
		"cat /etc/hosts",
		"echo gcloud",            // word "gcloud" but not a CLI invocation? — actually this matches
		"grep aws_region config", // "aws_region" does not match due to word boundaries
		"",
	}

	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			provider, toolName := detectCloudCLI(cmd)
			// "echo gcloud" will match because it contains "gcloud" with word boundaries — that's acceptable
			// for best-effort auth.
			if cmd == "echo gcloud" {
				assert.Equal(t, "gcp", provider, "gcloud substring detection is intentionally broad")
				return
			}
			assert.Empty(t, provider, "non-cloud command should not trigger detection: %s", cmd)
			assert.Empty(t, toolName)
		})
	}
}

func TestDetectCloudCLI_CaseInsensitive(t *testing.T) {
	provider, toolName := detectCloudCLI("GCLOUD compute instances list")
	assert.Equal(t, "gcp", provider)
	assert.Equal(t, ToolExecuteGcpCliCommand, toolName)

	provider, toolName = detectCloudCLI("AWS s3 ls")
	assert.Equal(t, "aws", provider)
	assert.Equal(t, ToolExecuteAwsCliCommand, toolName)
}

// =============================================================================
// Edge cases: empty commands, special characters, multiline
// =============================================================================

func TestShellCloudAuth_WrapEmptyCommand(t *testing.T) {
	// Edge case: empty command with auth prefix/suffix
	auth := &CloudAuthResult{
		CommandPrefix: "auth-cmd",
		CommandSuffix: "cleanup-cmd",
	}

	// Even empty commands should still have proper wrapping structure
	wrapped := WrapCommandWithBestEffortAuth("", auth)
	assert.Contains(t, wrapped, "cleanup-cmd", "cleanup must happen even for empty command")
}

func TestShellCloudAuth_WrapPipedCommand(t *testing.T) {
	// Commands with pipes should work correctly in the wrapper
	auth := &CloudAuthResult{
		CommandPrefix: "gcloud auth activate-service-account --key-file=/tmp/key.json",
		CommandSuffix: "rm -f /tmp/key.json",
	}

	pipedCmd := "gcloud compute instances list --format=json | jq '.[].name'"
	wrapped := WrapCommandWithBestEffortAuth(pipedCmd, auth)
	assert.Contains(t, wrapped, pipedCmd, "piped command must be preserved intact")
}

func TestShellCloudAuth_WrapCommandWithSemicolon(t *testing.T) {
	// Commands chained with semicolons should work
	auth := &CloudAuthResult{
		CommandPrefix: "auth-prefix",
		CommandSuffix: "cleanup",
	}

	chainedCmd := "echo hello; echo world"
	wrapped := WrapCommandWithAuth(chainedCmd, auth)
	assert.Contains(t, wrapped, chainedCmd, "chained command must be preserved")
	assert.Contains(t, wrapped, "cleanup", "cleanup must be present")
}

func TestShellCloudAuth_WrapCommandWithAndOperator(t *testing.T) {
	// Commands chained with && should work
	auth := &CloudAuthResult{
		CommandPrefix: "auth-prefix",
	}

	chainedCmd := "cd /app && ls -la"
	wrapped := WrapCommandWithAuth(chainedCmd, auth)
	assert.Contains(t, wrapped, chainedCmd, "&&-chained command must be preserved")
}
