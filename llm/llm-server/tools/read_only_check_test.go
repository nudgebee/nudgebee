package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

// The alert's follow-up check (#40734) reads through kubectl_execute and the cloud CLI tools under
// NBQueryConfig.StrictReadOnly: readOnlyCheckRefusal. These are its rules and their effect on the tools.

// A check NudgeBee runs without a confirmation is one bounded kubectl read on the agent's own cluster.
func TestValidateKubectlReadOnly(t *testing.T) {
	reads := []string{
		"kubectl get pods -n shop",
		"kubectl get deployment api -n shop -o jsonpath='{.status.readyReplicas}'",
		"kubectl describe pod api-1 -n shop",
		"kubectl logs deploy/api -n shop --tail=50",
		"kubectl rollout status deployment/api -n shop --timeout=90s",
		"kubectl rollout history deployment/api -n shop",
		"kubectl auth can-i patch deployments -n shop",
		"kubectl top pods -n shop",
		"kubectl events -n shop",
	}
	for _, c := range append(reads,
		"kubectl get pods -nshop",               // -n's value is "shop": its s is not -s
		"kubectl get pods -A -lapp=web",         // -l's value, not flags
		"kubectl logs api-1 -c sidecar -n shop", // -c's value
		"kubectl get pods -o wide -n shop",
	) {
		assert.NoError(t, ValidateKubectlReadOnly(c), c)
	}
	notReads := []string{
		"kubectl rollout restart deployment/api -n shop", // a write
		"kubectl scale deployment api --replicas=2 -n shop",
		"kubectl delete pod api-1 -n shop",
		"kubectl exec api-1 -n shop -- cat /etc/hosts",
		"kubectl -n shop get pods",                              // verb not first
		"kubectl get pods -n shop -w",                           // never ends
		"kubectl logs deploy/api -n shop -f",                    // never ends
		"kubectl get --raw /api/v1/namespaces/shop/secrets",     // any API path
		"kubectl get pods -n shop --context=prod",               // another cluster
		"kubectl get pods -n shop --server https://10.0.0.1",    // another API server
		"kubectl get pods -n shop --as=system:admin",            // another identity
		"kubectl get secrets -n shop",                           // secret-bearing kind
		"kubectl get pods -n shop; kubectl delete ns shop",      // two commands
		"aws cloudwatch describe-alarms --alarm-names cpu-high", // not kubectl
		"helm status api -n shop",                               // not kubectl
		"kubectl rollout",                                       // incomplete
		// One-letter flags bundled the way kubectl's parser accepts them.
		"kubectl get pods -Aw",                               // -A -w
		"kubectl get pods -wA",                               // -w -A
		"kubectl events -Aw",                                 // -A -w
		"kubectl logs api-1 -fn default",                     // -f -n default
		"kubectl get -f/etc/hostname",                        // -f /etc/hostname
		"kubectl get -Af /etc/hostname",                      // -A -f
		"kubectl get -k /tmp/kustomize",                      // kustomization dir
		"kubectl get pods -shttps://10.0.0.1",                // -s https://...
		"kubectl get pods -Ashttps://10.0.0.1",               // -A -s https://...
		"kubectl get pods -As https://10.0.0.1",              // -A -s https://...
		"kubectl get pods -o go-template-file=/etc/hostname", // reads an agent file
		"kubectl get pods -ojsonpath-file=/etc/hostname",     // reads an agent file
		"kubectl get pods --template-file=/etc/hostname",     // reads an agent file
	}
	for _, c := range notReads {
		assert.Error(t, ValidateKubectlReadOnly(c), c)
	}
}

// An approved command runs on the account's own cluster as the agent: no flag may point it elsewhere.
func TestKubectlTargetOverride(t *testing.T) {
	assert.Empty(t, KubectlTargetOverride("kubectl rollout restart deployment/api -n shop"))
	assert.Empty(t, KubectlTargetOverride("kubectl rollout restart deployment config -n shop"), "a resource named config")
	for _, c := range []string{"", "   ", "kubectl"} {
		assert.Equal(t, "unparseable command", KubectlTargetOverride(c), "%q", c)
	}
	for _, c := range []string{"kubectl config use-context other", "kubectl -n shop config set-context --current --namespace=x", "kubectl --request-timeout 5s config view"} {
		assert.Equal(t, "kubectl config", KubectlTargetOverride(c), c)
	}
	for flag, c := range map[string]string{
		"--context":    "kubectl rollout restart deployment/api -n shop --context prod",
		"--kubeconfig": "kubectl rollout restart deployment/api --kubeconfig=/tmp/other",
		"--server":     "kubectl rollout restart deployment/api --server=https://10.0.0.1",
		"-s":           "kubectl rollout restart deployment/api -s https://10.0.0.1",
		"--as":         "kubectl rollout restart deployment/api --as system:admin",
		"--token":      "kubectl rollout restart deployment/api --token abc",
		"--cluster":    "kubectl rollout restart deployment/api --cluster=other",
	} {
		assert.Equal(t, flag, KubectlTargetOverride(c), c)
	}
	for _, c := range []string{
		"kubectl rollout restart deployment/api -shttps://10.0.0.1",
		"kubectl rollout restart deployment/api -As https://10.0.0.1",
		"kubectl rollout restart deployment/api -Ashttps://10.0.0.1",
	} {
		assert.Equal(t, "-s", KubectlTargetOverride(c), c)
	}
	assert.Empty(t, KubectlTargetOverride("kubectl rollout restart deployment/api -nshop"), "-n's value is not a flag")
	for flag, c := range map[string]string{
		"--as": "kubectl rollout restart deployment/api --as=system:admin",
	} {
		assert.Equal(t, flag, KubectlTargetOverride(c), c)
	}
	assert.NotEmpty(t, KubectlTargetOverride(`kubectl get pods "unterminated`), "fails closed")
}

// A cloud read is sent only when it is the account's own CLI, has no shell syntax, and the CLI's own
// rules classify it as a read; every refusal says why.
func TestCloudReadRejection(t *testing.T) {
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "cloud-read-test"}
	cli, err := CloudReadRejection(ctx.Ctx, ctx.AccountId, "aws", "aws rds describe-db-instances --db-instance-identifier orders")
	require.NoError(t, err)
	assert.Equal(t, ToolExecuteAwsCliCommand, cli)

	for provider, command := range map[string]string{
		"aws":   "aws ec2 terminate-instances --instance-ids i-1",
		"k8s":   "kubectl get pods",
		"azure": "aws rds describe-db-instances",
		"gcp":   "gcloud compute instances list | sh",
	} {
		_, err := CloudReadRejection(ctx.Ctx, ctx.AccountId, provider, command)
		assert.Error(t, err, provider+": "+command)
	}
	_, err = CloudReadRejection(ctx.Ctx, ctx.AccountId, "aws", "aws rds describe-db-instances > /tmp/x")
	assert.ErrorContains(t, err, "shell syntax")
	_, err = CloudReadRejection(ctx.Ctx, ctx.AccountId, "aws", "")
	assert.Error(t, err)
}

// Review 3 bug 2: calls the CLIs' rules class as reads but that return credentials or secrets, or point the
// CLI at another identity, endpoint or configuration, are not sent.
func TestCloudReadDenied(t *testing.T) {
	for _, command := range []string{
		"aws sts get-session-token",
		"aws --region us-east-1 sts assume-role --role-arn arn:aws:iam::1:role/x --role-session-name s",
		"aws secretsmanager get-secret-value --secret-id db",
		"aws ssm get-parameter --name /db/password --with-decryption",
		"aws ecr get-login-password",
		"aws ec2 describe-instances --endpoint-url https://evil.example",
		"aws ec2 describe-instances --profile=prod-admin",
		"gcloud auth print-access-token",
		"gcloud config set project other",
		"gcloud compute instances list --impersonate-service-account sa@p.iam.gserviceaccount.com",
		"gcloud secrets versions access latest --secret db",
		"az account get-access-token",
		"az keyvault secret show --vault-name v --name db",
		"aws secretsmanager --region us-east-1 get-secret-value --secret-id x",
		"aws sts --region us-east-1 get-session-token",
		"gcloud secrets --project p versions access latest --secret=x",
		"aws sso get-role-credentials --role-name r --account-id 1 --access-token t",
		"az storage account keys list -n x",
		// Review of #41596: classed as reads by the CLIs' rules, but they return secrets, switch the kubeconfig
		// or write local files.
		"az acr credential show -n x",
		"az cosmosdb keys list -n x -g g",
		"az webapp config appsettings list -n x -g g",
		"az functionapp keys list -n x -g g",
		"az aks get-credentials -g g -n n",
		"gcloud container clusters get-credentials c",
		"az storage blob download -c c -n n -f /tmp/x",
		"aws ec2 describe-instances --cli-input-json file:///etc/passwd",
		"aws s3api get-object --bucket b --key k /tmp/out",
		"aws s3 cp s3://b/k /tmp/k",
	} {
		assert.NotEmpty(t, cloudReadDenied(command), command)
		_, err := CloudReadRejection(nil, "", "aws", command)
		assert.Error(t, err, command)
	}
	for _, command := range []string{
		"aws ec2 describe-instances --instance-ids i-1",
		"aws cloudwatch describe-alarms --alarm-names cpu-high",
		"gcloud compute instances list",
		"az vm list -g rg",
		"aws ssm get-parameter --name /app/flag",
		"aws s3 ls s3://b",
		"az aks show -g g -n n",
		"gcloud container clusters describe c",
	} {
		assert.Empty(t, cloudReadDenied(command), command)
	}
}

// unsafeKubectlReads are refused in the follow-up check, whatever the classifier would make of them.
var unsafeKubectlReads = []string{
	"kubectl -n x delete pod y",
	"kubectl --unknown get pods",
	"kubectl -v 9 delete pod y",
	"kubectl --kubeconfig=/x delete pod y",
	`kubectl "delete" pod x`,
	"kubectl DELETE pod x",
	"/usr/bin/kubectl delete pod x",
	"kubectl\tdelete pod x",
	"kubectl get pods\nkubectl delete pod x",
	"kubectl get pods && kubectl delete pod x",
	"kubectl get pods | xargs kubectl delete pod",
	"kubectl get pods; kubectl delete pod x",
	"kubectl get pods $(kubectl delete pod x)",
	"kubectl get --raw /api/v1/namespaces/x/secrets",
	"kubectl rollout restart deploy/api",
	"kubectl rollout undo deploy/api",
	"kubectl auth reconcile -f rbac.yaml",
	"kubectl get pods --server=https://x --insecure-skip-tls-verify",
	"kubectl get pods --token t",
	"kubectl get pods --as=system:admin",
	"kubectl get pods --context other",
	"kubectl get pods -shttps://x",
	"kubectl exec api-1 -- cat /etc/hostname",
	"kubectl port-forward svc/api 8080:80",
	"kubectl scale deploy/api --replicas=0",
	"kubectl get pods -w",
	"kubectl logs api-1 -f",
	"kubectl get secrets -n shop",
	"kubectl config view",
	"kubectl get pods -o custom-columns-file=/etc/passwd",
	"kubectl get pods -ocustom-columns-file=/proc/self/environ",
	"kubectl get pods --output=custom-columns-file=/tmp/x",
	"get pods",
	"",
}

func strict(ctx core.NbToolContext) core.NbToolContext {
	ctx.QueryConfig.StrictReadOnly = true
	return ctx
}

// In the follow-up check kubectl_execute runs a strict read on the conversation's own cluster and nothing
// else: each refused call is answered with its reason and never reaches the workspace.
func TestKubectlExecuteInTheFollowupCheck(t *testing.T) {
	const account = "kubectl-followup-check"
	clusterFixture(t, account)
	capture := &targetWorkspaceCapture{}
	tool := KubectlExecuteTool{workspaceManager: capture}
	ctx := strict(kubectlCallCtx(t, account, tool))

	for _, command := range unsafeKubectlReads {
		resp, err := tool.Call(ctx, core.NBToolCallRequest{Command: command})
		require.Error(t, err, command)
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status, command)
		assert.Contains(t, resp.Data, "read-only check", "the model is told why: %q", command)
	}
	_, err := tool.Call(ctx, core.NBToolCallRequest{Command: "kubectl get pods -n shop", Arguments: map[string]any{"cluster": "k8s-dev"}})
	require.ErrorContains(t, err, `omit "cluster"`)
	require.Empty(t, capture.command, "no refused call reached the workspace")

	for _, command := range []string{"kubectl get pods -n shop -o wide", "kubectl rollout status deploy/api -n shop", "kubectl logs deploy/api -n shop --tail 200"} {
		_, err := tool.Call(ctx, core.NBToolCallRequest{Command: command})
		require.NoError(t, err, command)
		assert.Equal(t, command, capture.command, "sent as written")
		assert.Equal(t, "k8s-prod", capture.env[workspace.ENV_NB_TOOL_CONFIG_NAME], "on the conversation's own cluster")
	}
}

// Outside the follow-up check kubectl_execute is unchanged: the strict rules do not apply.
func TestKubectlExecuteOutsideTheFollowupCheck(t *testing.T) {
	const account = "kubectl-outside-check"
	clusterFixture(t, account)
	capture := &targetWorkspaceCapture{}
	tool := KubectlExecuteTool{workspaceManager: capture}
	ctx := kubectlCallCtx(t, account, tool)
	readOnly := ctx
	readOnly.QueryConfig.ReadOnlyRun = true // an automatic investigation: read-only, not strict
	for _, command := range unsafeKubectlReads {
		assert.NoError(t, readOnlyCheckRefusal(ctx, ToolExecuteKubectlCommand, core.NBToolCallRequest{Command: command}), command)
		assert.NoError(t, readOnlyCheckRefusal(readOnly, ToolExecuteKubectlCommand, core.NBToolCallRequest{Command: command}), command)
	}
	_, err := tool.Call(readOnly, core.NBToolCallRequest{Command: "kubectl exec api-1 -n shop -- cat /etc/hostname"})
	require.NoError(t, err)
	assert.Equal(t, "kubectl exec api-1 -n shop -- cat /etc/hostname", capture.command)
}

// In the follow-up check a cloud CLI tool refuses every call, reads included (fails closed, review of #41596).
func TestCloudCliInTheFollowupCheck(t *testing.T) {
	const account = "aws-followup-check"
	awsFixture(t, account)
	capture := &targetWorkspaceCapture{}
	tool := AwsCliTool{workspaceManager: capture}
	ctx := strict(awsCtx(t, account, tool))
	for _, command := range []string{"aws ec2 describe-instances --instance-ids i-1", "aws lambda get-function-configuration --function-name f", "aws sts get-session-token"} {
		resp, err := tool.Call(ctx, core.NBToolCallRequest{Command: command})
		require.Error(t, err, command)
		assert.Contains(t, resp.Data, "read-only check", command)
	}
	require.Empty(t, capture.command, "no refused call reached the workspace")

	capture.command = ""
	normal := awsCtx(t, account, tool)
	normal.QueryConfig.ReadOnlyRun = true
	_, err := tool.Call(normal, core.NBToolCallRequest{Command: "aws ssm get-parameter --name /db/password --with-decryption"})
	require.NoError(t, err, "outside the follow-up check the tool is unchanged")
	assert.NotEmpty(t, capture.command)
}
