package tools

import "strings"

var sensitiveEnvKeys = []string{
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
	"AZURE_CLIENT_SECRET",
	"GCP_SA_KEY",
	"GITHUB_TOKEN",
	"GITLAB_TOKEN",
	"GH_TOKEN",
	"GH_ENTERPRISE_TOKEN",
	"GITHUB_ENTERPRISE_TOKEN",
}

// ScrubCredentials replaces sensitive environment values in command output.
// Provider credentials no longer enter workspaces; those keys remain here as
// defense in depth while mixed-version workspace pods roll over.
func ScrubCredentials(output string, env map[string]string) string {
	for _, key := range sensitiveEnvKeys {
		if val, ok := env[key]; ok && len(val) > 8 {
			output = strings.ReplaceAll(output, val, "[REDACTED]")
		}
	}
	return output
}

// CloudAuthResult is the environment carrier used by SCM CLI tools. Cloud
// provider CLIs authenticate behind the cloud-collector boundary instead.
type CloudAuthResult struct {
	Env map[string]string
}
