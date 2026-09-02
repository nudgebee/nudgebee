package prompts

import "context"

// GetPromptForTest resolves a prompt against the embedded FS with no DB, for the
// content-pinning tests that moved here from prompts_repo.
func GetPromptForTest(name string, args ...any) string {
	SetGlobalLoaderForTesting(NewLoaderForTesting())
	return GetPrompt(context.Background(), name, "", args...)
}
