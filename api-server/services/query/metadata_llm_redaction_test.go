package query_test

import (
	"os"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
)

// The integrations list redacts LLM secrets with a literal LIKE list in SQL; a
// secret prefix missing from it would be returned in plaintext.
func TestIntegrationListRedactsEveryLLMSecretPrefix(t *testing.T) {
	src, err := os.ReadFile("metadata.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range core.LLMSecretFieldPrefixes() {
		assert.Contains(t, string(src), "icv.name LIKE '"+p+"%'", "redaction list must cover %s", p)
	}
}
