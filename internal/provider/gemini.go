package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hackertron/Yantra/internal/types"
	"google.golang.org/genai"
)

// GeminiProvider implements Provider using the Google GenAI SDK.
type GeminiProvider struct {
	client    *genai.Client
	provider  types.ProviderID
	model     types.ModelID
	maxCtxTok int
}

// NewGemini creates a Google Gemini provider.
func NewGemini(name, apiKey, model string, entry types.ProviderRegistryEntry) (*GeminiProvider, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("creating genai client: %w", err)
	}

	maxCtx := entry.MaxContextTokens
	if maxCtx == 0 {
		maxCtx = 1000000
	}

	return &GeminiProvider{
		client:    client,
		provider:  types.ProviderID(name),
		model:     types.ModelID(model),
		maxCtxTok: maxCtx,
	}, nil
}

func (p *GeminiProvider) ProviderID() types.ProviderID { return p.provider }
func (p *GeminiProvider) ModelID() types.ModelID       { return p.model }
func (p *GeminiProvider) MaxContextTokens() int        { return p.maxCtxTok }

func (p *GeminiProvider) Complete(ctx context.Context, c *types.Context) (*types.Response, error) {
	contents, config := p.buildRequest(c)
	result, err := p.client.Models.GenerateContent(ctx, string(p.model), contents, config)
	if err != nil {
		return nil, &types.ProviderError{Provider: string(p.provider), Message: "generation failed", Err: err}
	}
	return geminiResultToResponse(result), nil
}

func (p *GeminiProvider) Stream(ctx context.Context, c *types.Context) <-chan types.StreamItem {
	ch := make(chan types.StreamItem, 64)
	go func() {
		defer close(ch)

		contents, config := p.buildRequest(c)
		var totalUsage types.Usage
		var toolCallIndex int

		for result, err := range p.client.Models.GenerateContentStream(ctx, string(p.model), contents, config) {
			if err != nil {
				ch <- types.StreamItem{Type: types.StreamError, Error: err}
				return
			}

			if text := result.Text(); text != "" {
				ch <- types.StreamItem{Type: types.StreamText, Text: text}
			}

			for _, fc := range result.FunctionCalls() {
				argsJSON, _ := json.Marshal(fc.Args)
				ch <- types.StreamItem{
					Type: types.StreamToolCallDelta,
					ToolCallDelta: &types.ToolCallDelta{
						Index:     toolCallIndex,
						ID:        formatToolCallID(fc.Name, toolCallIndex),
						Name:      fc.Name,
						Arguments: string(argsJSON),
					},
				}
				toolCallIndex++
			}

			if result.UsageMetadata != nil {
				totalUsage = types.Usage{
					PromptTokens:     int(result.UsageMetadata.PromptTokenCount),
					CompletionTokens: int(result.UsageMetadata.CandidatesTokenCount),
					TotalTokens:      int(result.UsageMetadata.TotalTokenCount),
				}
			}
		}

		ch <- types.StreamItem{Type: types.StreamDone, Usage: &totalUsage}
	}()
	return ch
}

func (p *GeminiProvider) buildRequest(c *types.Context) ([]*genai.Content, *genai.GenerateContentConfig) {
	contents, systemInstruction := convertMessagesGemini(c.Messages)
	config := &genai.GenerateContentConfig{}

	if systemInstruction != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: systemInstruction}},
		}
	}

	if len(c.Tools) > 0 {
		config.Tools = convertToolsGemini(c.Tools)
	}

	return contents, config
}

func geminiResultToResponse(result *genai.GenerateContentResponse) *types.Response {
	resp := &types.Response{
		Message: types.Message{
			Role:    types.RoleAssistant,
			Content: result.Text(),
		},
	}

	for i, fc := range result.FunctionCalls() {
		argsJSON, _ := json.Marshal(fc.Args)
		resp.Message.ToolCalls = append(resp.Message.ToolCalls, types.ToolCall{
			ID: formatToolCallID(fc.Name, i),
			Function: types.FunctionCall{
				Name:      fc.Name,
				Arguments: string(argsJSON),
			},
		})
	}

	if result.UsageMetadata != nil {
		resp.Usage = types.Usage{
			PromptTokens:     int(result.UsageMetadata.PromptTokenCount),
			CompletionTokens: int(result.UsageMetadata.CandidatesTokenCount),
			TotalTokens:      int(result.UsageMetadata.TotalTokenCount),
		}
	}

	if len(result.Candidates) > 0 {
		resp.FinishReason = string(result.Candidates[0].FinishReason)
	}

	return resp
}

func convertMessagesGemini(msgs []types.Message) ([]*genai.Content, string) {
	var contents []*genai.Content
	var systemPrompt string

	for _, m := range msgs {
		switch m.Role {
		case types.RoleSystem:
			systemPrompt = m.Content

		case types.RoleUser:
			contents = append(contents, &genai.Content{
				Role:  "user",
				Parts: []*genai.Part{{Text: m.Content}},
			})

		case types.RoleAssistant:
			var parts []*genai.Part
			if m.Content != "" {
				parts = append(parts, &genai.Part{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						Name: tc.Function.Name,
						Args: args,
					},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{Role: "model", Parts: parts})
			}

		case types.RoleTool:
			var result map[string]any
			if err := json.Unmarshal([]byte(m.Content), &result); err != nil {
				result = map[string]any{"result": m.Content}
			}
			contents = append(contents, &genai.Content{
				Role: "user",
				Parts: []*genai.Part{{
					FunctionResponse: &genai.FunctionResponse{
						Name:     m.ToolCallID,
						Response: result,
					},
				}},
			})
		}
	}

	return contents, systemPrompt
}

func convertToolsGemini(decls []types.FunctionDecl) []*genai.Tool {
	funcDecls := make([]*genai.FunctionDeclaration, 0, len(decls))
	for _, d := range decls {
		fd := &genai.FunctionDeclaration{
			Name:        d.Name,
			Description: d.Description,
		}
		if len(d.Parameters) > 0 {
			fd.Parameters = jsonSchemaToGeminiSchema(d.Parameters)
		}
		funcDecls = append(funcDecls, fd)
	}
	return []*genai.Tool{{FunctionDeclarations: funcDecls}}
}

func jsonSchemaToGeminiSchema(raw json.RawMessage) *genai.Schema {
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return &genai.Schema{Type: genai.TypeObject}
	}

	gs := &genai.Schema{
		Type:     genai.TypeObject,
		Required: schema.Required,
	}

	if len(schema.Properties) > 0 {
		gs.Properties = make(map[string]*genai.Schema)
		for name, propRaw := range schema.Properties {
			gs.Properties[name] = jsonPropToGeminiSchema(propRaw)
		}
	}

	return gs
}

func jsonPropToGeminiSchema(raw json.RawMessage) *genai.Schema {
	var prop struct {
		Type        string   `json:"type"`
		Description string   `json:"description"`
		Enum        []string `json:"enum,omitempty"`
	}
	if err := json.Unmarshal(raw, &prop); err != nil {
		return &genai.Schema{Type: genai.TypeString}
	}

	gs := &genai.Schema{Description: prop.Description, Enum: prop.Enum}
	switch prop.Type {
	case "string":
		gs.Type = genai.TypeString
	case "integer":
		gs.Type = genai.TypeInteger
	case "number":
		gs.Type = genai.TypeNumber
	case "boolean":
		gs.Type = genai.TypeBoolean
	case "array":
		gs.Type = genai.TypeArray
	default:
		gs.Type = genai.TypeString
	}
	return gs
}

var _ types.Provider = (*GeminiProvider)(nil)
