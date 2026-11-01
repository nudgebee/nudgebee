package executors

import (
	"regexp"
	"strings"
)

// loginPatterns are the ways a script signs itself in. A script step already
// runs as the account it names — with credentials fetched for that account on
// Nudgebee's executor, or as the agent inside the cluster — so a script that
// logs in again either fails (the fetched session may not assume roles) or
// quietly swaps to an identity nobody chose. Mirrored by the app's Run Script
// hint (app/src/components/workflow/utils/scriptRunLocation.ts); both are
// checked against testdata/script_step_rules.json.
var loginPatterns = []struct {
	what string
	re   *regexp.Regexp
}{
	{"sts assume-role", regexp.MustCompile(`(?i)\bsts\s+assume-role\b`)},
	{"a hard-coded role ARN", regexp.MustCompile(`arn:aws:iam::\d{12}:role/`)},
	{"aws eks update-kubeconfig", regexp.MustCompile(`(?i)\bupdate-kubeconfig\b`)},
	{"az login", regexp.MustCompile(`(?i)\baz\s+login\b`)},
	{"gcloud auth", regexp.MustCompile(`(?i)\bgcloud\s+auth\s+(activate-service-account|login)\b`)},
	{"unsetting the in-cluster Kubernetes settings", regexp.MustCompile(`\bunset\b[^\n;]*\bKUBERNETES_SERVICE_HOST\b`)},
	{"AWS access keys", regexp.MustCompile(`\bAWS_(ACCESS_KEY_ID|SECRET_ACCESS_KEY|SESSION_TOKEN)\s*=`)},
}

var awsKeyNames = map[string]bool{"AWS_ACCESS_KEY_ID": true, "AWS_SECRET_ACCESS_KEY": true, "AWS_SESSION_TOKEN": true}

// ScriptLogin reports how a script, or the environment it is given, signs
// itself in — "" when it does not.
func ScriptLogin(script string, env map[string]string) string {
	for _, p := range loginPatterns {
		if p.re.MatchString(script) {
			return p.what
		}
	}
	for k := range env {
		if awsKeyNames[strings.ToUpper(k)] {
			return "AWS access keys"
		}
	}
	return ""
}
