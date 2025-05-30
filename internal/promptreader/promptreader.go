package promptreader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"text/template"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"gopkg.in/yaml.v3"
)

type PromptHandlerPair struct {
	Prompt  mcp.Prompt
	Handler server.PromptHandlerFunc
}

// TemplatedTextContent represents a text content that can be templated.
type TemplatedTextContent struct {
	mcp.TextContent `json:",inline"`
	Template        *template.Template `json:"-"` // Template for the text content
	Arguments       map[string]string  `json:"-"`
}

func (ttc TemplatedTextContent) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	if err := ttc.Template.Execute(&buf, ttc.Arguments); err != nil {
		return nil, err
	}
	ttc.Text = buf.String()
	return json.Marshal(ttc.TextContent)
}

// LoadPromptsFromYAML reads prompt definitions from a YAML file and converts them
// to pairs of mcp.Prompt and server.PromptHandlerFunc
func LoadPromptsFromYAML(data []byte) ([]PromptHandlerPair, error) {
	// Parse YAML
	var promptDefs struct {
		Prompts []struct {
			mcp.Prompt `yaml:",inline"`
			Messages   []struct {
				Role    mcp.Role `yaml:"role"`
				Content struct {
					mcp.TextContent `yaml:",inline"`
				} `yaml:"content"`
			} `yaml:"messages"`
		} `yaml:"prompts"`
	}
	// Convert to mcp.Prompt and handlers
	result := make([]PromptHandlerPair, 0, len(promptDefs.Prompts))

	if err := yaml.Unmarshal(data, &promptDefs); err != nil {
		return nil, fmt.Errorf("error parsing prompts YAML: %w", err)
	}

	for _, def := range promptDefs.Prompts {
		// Validate at least one message is defined
		if len(def.Messages) == 0 {
			return nil, fmt.Errorf("prompt %s has no messages", def.GetName())
		}

		tmpls := template.New("").Option("missingkey=error")
		var err error
		for idx, msgDef := range def.Messages {
			if msgDef.Content.Type != "text" {
				return nil, fmt.Errorf(
					"prompt %s message %d has unsupported content type %s",
					def.GetName(),
					idx,
					msgDef.Content.Type,
				)
			}
			if msgDef.Content.Text == "" {
				return nil, fmt.Errorf(
					"prompt %s message %d has no text defined",
					def.GetName(),
					idx,
				)
			}
			// Parse template
			tmpls, err = tmpls.New(strconv.Itoa(idx)).Parse(msgDef.Content.Text)
			if err != nil {
				return nil, fmt.Errorf(
					"error parsing template for prompt %s: %w",
					def.GetName(),
					err,
				)
			}
		}
		// Create handler
		handler := func(ctx context.Context, rq mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			if rq.Params.Name != def.GetName() {
				return nil, fmt.Errorf("prompt %s not found", rq.Params.Name)
			}
			messages := make([]mcp.PromptMessage, 0, len(promptDefs.Prompts))

			arguments := rq.Params.Arguments
			for msgIdx, msg := range def.Messages {
				tmpl := tmpls.Lookup(strconv.Itoa(msgIdx))
				messages = append(messages, mcp.NewPromptMessage(msg.Role, TemplatedTextContent{
					TextContent: mcp.TextContent{
						Type:      msg.Content.Type,
						Annotated: msg.Content.Annotated,
					},
					Template:  tmpl,
					Arguments: arguments,
				}))
			}
			return &mcp.GetPromptResult{
				Description: def.Description,
				Messages:    messages,
			}, nil
		}

		result = append(result, PromptHandlerPair{
			Prompt:  def.Prompt,
			Handler: handler,
		})
	}

	return result, nil
}
