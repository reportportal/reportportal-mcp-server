package promptreader_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/reportportal/reportportal-mcp-server/internal/promptreader"
)

func TestLoadPromptsFromYAML(t *testing.T) {
	yamlContent := []byte(`
prompts:
  - name: reportportal_analyze_launch
    description: "Analyze ReportPortal launch"
    arguments:
      - name: launch_id
        description: "ID of the ReportPortal launch to analyze"
        required: true
      - name: name
        description: "Name of the launch to analyze"
        required: true
    messages:
      - role: user
        content:
          type: text
          text: |
            Provide comprehensive analysis of test execution reported to ReportPortal as launch named '{{.name}}' with ID {{.launch_id}}.
            Focus on the following aspects:
              1. Test Execution Status: Provide a summary of the test execution status.
              2. Test Duration: Analyze the duration of the test execution.
      - role: system
        content:
          type: text
          text: "I'll provide a comprehensive analysis of the test results."
  - name: reportportal_summarize_errors
    description: "Summarize errors from a ReportPortal launch"
    arguments:
      - name: launch_id
        description: "ID of the ReportPortal launch to analyze"
        required: true
    messages:
      - role: user
        content:
          type: text
          text: "Provide a concise summary of all errors in launch {{.launch_id}}."
`)

	// Load prompts from the YAML content
	prompts, err := promptreader.LoadPromptsFromYAML(yamlContent)
	require.NoError(t, err)
	require.Len(t, prompts, 2)

	t.Run("VerifyPromptMetadata", func(t *testing.T) {
		// Check first prompt metadata
		assert.Equal(t, "reportportal_analyze_launch", prompts[0].Prompt.GetName())
		assert.Equal(t, "Analyze ReportPortal launch", prompts[0].Prompt.Description)

		// Check second prompt metadata
		assert.Equal(t, "reportportal_summarize_errors", prompts[1].Prompt.GetName())
		assert.Equal(
			t,
			"Summarize errors from a ReportPortal launch",
			prompts[1].Prompt.Description,
		)
	})

	t.Run("VerifyArgumentsCount", func(t *testing.T) {
		// First prompt should have two arguments
		assert.Len(t, prompts[0].Prompt.Arguments, 2)

		// Second prompt should have one argument
		assert.Len(t, prompts[1].Prompt.Arguments, 1)
	})

	t.Run("VerifyArgumentProperties", func(t *testing.T) {
		// Check first prompt arguments
		firstPromptArgs := prompts[0].Prompt.Arguments

		// First argument: launch_id
		assert.Equal(t, "launch_id", firstPromptArgs[0].Name)
		assert.Equal(t, "ID of the ReportPortal launch to analyze", firstPromptArgs[0].Description)
		assert.True(t, firstPromptArgs[0].Required)

		// Second argument: name
		assert.Equal(t, "name", firstPromptArgs[1].Name)
		assert.Equal(t, "Name of the launch to analyze", firstPromptArgs[1].Description)
		assert.True(t, firstPromptArgs[1].Required)
	})

	t.Run("TestTemplateRendering", func(t *testing.T) {
		// Call the handler with test arguments
		ctx := context.Background()
		promptResult, err := prompts[0].Handler(ctx, mcp.GetPromptRequest{
			Params: struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments,omitempty"`
			}{
				Name: "reportportal_analyze_launch",
				Arguments: map[string]string{
					"launch_id": "123",
					"name":      "Test Launch",
				},
			},
		})

		require.NoError(t, err)
		require.NotNil(t, promptResult)

		// Verify description
		assert.Equal(t, "Analyze ReportPortal launch", promptResult.Description)

		// Verify message count
		assert.Len(t, promptResult.Messages, 2)

		// Verify user message contains rendered template
		userMessage := promptResult.Messages[0]
		assert.Equal(t, "user", string(userMessage.Role))

		// Convert the content to a string and verify it contains expected text
		contentStr, err := json.Marshal(userMessage.Content)
		require.NoError(t, err)
		assert.Contains(t, string(contentStr), "Test Launch")
		assert.Contains(t, string(contentStr), "123")
	})

	t.Run("TestMissingArgument", func(t *testing.T) {
		// Call the handler with missing arguments
		ctx := context.Background()
		promptRes, err := prompts[0].Handler(ctx, mcp.GetPromptRequest{
			Params: struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments,omitempty"`
			}{
				Name: "reportportal_analyze_launch",
				Arguments: map[string]string{
					"launch_id": "123",
					// Missing "name"
				},
			},
		})
		assert.NoError(t, err)
		_, err = json.Marshal(promptRes)
		assert.Error(t, err)
	})
}

func TestLoadPromptsFromInvalidYAML(t *testing.T) {
	// Create invalid YAML content
	invalidYAML := []byte("invalid: yaml: content")

	_, err := promptreader.LoadPromptsFromYAML(invalidYAML)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error parsing prompts YAML")
}

func TestSerializeRetrievedPrompt(t *testing.T) {
	// Create YAML content
	yamlContent := []byte(`
prompts:
  - name: reportportal_analyze_launch
    description: "Analyze ReportPortal launch"
    arguments:
      - name: launch_id
        description: "ID of the ReportPortal launch to analyze"
        required: true
    messages:
      - role: user
        content:
          type: text
          text: "Analyze launch {{.launch_id}}"
`)

	prompts, err := promptreader.LoadPromptsFromYAML(yamlContent)
	require.NoError(t, err)
	require.Len(t, prompts, 1)

	// Get prompt result
	ctx := context.Background()
	promptResult, err := prompts[0].Handler(ctx, mcp.GetPromptRequest{
		Params: struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments,omitempty"`
		}{
			Name: "reportportal_analyze_launch",
			Arguments: map[string]string{
				"launch_id": "123",
			},
		},
	})
	require.NoError(t, err)

	// Test JSON serialization of the result
	data, err := json.Marshal(promptResult)
	require.NoError(t, err)

	var resultMap map[string]interface{}
	err = json.Unmarshal(data, &resultMap)
	require.NoError(t, err)

	// Verify structure
	assert.Equal(t, "Analyze ReportPortal launch", resultMap["description"])
	messages, ok := resultMap["messages"].([]interface{})
	require.True(t, ok)
	require.Len(t, messages, 1)

	message, ok := messages[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "user", message["role"])
}

func TestEmptyPromptsYAML(t *testing.T) {
	// Test with empty YAML
	emptyYAML := []byte(`prompts: []`)

	prompts, err := promptreader.LoadPromptsFromYAML(emptyYAML)
	require.NoError(t, err)
	assert.Empty(t, prompts)
}

func TestMalformedPrompt(t *testing.T) {
	// Test with a prompt missing required fields
	malformedYAML := []byte(`
prompts:
  - name: incomplete_prompt
    # Missing description and messages
`)

	_, err := promptreader.LoadPromptsFromYAML(malformedYAML)
	assert.Error(t, err)
}

func TestMultipleUserMessages(t *testing.T) {
	// Test with multiple user messages
	yamlContent := []byte(`
prompts:
  - name: multi_user_messages
    description: "Test multiple user messages"
    arguments:
      - name: test_arg
        description: "Test argument"
        required: true
    messages:
      - role: user
        content:
          type: text
          text: "First user message: {{.test_arg}}"
      - role: system
        content:
          type: text
          text: "System response"
      - role: user
        content:
          type: text
          text: "Second user message: {{.test_arg}}"
`)

	prompts, err := promptreader.LoadPromptsFromYAML(yamlContent)
	require.NoError(t, err)
	require.Len(t, prompts, 1)

	ctx := context.Background()
	promptResult, err := prompts[0].Handler(ctx, mcp.GetPromptRequest{
		Params: struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments,omitempty"`
		}{
			Name: "multi_user_messages",
			Arguments: map[string]string{
				"test_arg": "test value",
			},
		},
	})
	require.NoError(t, err)

	// Verify all messages were processed
	assert.Len(t, promptResult.Messages, 3)

	// For each message, check the role
	assert.Equal(t, "user", string(promptResult.Messages[0].Role))
	assert.Equal(t, "system", string(promptResult.Messages[1].Role))
	assert.Equal(t, "user", string(promptResult.Messages[2].Role))
}
