package promptreader

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"text/template"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

type PromptHandlerPair struct {
	Prompt  *mcp.Prompt
	Handler mcp.PromptHandler
}

// ReadPrompts reads prompt definitions from a YAML file and converts them
// to pairs of mcp.Prompt and mcp.PromptHandler. It delegates to LoadPromptsFromYAML.
func ReadPrompts(data []byte) ([]PromptHandlerPair, error) {
	return LoadPromptsFromYAML(data)
}

// LoadPromptsFromYAML reads prompt definitions from a YAML file and converts them
// to pairs of mcp.Prompt and mcp.PromptHandler.
func LoadPromptsFromYAML(data []byte) ([]PromptHandlerPair, error) {
	// Parse YAML
	var promptDefs struct {
		Prompts []struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
			Arguments   []struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
				Required    bool   `yaml:"required"`
			} `yaml:"arguments"`
			Messages []struct {
				Role    string `yaml:"role"`
				Content struct {
					Type string `yaml:"type"`
					Text string `yaml:"text"`
				} `yaml:"content"`
			} `yaml:"messages"`
		} `yaml:"prompts"`
	}

	if err := yaml.Unmarshal(data, &promptDefs); err != nil {
		return nil, fmt.Errorf("error parsing prompts YAML: %w", err)
	}

	// Convert to mcp.Prompt and handlers
	result := make([]PromptHandlerPair, 0, len(promptDefs.Prompts))

	for _, def := range promptDefs.Prompts {
		// Validate at least one message is defined
		if len(def.Messages) == 0 {
			return nil, fmt.Errorf("prompt %s has no messages", def.Name)
		}

		tmpls := template.New("").Option("missingkey=error")
		var err error
		for idx, msgDef := range def.Messages {
			if msgDef.Content.Type != "text" {
				return nil, fmt.Errorf(
					"prompt %s message %d has unsupported content type %s",
					def.Name,
					idx,
					msgDef.Content.Type,
				)
			}
			if msgDef.Content.Text == "" {
				return nil, fmt.Errorf(
					"prompt %s message %d has no text defined",
					def.Name,
					idx,
				)
			}
			// Parse template
			tmpls, err = tmpls.New(strconv.Itoa(idx)).Parse(msgDef.Content.Text)
			if err != nil {
				return nil, fmt.Errorf(
					"error parsing template for prompt %s: %w",
					def.Name,
					err,
				)
			}
		}

		// Create prompt definition
		prompt := &mcp.Prompt{
			Name:        def.Name,
			Description: def.Description,
		}

		// Add arguments if any
		for _, arg := range def.Arguments {
			prompt.Arguments = append(prompt.Arguments, &mcp.PromptArgument{
				Name:        arg.Name,
				Description: arg.Description,
				Required:    arg.Required,
			})
		}

		// Create handler (capture def and tmpls by value)
		defCopy := def
		tmplsCopy := tmpls
		handler := func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			if req.Params.Name != defCopy.Name {
				return nil, fmt.Errorf("prompt %s not found", req.Params.Name)
			}
			messages := make([]*mcp.PromptMessage, 0, len(defCopy.Messages))

			for msgIdx, msg := range defCopy.Messages {
				tmpl := tmplsCopy.Lookup(strconv.Itoa(msgIdx))
				if tmpl == nil {
					return nil, fmt.Errorf(
						"template %d not found for prompt %s",
						msgIdx,
						defCopy.Name,
					)
				}
				var buf bytes.Buffer
				if err := tmpl.Execute(&buf, req.Params.Arguments); err != nil {
					return nil, fmt.Errorf("error executing template: %w", err)
				}

				if msg.Role != "user" && msg.Role != "assistant" {
					return nil, fmt.Errorf(
						"invalid role %q in prompt %s message %d: must be \"user\" or \"assistant\"",
						msg.Role,
						defCopy.Name,
						msgIdx,
					)
				}
				role := mcp.Role(msg.Role)
				messages = append(messages, &mcp.PromptMessage{
					Role: role,
					Content: &mcp.TextContent{
						Text: buf.String(),
					},
				})
			}
			return &mcp.GetPromptResult{
				Description: defCopy.Description,
				Messages:    messages,
			}, nil
		}

		result = append(result, PromptHandlerPair{
			Prompt:  prompt,
			Handler: handler,
		})
	}

	return result, nil
}
