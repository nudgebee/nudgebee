package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// sensitiveEnvKeys lists environment variable names whose values must be
// scrubbed from command output to prevent accidental credential exposure.
var sensitiveEnvKeys = []string{
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
	"AZURE_CLIENT_SECRET",
	"GCP_SA_KEY",
	"GITHUB_TOKEN",
}

// ScrubCredentials replaces any occurrence of sensitive credential values in
// output with [REDACTED]. This prevents accidental exposure when a shell
// command (e.g. "env", "printenv") dumps environment variables.
func ScrubCredentials(output string, env map[string]string) string {
	for _, key := range sensitiveEnvKeys {
		if val, ok := env[key]; ok && len(val) > 8 {
			output = strings.ReplaceAll(output, val, "[REDACTED]")
		}
	}
	return output
}

// sanitizePathComponent replaces any character that is not alphanumeric, hyphen,
// or underscore with an underscore. This prevents path-traversal attacks when
// creds.ID is interpolated into file paths (defense-in-depth).
func sanitizePathComponent(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

// CloudAuthResult holds the environment variables and optional command prefix
// needed to authenticate a cloud CLI command in a workspace.
type CloudAuthResult struct {
	// Env contains environment variables to inject (e.g. AWS_ACCESS_KEY_ID, GCP_SA_KEY).
	Env map[string]string
	// CommandPrefix is a shell command that must run before the user's command
	// (e.g. gcloud auth activate-service-account). Empty for providers that
	// only need env vars (AWS).
	CommandPrefix string
	// CommandSuffix is a shell snippet appended after the user's command for cleanup
	// (e.g. removing a temporary GCP key file). Empty when no cleanup is needed.
	CommandSuffix string
}

// BuildAwsAuth builds the environment variables needed to authenticate AWS CLI
// commands in a workspace. It handles both static credentials and STS AssumeRole.
func BuildAwsAuth(ctx context.Context, creds CloudAccountCredentials) (*CloudAuthResult, error) {
	result := &CloudAuthResult{Env: map[string]string{}}

	var opts []func(*awsconfig.LoadOptions) error
	if creds.Region != nil {
		result.Env["AWS_DEFAULT_REGION"] = *creds.Region
		opts = append(opts, awsconfig.WithRegion(*creds.Region))
	}

	if creds.AccessKey != nil && creds.AccessSecret != nil {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(*creds.AccessKey, *creds.AccessSecret, ""),
		))
	}

	// Load AWS Config (uses static creds if provided, otherwise falls back to environment/pod identity)
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("aws: failed to load config: %w", err)
	}

	// Handle Role Assumption if configured
	if creds.AssumeRole != nil && *creds.AssumeRole != "" {
		stsClient := sts.NewFromConfig(cfg)

		roleArn := *creds.AssumeRole
		assumeRoleOutput, err := stsClient.AssumeRole(ctx, &sts.AssumeRoleInput{
			RoleArn:         aws.String(roleArn),
			RoleSessionName: aws.String("nudgebee-workspace-session"),
		})
		if err != nil {
			errMsg := err.Error()
			// STS 403/AccessDenied is a permanent credential error — do not retry.
			if strings.Contains(errMsg, "403") || strings.Contains(errMsg, "AccessDenied") || strings.Contains(errMsg, "not authorized") {
				return nil, fmt.Errorf("PERMANENT ERROR: AWS STS AssumeRole failed for role %s. This is a credentials/permission issue that cannot be resolved by retrying. Error: %s. Check IAM trust policy and permissions", roleArn, errMsg)
			}
			return nil, fmt.Errorf("aws: failed to assume role %s: %w", roleArn, err)
		}

		result.Env["AWS_ACCESS_KEY_ID"] = *assumeRoleOutput.Credentials.AccessKeyId
		result.Env["AWS_SECRET_ACCESS_KEY"] = *assumeRoleOutput.Credentials.SecretAccessKey
		result.Env["AWS_SESSION_TOKEN"] = *assumeRoleOutput.Credentials.SessionToken
	} else {
		// No role to assume, pass static credentials directly
		if creds.AccessKey != nil {
			result.Env["AWS_ACCESS_KEY_ID"] = *creds.AccessKey
		}
		if creds.AccessSecret != nil {
			result.Env["AWS_SECRET_ACCESS_KEY"] = *creds.AccessSecret
		}
	}

	return result, nil
}

// BuildGcpAuth builds the environment variables and auth command prefix needed
// to authenticate gcloud CLI commands in a workspace.
func BuildGcpAuth(creds CloudAccountCredentials) (*CloudAuthResult, error) {
	result := &CloudAuthResult{Env: map[string]string{}}

	if creds.AccessSecret == nil {
		return nil, fmt.Errorf("gcp: service_account_key (access_secret) is required")
	}
	if creds.AccountNumber == "" {
		return nil, fmt.Errorf("gcp: project_id (account_number) is required")
	}

	result.Env["GCP_SA_KEY"] = *creds.AccessSecret
	result.Env["GCP_PROJECT_ID"] = creds.AccountNumber

	// Optimize gcloud configuration using environment variables
	result.Env["CLOUDSDK_CORE_PROJECT"] = creds.AccountNumber
	result.Env["CLOUDSDK_CORE_DISABLE_PROMPTS"] = "1"
	result.Env["TERM"] = "xterm"

	// Use a per-account gcloud config directory to isolate auth state between different
	// GCP accounts. Without this, activating account B overwrites account A's active
	// session, causing "no active account selected" errors on subsequent account A calls.
	configDir := fmt.Sprintf("/tmp/gcp_config_%s", sanitizePathComponent(creds.ID))
	result.Env["CLOUDSDK_CONFIG"] = configDir

	// Store the key file inside the per-account config directory so both the key and
	// gcloud's auth state share the same lifecycle.
	keyFile := fmt.Sprintf("%s/sa_key.json", configDir)
	result.Env["GOOGLE_APPLICATION_CREDENTIALS"] = keyFile

	// flock serializes the key-write + activate block so concurrent gcloud commands
	// for the same account don't race on the key file. util-linux (which provides
	// flock) is installed in the workspace image.
	// We use a sentinel file (.auth_complete) instead of the key file because
	// the key file is written before gcloud auth runs. If the key write succeeds
	// but activation fails, checking the key file would skip auth on retry,
	// leaving the service account unconfigured. The sentinel is only created
	// after activation succeeds.
	//
	// mkdir -p ensures the config directory exists before flock tries to open the
	// lock file inside it. Without this, a fresh pod (or any /tmp cleanup) causes
	// flock to fail immediately with "can't open ... No such file or directory".
	result.CommandPrefix = fmt.Sprintf(
		"mkdir -p '%[2]s' && flock -x '%[1]s.lock' sh -c "+
			"'if [ ! -f \"%[1]s.auth_complete\" ]; then "+
			"(umask 077 && printf \"%%s\" \"$GCP_SA_KEY\" > \"%[1]s\") && "+
			"{ gcloud auth activate-service-account --key-file=\"%[1]s\" --verbosity=none 2>/dev/null || "+
			"{ echo \"gcp: service account auth failed - check credentials\"; exit 1; }; } && "+
			"touch \"%[1]s.auth_complete\"; "+
			"fi'", keyFile, configDir)

	// No cleanup suffix — config directory is reused across commands in the same workspace.
	// Workspace pods are ephemeral and cleaned up after the conversation.

	return result, nil
}

// BuildAzureAuth builds the environment variables and login command prefix needed
// to authenticate az CLI commands in a workspace.
func BuildAzureAuth(creds CloudAccountCredentials) (*CloudAuthResult, error) {
	result := &CloudAuthResult{Env: map[string]string{}}

	if creds.AccessKey == nil {
		return nil, fmt.Errorf("azure: client_id (access_key) is required")
	}
	if creds.AccessSecret == nil {
		return nil, fmt.Errorf("azure: client_secret (access_secret) is required")
	}
	if creds.AssumeRole == nil {
		return nil, fmt.Errorf("azure: subscription_id (assume_role) is required")
	}

	result.Env["AZURE_CLIENT_ID"] = *creds.AccessKey
	result.Env["AZURE_CLIENT_SECRET"] = *creds.AccessSecret
	result.Env["AZURE_SUBSCRIPTION_ID"] = *creds.AssumeRole

	tenantId := creds.AccountNumber
	if tenantId == "" {
		return nil, fmt.Errorf("azure: tenant_id not found in credentials (ensure 'tenantId' is set in account configuration)")
	}
	result.Env["AZURE_TENANT_ID"] = tenantId

	// Environment optimizations for non-interactive use
	result.Env["AZURE_CORE_NO_COLOR"] = "true"

	// Use absolute path for config directory to avoid issues if commands change working directory.
	// Per-account + per-subscription directory prevents race conditions when parallel commands
	// share a workspace pod. Previously, a single per-account config dir caused concurrent
	// `az login` calls to corrupt the shared azureProfile.json, and `az account set` running
	// outside the cache guard could collide with a parallel login in progress.
	configDir := fmt.Sprintf("/tmp/azure_auth_%s_%s",
		sanitizePathComponent(creds.ID),
		sanitizePathComponent(*creds.AssumeRole))
	result.Env["AZURE_CONFIG_DIR"] = configDir

	// AZURE_EXTENSION_DIR decouples extension storage from AZURE_CONFIG_DIR.
	// Without this, extensions pre-installed in the workspace image are not found
	// because Azure CLI looks in $AZURE_CONFIG_DIR/cliextensions/ (an empty temp dir).
	result.Env["AZURE_EXTENSION_DIR"] = "/opt/azure-cli-extensions"

	// Login AND subscription selection only if a sentinel file doesn't exist (avoids 2s+ re-login).
	// Both az login and az account set are inside the guard to prevent race conditions.
	//
	// We use a sentinel file (.auth_complete) instead of azureProfile.json because az login
	// creates azureProfile.json before az account set runs. If login succeeds but account set
	// fails, checking azureProfile.json would skip auth on retry, leaving the subscription
	// unconfigured. The sentinel is only created after both steps succeed.
	//
	// flock serializes the entire check+login+set block so parallel commands on the same
	// workspace pod cannot both pass the sentinel check and race on az login. util-linux
	// (which provides flock) is installed in the workspace image.
	result.CommandPrefix = fmt.Sprintf("mkdir -p '%[1]s' && "+
		"flock -x '%[1]s/.auth.lock' sh -c "+
		"'if [ ! -f \"%[1]s/.auth_complete\" ]; then "+
		"az login --service-principal -u \"$AZURE_CLIENT_ID\" -p \"$AZURE_CLIENT_SECRET\" --tenant \"$AZURE_TENANT_ID\" || "+
		"{ echo \"azure: login failed - check credentials\"; exit 1; }; "+
		"az account set --subscription \"$AZURE_SUBSCRIPTION_ID\" || "+
		"{ echo \"azure: subscription set failed - check subscription ID\"; exit 1; }; "+
		"touch \"%[1]s/.auth_complete\"; "+
		"fi'", configDir)

	return result, nil
}

// WrapCommandWithAuth wraps a command with the auth prefix and cleanup suffix.
// Auth failure blocks the user's command (appropriate for dedicated cloud tools).
// For AWS (no prefix/suffix), it returns the command unchanged.
func WrapCommandWithAuth(command string, auth *CloudAuthResult) string {
	if auth.CommandPrefix == "" && auth.CommandSuffix == "" {
		return command
	}

	if auth.CommandSuffix != "" {
		// Wrap in subshell to ensure cleanup happens regardless of command success
		return fmt.Sprintf("(%s && %s\n); status=$?; %s; exit $status", auth.CommandPrefix, command, auth.CommandSuffix)
	}

	return fmt.Sprintf("%s && %s", auth.CommandPrefix, command)
}

// WrapCommandWithBestEffortAuth wraps a command with auth that is allowed to fail.
// If the auth prefix fails, the user's command still executes (it just won't have
// cloud CLI access). This is appropriate for the shell tool where the command may
// not be cloud-related at all (e.g. ls, curl, grep).
// For AWS (no prefix/suffix), it returns the command unchanged.
func WrapCommandWithBestEffortAuth(command string, auth *CloudAuthResult) string {
	if auth.CommandPrefix == "" && auth.CommandSuffix == "" {
		return command
	}

	if auth.CommandSuffix != "" {
		// Try auth, run command regardless, then clean up
		return fmt.Sprintf("(%s 2>/dev/null || true) && %s\n; status=$?; %s; exit $status",
			auth.CommandPrefix, command, auth.CommandSuffix)
	}

	// Try auth prefix but don't fail if it errors
	return fmt.Sprintf("(%s 2>/dev/null || true) && %s", auth.CommandPrefix, command)
}
