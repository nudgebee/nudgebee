package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSameRepo(t *testing.T) {
	same := []string{"https://github.com/Acme/infra", "https://github.com/acme/infra.git", "git@github.com:acme/infra.git", "github.com/acme/infra/", "acme/infra"}
	for _, a := range same {
		for _, b := range same {
			assert.True(t, SameRepo(a, b), "%s ~ %s", a, b)
		}
	}
	assert.True(t, SameRepo("https://gitlab.example.com/group/sub/repo", "group/sub/repo"), "a GitLab subgroup path")
	assert.False(t, SameRepo("https://gitlab.example.com/group/sub/repo", "sub/repo"))
	assert.False(t, SameRepo("https://github.com/acme/infra", "https://gitlab.com/acme/infra"), "another host")
	assert.False(t, SameRepo("acme/infra", "acme/web"))
	assert.False(t, SameRepo("deploy/kubernetes/app/values-dev.yaml", "acme/infra"))
	assert.False(t, SameRepo("sample-app", "sample-app"), "not a repository reference")
}
