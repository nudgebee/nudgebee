package cicd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// `argocd app diff` exits 1 when the live app differs from git: a result, not a failure.
func TestArgoCDOKExitCodes(t *testing.T) {
	assert.Equal(t, []int{1}, argoCDOKExitCodes("app diff my-app"))
	assert.Equal(t, []int{1}, argoCDOKExitCodes("argocd app diff my-app --revision HEAD"))
	assert.Nil(t, argoCDOKExitCodes("app get my-app"))
	assert.Nil(t, argoCDOKExitCodes("app sync my-app"))
	assert.Nil(t, argoCDOKExitCodes("app get diff-app"))
}
