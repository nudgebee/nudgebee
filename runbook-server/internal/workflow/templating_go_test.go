package workflow

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTemplateContext_Secrets(t *testing.T) {
	t.Run("should correctly template secrets from environment variables", func(t *testing.T) {
		// Set a dummy secret environment variable
		err := os.Setenv("SECRET_MY_API_KEY", "supersecretkey123")
		assert.NoError(t, err)
		defer func() {
			err := os.Unsetenv("SECRET_MY_API_KEY")
			assert.NoError(t, err)
		}()

		ctx := NewTemplateContextWithEngine(nil, nil, "go") // No inputs needed for this test

		templateString := "My API Key is: {{.Secrets.MY_API_KEY}}"
		renderedString, err := ctx.Render(templateString)

		assert.NoError(t, err)
		assert.Equal(t, "My API Key is: supersecretkey123", renderedString)
	})

	t.Run("should not template non-existent secrets", func(t *testing.T) {
		ctx := NewTemplateContextWithEngine(nil, nil, "go")

		templateString := "Non-existent secret: {{.Secrets.NON_EXISTENT_SECRET}}"
		renderedString, err := ctx.Render(templateString)

		assert.NoError(t, err)
		assert.Equal(t, "Non-existent secret: <no value>", renderedString)
	})

	t.Run("should not template non-SECRET_ prefixed environment variables", func(t *testing.T) {
		err := os.Setenv("NON_SECRET_VAR", "somevalue")
		assert.NoError(t, err)
		defer func() {
			err := os.Unsetenv("NON_SECRET_VAR")
			assert.NoError(t, err)
		}()

		ctx := NewTemplateContextWithEngine(nil, nil, "go")

		templateString := "Non-secret var: {{.Secrets.NON_SECRET_VAR}}"
		renderedString, err := ctx.Render(templateString)

		assert.NoError(t, err)
		assert.Equal(t, "Non-secret var: <no value>", renderedString)
	})
}
