package tools

import (
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestStaticCLIClassifiers(t *testing.T) {
	tests := []struct {
		name  string
		infer func(string) core.ToolRequestType
		input string
		want  core.ToolRequestType
	}{
		{"helm list", inferHelmRequestType, "helm list -A", core.ToolRequestTypeRead},
		{"helm get", inferHelmRequestType, "helm get values api", core.ToolRequestTypeRead},
		{"helm install", inferHelmRequestType, "helm install api ./chart", core.ToolRequestTypeCreate},
		{"helm upgrade", inferHelmRequestType, "helm upgrade api ./chart", core.ToolRequestTypeUpdate},
		{"helm repo update", inferHelmRequestType, "helm repo update", core.ToolRequestTypeUpdate},
		{"helm repo remove", inferHelmRequestType, "helm repo remove stable", core.ToolRequestTypeDelete},
		{"helm dependency update defers", inferHelmRequestType, "helm dependency update ./chart", ""},
		{"helm dependency build defers", inferHelmRequestType, "helm dependency build ./chart", ""},
		{"helm compound defers", inferHelmRequestType, "helm list; helm uninstall api", ""},
		{"helm pipeline defers", inferHelmRequestType, "helm list | grep api", ""},
		{"helm malformed quotes defer", inferHelmRequestType, `helm get values "api`, ""},
		{"helm missing executable defers", inferHelmRequestType, "list -A", ""},
		{"helm help on mutation", inferHelmRequestType, "helm uninstall --help", core.ToolRequestTypeRead},
		{"helm release named version is still create", inferHelmRequestType, "helm install version ./chart", core.ToolRequestTypeCreate},
		{"helm version constraint is still create", inferHelmRequestType, "helm install api ./chart --version 1.2.3", core.ToolRequestTypeCreate},

		{"github issue list", inferGithubRequestType, "gh issue list", core.ToolRequestTypeRead},
		{"github repo clone", inferGithubRequestType, "gh repo clone acme/api", core.ToolRequestTypeRead},
		{"github pr create", inferGithubRequestType, "gh pr create --title fix", core.ToolRequestTypeCreate},
		{"github pr merge", inferGithubRequestType, "gh pr merge 42 --squash", core.ToolRequestTypeUpdate},
		{"github repo delete", inferGithubRequestType, "gh repo delete acme/api --yes", core.ToolRequestTypeDelete},
		{"github api defaults to read", inferGithubRequestType, "gh api repos/acme/api", core.ToolRequestTypeRead},
		{"github api patch", inferGithubRequestType, "gh api --method PATCH repos/acme/api -f archived=true", core.ToolRequestTypeUpdate},
		{"github api patch with equals", inferGithubRequestType, "gh api --method=PATCH repos/acme/api -f archived=true", core.ToolRequestTypeUpdate},
		{"github api delete with equals", inferGithubRequestType, "gh api --method=DELETE repos/acme/api/hooks/1", core.ToolRequestTypeDelete},
		{"github api field defaults to post", inferGithubRequestType, "gh api repos/acme/api/issues -f title=bug", core.ToolRequestTypeCreate},
		{"github API field with equals defaults to post", inferGithubRequestType, "gh api repos/acme/api/issues --field=title=bug", core.ToolRequestTypeCreate},
		{"gitlab API form with equals defaults to post", inferGitlabRequestType, "glab api projects/1/issues --form=title=bug", core.ToolRequestTypeCreate},
		{"github global flags defer", inferGithubRequestType, "gh --repo acme/api issue list", ""},
		{"github missing executable defers", inferGithubRequestType, "issue list", ""},

		{"gitlab mr view", inferGitlabRequestType, "glab mr view 42", core.ToolRequestTypeRead},
		{"gitlab repo clone", inferGitlabRequestType, "glab repo clone acme/api", core.ToolRequestTypeRead},
		{"gitlab ci retry", inferGitlabRequestType, "glab ci retry 42", core.ToolRequestTypeUpdate},
		{"gitlab issue delete", inferGitlabRequestType, "glab issue delete 42", core.ToolRequestTypeDelete},
		{"gitlab api defaults to read", inferGitlabRequestType, "glab api projects/1/repository/files/main.go/raw?ref=main", core.ToolRequestTypeRead},
		{"gitlab api delete", inferGitlabRequestType, "glab api --method DELETE projects/1/hooks/2", core.ToolRequestTypeDelete},

		{"argocd app list", inferArgoRequestType, "argocd app list", core.ToolRequestTypeRead},
		{"argocd app sync", inferArgoRequestType, "argocd app sync api", core.ToolRequestTypeUpdate},
		{"argocd app terminate", inferArgoRequestType, "argocd app terminate-op api", core.ToolRequestTypeDelete},

		{"redis get", inferRedisRequestType, "redis-cli GET key", core.ToolRequestTypeRead},
		{"redis set", inferRedisRequestType, "redis-cli SET key value", core.ToolRequestTypeCreate},
		{"redis expire", inferRedisRequestType, "redis-cli EXPIRE key 10", core.ToolRequestTypeUpdate},
		{"redis flush", inferRedisRequestType, "redis-cli FLUSHDB", core.ToolRequestTypeDelete},
		{"redis client kill defers", inferRedisRequestType, "redis-cli CLIENT KILL TYPE normal", ""},
		{"redis option value cannot spoof verb", inferRedisRequestType, "redis-cli -h get SET key value", ""},

		{"rabbit list", inferRabbitRequestType, "rabbitmqadmin list queues", core.ToolRequestTypeRead},
		{"rabbit declare", inferRabbitRequestType, "rabbitmqadmin declare queue name=q", core.ToolRequestTypeCreate},
		{"rabbit purge", inferRabbitRequestType, "rabbitmqadmin purge queue name=q", core.ToolRequestTypeUpdate},
		{"rabbit delete", inferRabbitRequestType, "rabbitmqadmin delete queue name=q", core.ToolRequestTypeDelete},
		{"rabbit option value cannot spoof verb", inferRabbitRequestType, "rabbitmqadmin --host list delete queue name=q", ""},
		{"rabbit JSON args", inferRabbitRequestType, `{"command":"rabbitmqadmin","args":"list queues"}`, core.ToolRequestTypeRead},
		{"rabbit API get", inferRabbitRequestType, `{"args":"rabbitmq-api GET /api/queues"}`, core.ToolRequestTypeRead},
		{"rabbit API delete", inferRabbitRequestType, `{"args":"rabbitmq-api DELETE /api/queues/%2F/q"}`, core.ToolRequestTypeDelete},
		{"rabbit quoted executable path", inferRabbitRequestType, `"/path with spaces/rabbitmq-api" DELETE /api/queues/%2F/q`, core.ToolRequestTypeDelete},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.infer(tc.input))
		})
	}
}

func inferGitlabRequestType(input string) core.ToolRequestType {
	return inferNestedCLIRequestType(input, "glab", gitlabReadActions, gitlabCreateActions, gitlabUpdateActions, gitlabDeleteActions)
}

func inferArgoRequestType(input string) core.ToolRequestType {
	return inferNestedCLIRequestType(input, "argocd", argoReadActions, argoCreateActions, argoUpdateActions, argoDeleteActions)
}

func TestNewStaticClassifiersRetainPromptFallback(t *testing.T) {
	tools := []any{
		HelmExecuteTool{}, GithubCliTool{}, GitlabCliTool{}, ArgoCDExecuteTool{}, RedisExecuteTool{}, RabbitExecuteTool{},
	}
	for _, tool := range tools {
		_, staticOK := tool.(core.ToolRequestInference)
		_, promptOK := tool.(core.ToolRequestInferencePrompt)
		assert.True(t, staticOK)
		assert.True(t, promptOK)
	}
}

func TestHelpOrVersionEmptyInput(t *testing.T) {
	assert.False(t, helpOrVersion(nil))
}
