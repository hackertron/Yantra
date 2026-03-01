package provider

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hackertron/Yantra/internal/types"
)

// AnthropicProvider implements Provider using the Anthropic Messages API.
type AnthropicProvider struct {
	client    *anthropic.Client
	provider  types.ProviderID
	model     types.ModelID
	maxCtxTok int
}

// NewAnthropic creates an Anthropic Messages API provider.
func NewAnthropic(name, apiKey, model string, entry types.ProviderRegistryEntry) (*AnthropicProvider, error) {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if entry.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(entry.BaseURL))
	}
	client := anthropic.NewClient(opts...)

	maxCtx := entry.MaxContextTokens
	if maxCtx == 0 {
		maxCtx = 200000
	}

	return &AnthropicProvider{
		client:    &client,
		provider:  types.ProviderID(name),
		model:     types.ModelID(model),
		maxCtxTok: maxCtx,
	}, nil
}

func (p *AnthropicProvider) ProviderID() types.ProviderID { return p.provider }
func (p *AnthropicProvider) ModelID() types.ModelID       { return p.model }
func (p *AnthropicProvider) MaxContextTokens() int        { return p.maxCtxTok }

func (p *AnthropicProvider) Complete(ctx context.Context, c *types.Context) (*types.Response, error) {
	params := p.buildParams(c)
	message, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, &types.ProviderError{Provider: string(p.provider), Message: "completion failed", Err: err}
	}
	return anthropicMessageToResponse(message), nil
}

func (p *AnthropicProvider) Stream(ctx context.Context, c *types.Context) <-chan types.StreamItem {
	ch := make(chan types.StreamItem, 64)
	go func() {
		defer close(ch)

		params := p.buildParams(c)
		stream := p.client.Messages.NewStreaming(ctx, params)
		acc := anthropic.Message{}

		for stream.Next() {
			event := stream.Current()
			if err := acc.Accumulate(event); err != nil {
				ch <- types.StreamItem{Type: types.StreamError, Error: err}
				return
			}

			switch ev := event.AsAny().(type) {
			case anthropic.ContentBlockStartEvent:
				if ev.ContentBlock.Type == "tool_use" {
					if block, ok := ev.ContentBlock.AsAny().(anthropic.ToolUseBlock); ok {
						ch <- types.StreamItem{
							Type: types.StreamToolCallDelta,
							ToolCallDelta: &types.ToolCallDelta{
								Index: int(ev.Index),
								ID:    block.ID,
								Name:  block.Name,
							},
						}
					}
				}

			case anthropic.ContentBlockDeltaEvent:
				switch delta := ev.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					ch <- types.StreamItem{Type: types.StreamText, Text: delta.Text}
				case anthropic.InputJSONDelta:
					ch <- types.StreamItem{
						Type: types.StreamToolCallDelta,
						ToolCallDelta: &types.ToolCallDelta{
							Index:     int(ev.Index),
							Arguments: delta.PartialJSON,
						},
					}
				}
			}
		}

		if err := stream.Err(); err != nil {
			ch <- types.StreamItem{Type: types.StreamError, Error: err}
			return
		}

		ch <- types.StreamItem{
			Type: types.StreamDone,
			Usage: &types.Usage{
				PromptTokens:     int(acc.Usage.InputTokens),
				CompletionTokens: int(acc.Usage.OutputTokens),
				TotalTokens:      int(acc.Usage.InputTokens + acc.Usage.OutputTokens),
			},
		}
	}()
	return ch
}

func (p *AnthropicProvider) buildParams(c *types.Context) anthropic.MessageNewParams {
	msgs, systemPrompt := convertMessagesAnthropic(c.Messages)

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 8192,
		Messages:  msgs,
	}

	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: systemPrompt},
		}
	}

	if len(c.Tools) > 0 {
		params.Tools = convertToolsAnthropic(c.Tools)
	}

	return params
}

func anthropicMessageToResponse(msg *anthropic.Message) *types.Response {
	resp := &types.Response{
		Message:      types.Message{Role: types.RoleAssistant},
		FinishReason: string(msg.StopReason),
		Usage: types.Usage{
			PromptTokens:     int(msg.Usage.InputTokens),
			CompletionTokens: int(msg.Usage.OutputTokens),
			TotalTokens:      int(msg.Usage.InputTokens + msg.Usage.OutputTokens),
		},
	}

	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			resp.Message.Content += b.Text
		case anthropic.ToolUseBlock:
			argsJSON, _ := json.Marshal(b.Input)
			resp.Message.ToolCalls = append(resp.Message.ToolCalls, types.ToolCall{
				ID: b.ID,
				Function: types.FunctionCall{
					Name:      b.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	return resp
}

func convertMessagesAnthropic(msgs []types.Message) ([]anthropic.MessageParam, string) {
	var out []anthropic.MessageParam
	var systemPrompt string

	for _, m := range msgs {
		switch m.Role {
		case types.RoleSystem:
			systemPrompt = m.Content

		case types.RoleUser:
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))

		case types.RoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				var input map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: input,
					},
				})
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: blocks,
				})
			}

		case types.RoleTool:
			out = append(out, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false),
			))
		}
	}

	return out, systemPrompt
}

func convertToolsAnthropic(decls []types.FunctionDecl) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(decls))
	for _, d := range decls {
		var schema anthropic.ToolInputSchemaParam
		if len(d.Parameters) > 0 {
			_ = json.Unmarshal(d.Parameters, &schema)
		}
		out = append(out, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        d.Name,
				Description: anthropic.String(d.Description),
				InputSchema: schema,
			},
		})
	}
	return out
}

var _ types.Provider = (*AnthropicProvider)(nil)
