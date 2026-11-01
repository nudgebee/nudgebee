package executors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A script step's account has to be one its executor can run for; the error
// names the change that makes the step work.
func TestCheckAccountSupports(t *testing.T) {
	for _, tc := range []struct {
		executor, provider string
		want               string // substring of the error; "" means the pair works
	}{
		// The pair that broke saved workflows: no cloud credentials to mint.
		{"kubernetes", "K8s", "set Executor Type to Agent"},
		{"Kubernetes", "kubernetes", "set Executor Type to Agent"},
		{"aws_ssm", "K8s", "needs an AWS account"},
		{"azure_run_command", "K8s", "needs an Azure account"},
		{"gcp_compute_ssh", "K8s", "needs a GCP account"},
		{"kubernetes", "SelfHosted", "a SelfHosted account has no cloud credentials"},
		{"aws_ssm", "Azure", "needs an AWS account, and this step's account is Azure"},
		{"gcp_compute_ssh", "AWS", "needs a GCP account"},

		{"kubernetes", "AWS", ""},
		{"kubernetes", "Azure", ""},
		{"kubernetes", "GCP", ""},
		{"kubernetes", "gcloud", ""},
		{"aws_ssm", "AWS", ""},
		{"azure_run_command", "Azure", ""},
		{"gcp_compute_ssh", "GCP", ""},
		{"agent", "K8s", ""},
		{"ssh", "K8s", ""},
		// Nothing to judge: no executor named, no account, or an executor
		// NewExecutor itself will reject.
		{"", "K8s", ""},
		{"kubernetes", "", ""},
		{"teleport", "K8s", ""},
	} {
		err := CheckAccountSupports(tc.executor, tc.provider)
		if tc.want == "" {
			assert.NoError(t, err, "%s on %s", tc.executor, tc.provider)
			continue
		}
		if assert.Error(t, err, "%s on %s", tc.executor, tc.provider) {
			assert.Contains(t, err.Error(), tc.want)
		}
	}
}
