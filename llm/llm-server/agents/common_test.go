package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_updateMarkDown(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{
			input:    "This is a test string.",
			expected: "<p>This is a test string.</p>",
		},
		{
			input:    "This is a test string with\n\na new line.",
			expected: "<p>This is a test string with</p><p>a new line.</p>",
		},
		{
			input:    "This is a test string with\n\na new line and `some code`.",
			expected: "<p>This is a test string with</p><p>a new line and <code>some code</code>.</p>",
		},
		{
			input:    "This is a test string with\n\na new line and ```some code block```.",
			expected: "<p>This is a test string with</p><p>a new line and <pre>some code block</pre></p>",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			output := updateMarkDown(tc.input)
			assert.Equal(t, tc.expected, output)
		})
	}
}

// TestRemoveXMLTags tests the XML tag removal functionality
func TestRemoveXMLTags(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "Simple XML tag with content",
			input: `<thinking>This is some reasoning</thinking>

### Event Summary
- Title: Pod OOMKilled`,
			expected: `### Event Summary
- Title: Pod OOMKilled`,
		},
		{
			name: "Multiple XML tags",
			input: `<thinking>Analyzing the event...</thinking>

### Event Summary
- Title: Pod OOMKilled

<analysis>Memory exceeded</analysis>

### Root Cause
Memory limit reached`,
			expected: `### Event Summary
- Title: Pod OOMKilled

### Root Cause
Memory limit reached`,
		},
		{
			name: "Nested XML tags",
			input: `<outer>
<inner>Nested content</inner>
Some text
</outer>

### Event Summary
- Title: Test`,
			expected: `### Event Summary
- Title: Test`,
		},
		{
			name: "XML tags with attributes",
			input: `<thinking type="reasoning" level="deep">Analysis here</thinking>

### Summary
Content here`,
			expected: `### Summary
Content here`,
		},
		{
			name: "XML tags spanning multiple lines",
			input: `<thinking>
Line 1 of reasoning
Line 2 of reasoning
Line 3 of reasoning
</thinking>

### Event Details
- Info: Test`,
			expected: `### Event Details
- Info: Test`,
		},
		{
			name:     "No XML tags - pure markdown",
			input:    `### Event Summary\n- Title: Test\n- ID: 123`,
			expected: `### Event Summary\n- Title: Test\n- ID: 123`,
		},
		{
			name:     "Empty input",
			input:    "",
			expected: "",
		},
		{
			name: "XML tags at beginning and end",
			input: `<preamble>Start thinking</preamble>

### Event Summary
- Title: Test Event

<conclusion>End thinking</conclusion>`,
			expected: `### Event Summary
- Title: Test Event`,
		},
		{
			name: "Multiple consecutive XML tags",
			input: `<thinking>First</thinking>
<analysis>Second</analysis>
<scratchpad>Third</scratchpad>

### Content
Real content here`,
			expected: `### Content
Real content here`,
		},
		{
			name: "XML tags mixed with markdown code blocks",
			input: `<thinking>Analyzing code</thinking>

### Code Example
` + "```go\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```" + `

<analysis>Code review</analysis>`,
			expected: `### Code Example
` + "```go\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```",
		},
		{
			name: "Real-world LLM output scenario",
			input: `<thinking>
This appears to be an OOMKilled event. The pod was terminated due to exceeding memory limits.
Let me analyze the evidence:
- Memory usage spiked to 2.5GiB
- Limit was set to 2GiB
- Pod restarted automatically
</thinking>

### 📝 Event Summary
- **Title:** Pod OOMKilled in production
- **Event Id:** event-abc-123
- **Resource:** pod/frontend-app in namespace production
- **Time:** 2024-01-15 10:30:00

### 🔍 Key Findings & Analysis
- Pod memory usage exceeded the configured limit of 2GiB
- Memory consumption reached 2.5GiB before termination
- This is the 3rd occurrence in the last 24 hours

<analysis>
The root cause is likely a memory leak or insufficient resource allocation.
Need to investigate application logs for memory consumption patterns.
</analysis>

### 🎯 Suspected Root Cause
Memory leak in the application or insufficient memory allocation for the workload.

### 💡 Recommendations & Next Steps
1. **Immediate Fix:** Increase memory limit to 3GiB
2. **Long-term Solution:** Profile application for memory leaks
3. **Further Investigation:** Review application logs for memory patterns`,
			expected: `### 📝 Event Summary
- **Title:** Pod OOMKilled in production
- **Event Id:** event-abc-123
- **Resource:** pod/frontend-app in namespace production
- **Time:** 2024-01-15 10:30:00

### 🔍 Key Findings & Analysis
- Pod memory usage exceeded the configured limit of 2GiB
- Memory consumption reached 2.5GiB before termination
- This is the 3rd occurrence in the last 24 hours

### 🎯 Suspected Root Cause
Memory leak in the application or insufficient memory allocation for the workload.

### 💡 Recommendations & Next Steps
1. **Immediate Fix:** Increase memory limit to 3GiB
2. **Long-term Solution:** Profile application for memory leaks
3. **Further Investigation:** Review application logs for memory patterns`,
		},
		{
			name: "XML tags with special characters in content",
			input: `<thinking>
Special chars: & < > " '
And some unicode: 🔍 📝
</thinking>

### Summary
Clean content`,
			expected: `### Summary
Clean content`,
		},
		{
			name: "Unclosed XML tag - should not match",
			input: `<thinking>
This tag is not closed

### Summary
Content here`,
			expected: `<thinking>
This tag is not closed

### Summary
Content here`,
		},
		{
			name: "Mismatched XML tags - should not match",
			input: `<thinking>
Content here
</analysis>

### Summary
More content`,
			expected: `<thinking>
Content here
</analysis>

### Summary
More content`,
		},
		{
			name: "Custom/random XML tag names",
			input: `<custom_tag>Custom content</custom_tag>

### Summary
Real content

<random-tag>Random content</random-tag>

More content`,
			expected: `### Summary
Real content

More content`,
		},
		{
			name: "XML tags with numbers and hyphens",
			input: `<tag-123>Content with numbers</tag-123>

### Event
Details here

<step_1>First step</step_1>`,
			expected: `### Event
Details here`,
		},
		{
			name: "Unknown LLM reasoning tags",
			input: `<brainstorm>Brainstorming ideas...</brainstorm>

### Summary
- Point 1
- Point 2

<hypothesis>Testing hypothesis</hypothesis>

### Conclusion
Final thoughts`,
			expected: `### Summary
- Point 1
- Point 2

### Conclusion
Final thoughts`,
		},
		{
			name: "Mixed known and unknown tags",
			input: `<thinking>Initial thoughts</thinking>

### Title
Content

<my_custom_tag>My content</my_custom_tag>

More markdown

<analysis>Deep dive</analysis>`,
			expected: `### Title
Content

More markdown`,
		},
		{
			name: "Self-closing XML tags",
			input: `<tag/>

### Summary
Content here

<br/>
<img src="test"/>

More content`,
			expected: `<tag/>

### Summary
Content here

<br/>
<img src="test"/>

More content`,
		},
		{
			name: "Stray opening tags without closing",
			input: `<thinking>

### Summary
Content

<analysis>

More content`,
			expected: `<thinking>

### Summary
Content

<analysis>

More content`,
		},
		{
			name: "Stray closing tags without opening",
			input: `### Summary
Content

</thinking>

More content

</analysis>`,
			expected: `### Summary
Content

</thinking>

More content

</analysis>`,
		},
		{
			name: "Mix of paired and unpaired tags",
			input: `<thinking>Paired content</thinking>

### Summary
Real content

<stray_tag>

<analysis>More paired</analysis>`,
			expected: `### Summary
Real content

<stray_tag>`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := RemoveXMLTags(tc.input)
			assert.Equal(t, tc.expected, result, "Failed test case: %s", tc.name)
		})
	}
}

// TestRemoveXMLTagsPerformance tests the performance of XML removal with large content
func TestRemoveXMLTagsPerformance(t *testing.T) {
	// Create a large input with multiple XML tags
	largeInput := `<thinking>
` + string(make([]byte, 10000)) + `
</thinking>

### Event Summary
- Title: Large Event
- Description: ` + string(make([]byte, 5000)) + `

<analysis>
` + string(make([]byte, 10000)) + `
</analysis>

### Details
More content here`

	// Run the function and ensure it completes without hanging
	result := RemoveXMLTags(largeInput)

	// Verify XML tags were removed
	assert.NotContains(t, result, "<thinking>")
	assert.NotContains(t, result, "</thinking>")
	assert.NotContains(t, result, "<analysis>")
	assert.NotContains(t, result, "</analysis>")
	assert.Contains(t, result, "### Event Summary")
	assert.Contains(t, result, "### Details")
}
