package knowledge

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every command the workspace image shims to a server-side tool runs for a
// skill, so knownSkillCLIs must not call it missing (helm was, while
// helm_execute ran it fine).
func TestKnownSkillCLIsMatchWorkspaceShims(t *testing.T) {
	dockerfile, err := os.ReadFile("../../code-analysis/Dockerfile")
	if err != nil {
		t.Skipf("workspace Dockerfile not available: %v", err)
	}
	shims := regexp.MustCompile(`ln -sf /usr/local/bin/shim /usr/local/bin/([A-Za-z0-9_.-]+)`).FindAllStringSubmatch(string(dockerfile), -1)
	assert.NotEmpty(t, shims, "no shim symlinks found; did the Dockerfile layout change?")
	for _, m := range shims {
		if provided, known := knownSkillCLIs[m[1]]; known {
			assert.True(t, provided, "%s is shimmed in the workspace image but knownSkillCLIs marks it missing", m[1])
		}
	}
	for _, name := range []string{"helm", "psql", "argocd"} {
		assert.True(t, knownSkillCLIs[name], name)
	}
}
