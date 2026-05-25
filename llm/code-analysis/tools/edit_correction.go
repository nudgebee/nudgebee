package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"nudgebee/code-analysis-agent/llm"

	"github.com/tmc/langchaingo/llms"
)

const (
	editCorrectionMaxCacheSize = 50
	editCorrectionTimeout      = 30 * time.Second
)

var editCorrectionSysPrompt = `You are an expert code-editing assistant specializing in debugging and correcting failed search-and-replace operations.

# Primary Goal
Your task is to analyze a failed edit attempt and provide a corrected search string that will match the text in the file precisely. The correction should be as minimal as possible, staying very close to the original, failed search string. Do NOT invent a completely new edit based on the instruction; your job is to fix the provided parameters.

Do not try to figure out if the instruction is correct. DO NOT GIVE ADVICE. Your only goal is to do your best to perform the search and replace task.

# Input Context
You will be given:
1. The high-level instruction for the original edit.
2. The exact search and replace strings that failed.
3. The error message that was produced.
4. The full content of the latest version of the source file.

# Rules for Correction
1. **Minimal Correction:** Your new search string must be a close variation of the original. Focus on fixing issues like whitespace, indentation, line endings, or small contextual differences.
2. **Explain the Fix:** Your explanation MUST state exactly why the original search failed and how your new search string resolves that specific failure.
3. **Preserve the replace String:** Do NOT modify the replace string unless the instruction explicitly requires it. Your primary focus is fixing the search string.
4. **No Changes Case:** If the change is already present in the file, set no_changes_required to true and explain why.
5. **Exactness:** The final search field must be the EXACT literal text from the file. Do not escape characters.

# Response Format
Respond with a JSON object containing:
- "search": the corrected search string (exact text from file)
- "replace": the replacement string (unchanged unless absolutely necessary)
- "no_changes_required": boolean, true if the fix is already applied
- "explanation": why the original failed and how this fixes it`

var editCorrectionUserPrompt = `# Goal of the Original Edit
<instruction>
%s
</instruction>

# Failed Attempt Details
- **Original search parameter (failed):**
<search>
%s
</search>
- **Original replace parameter:**
<replace>
%s
</replace>
- **Error Encountered:**
<error>
%s
</error>

# Full File Content
<file_content>
%s
</file_content>

# Your Task
Based on the error and the file content, provide a corrected search string that will succeed. Remember to keep your correction minimal and explain the precise reason for the failure in your explanation.

Respond with a JSON object: {"search": "...", "replace": "...", "no_changes_required": false, "explanation": "..."}`

// EditCorrectionResult represents the LLM's corrected search/replace pair.
type EditCorrectionResult struct {
	Search            string `json:"search"`
	Replace           string `json:"replace"`
	NoChangesRequired bool   `json:"no_changes_required"`
	Explanation       string `json:"explanation"`
}

// EditCorrectionService provides LLM-based self-correction for failed edits.
// Inspired by Gemini CLI's llm-edit-fixer.ts.
type EditCorrectionService struct {
	llmClient *llm.Client
	cache     map[string]*EditCorrectionResult
	mu        sync.Mutex
}

// NewEditCorrectionService creates a new EditCorrectionService.
func NewEditCorrectionService(llmClient *llm.Client) *EditCorrectionService {
	return &EditCorrectionService{
		llmClient: llmClient,
		cache:     make(map[string]*EditCorrectionResult),
	}
}

// FixFailedEdit attempts to correct a failed search/replace by asking the LLM.
// Returns nil if the LLM cannot fix it or times out.
func (s *EditCorrectionService) FixFailedEdit(
	ctx context.Context,
	instruction string,
	oldString string,
	newString string,
	errorMsg string,
	currentContent string,
) (*EditCorrectionResult, error) {
	// Check cache first
	cacheKey := s.computeCacheKey(currentContent, oldString, newString, instruction, errorMsg)
	s.mu.Lock()
	if cached, ok := s.cache[cacheKey]; ok {
		s.mu.Unlock()
		return cached, nil
	}
	s.mu.Unlock()

	// Build the user prompt
	userPrompt := fmt.Sprintf(editCorrectionUserPrompt, instruction, oldString, newString, errorMsg, currentContent)

	messages := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextPart(editCorrectionSysPrompt)},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextPart(userPrompt)},
		},
	}

	// Create a timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, editCorrectionTimeout)
	defer cancel()

	resp, err := s.llmClient.GenerateContent(timeoutCtx, messages)
	if err != nil {
		return nil, fmt.Errorf("edit correction LLM call failed: %w", err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Content == "" {
		return nil, fmt.Errorf("edit correction LLM returned empty response")
	}

	// Parse JSON response
	result, err := parseEditCorrectionResponse(resp.Choices[0].Content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse edit correction response: %w", err)
	}

	// Cache the result
	s.mu.Lock()
	if len(s.cache) >= editCorrectionMaxCacheSize {
		// Simple eviction: clear the entire cache when full
		s.cache = make(map[string]*EditCorrectionResult)
	}
	s.cache[cacheKey] = result
	s.mu.Unlock()

	return result, nil
}

func (s *EditCorrectionService) computeCacheKey(content, oldString, newString, instruction, errorMsg string) string {
	h := sha256.New()
	h.Write([]byte(content))
	h.Write([]byte(oldString))
	h.Write([]byte(newString))
	h.Write([]byte(instruction))
	h.Write([]byte(errorMsg))
	return hex.EncodeToString(h.Sum(nil))
}

// parseEditCorrectionResponse extracts the JSON from the LLM response.
func parseEditCorrectionResponse(content string) (*EditCorrectionResult, error) {
	// Try direct JSON parse first
	var result EditCorrectionResult
	if err := json.Unmarshal([]byte(content), &result); err == nil {
		return &result, nil
	}

	// Try to extract JSON from markdown code block
	jsonStart := -1
	jsonEnd := -1

	// Look for ```json ... ``` pattern
	for i := 0; i < len(content)-3; i++ {
		if content[i] == '{' && jsonStart == -1 {
			jsonStart = i
		}
	}
	if jsonStart >= 0 {
		// Find the matching closing brace
		braceCount := 0
		for i := jsonStart; i < len(content); i++ {
			if content[i] == '{' {
				braceCount++
			} else if content[i] == '}' {
				braceCount--
				if braceCount == 0 {
					jsonEnd = i + 1
					break
				}
			}
		}
	}

	if jsonStart >= 0 && jsonEnd > jsonStart {
		var result EditCorrectionResult
		if err := json.Unmarshal([]byte(content[jsonStart:jsonEnd]), &result); err == nil {
			return &result, nil
		}
	}

	return nil, fmt.Errorf("could not extract JSON from LLM response")
}
