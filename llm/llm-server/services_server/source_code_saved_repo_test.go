package services_server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A repository a person saved on the workload fills only what the cluster's annotations leave unset.
func TestWithSavedAttributes(t *testing.T) {
	saved := map[string]string{AnnotationGitRepo: "https://github.com/acme/saved", AnnotationCIGitRepo: "https://github.com/acme/infra"}
	got := withSavedAttributes(map[string]string{AnnotationGitRepo: "https://github.com/acme/annotated", AnnotationGitHash: ""}, saved)
	assert.Equal(t, "https://github.com/acme/annotated", got[AnnotationGitRepo], "an annotation wins")
	assert.Equal(t, "https://github.com/acme/infra", got[AnnotationCIGitRepo], "a saved value fills a gap")
	assert.Equal(t, map[string]string{AnnotationGitRepo: "https://github.com/acme/saved", AnnotationCIGitRepo: "https://github.com/acme/infra"},
		withSavedAttributes(nil, saved))
	assert.Equal(t, map[string]string{}, withSavedAttributes(nil, nil))
}

// No read is made without a valid resource id.
func TestSavedCodeAttributesWithoutIds(t *testing.T) {
	got, err := savedCodeAttributes(nil, []string{"", "not-a-uuid"})
	assert.NoError(t, err)
	assert.Nil(t, got)
}
