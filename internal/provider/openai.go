package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hackertron/Yantra/internal/types"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// OpenAIProvider implements Provider using the OpenAI Chat Completions API.
type OpenAIProvider struct {
	client    *openai.Client
	provider  types.ProviderID
	model     types.ModelID
	maxCtxTok int
}

// NewOpenAI creates an OpenAI Chat Completions provider.
func NewOpenAI(name, apiKey, model string, entry types.ProviderRegistryEntry) (*OpenAIProvider, error) {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if entry.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(entry.BaseURL))
	}
	client := openai.NewClient(opts...)

	maxCtx := entry.MaxContextTokens
	if maxCtx == 0 {
		maxCtx = 128000
	}

	return &OpenAIProvider{
		client:    &client,
		provider:  types.ProviderID(name),
		model:     types.ModelID(model),
		maxCtxTok: maxCtx,
	}, nil
}

func (p *OpenAIProvider) ProviderID() types.ProviderID { return p.provider }
func (p *OpenAIProvider) ModelID() types.ModelID       { return p.model }
func (p *OpenAIProvider) MaxContextTokens() int        { return p.maxCtxTok }

func (p *OpenAIProvider) Complete(ctx context.Context, c *types.Context) (*types.Response, error) {
	params := p.buildParams(c)
	completion, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, &types.ProviderError{Provider: string(p.provider), Message: "completion failed", Err: err}
	}
	if len(completion.Choices) == 0 {
		return nil, &types.ProviderError{Provider: string(p.provider), Message: "no choices in response"}
	}

	choice := completion.Choices[0]
	return &types.Response{
		Message:      openaiMessageToYantra(choice.Message),
		FinishReason: string(choice.FinishReason),
		Usage: types.Usage{
			PromptTokens:     int(completion.Usage.PromptTokens),
			CompletionTokens: int(completion.Usage.CompletionTokens),
			TotalTokens:      int(completion.Usage.TotalTokens),
		},
	}, nil
}

func (p *OpenAIProvider) Stream(ctx context.Context, c *types.Context) <-chan types.StreamItem {
	ch := make(chan types.StreamItem, 64)
	go func() {
		defer close(ch)

		params := p.buildParams(c)
		stream := p.client.Chat.Completions.NewStreaming(ctx, params)
		acc := openai.ChatCompletionAccumulator{}

		for stream.Next() {
			chunk := stream.Current()
			acc.AddChunk(chunk)

			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta

				// Text content
				if delta.Content != "" {
					ch <- types.StreamItem{Type: types.StreamText, Text: delta.Content}
				}

				// Tool call deltas
				for _, tc := range delta.ToolCalls {
					ch <- types.StreamItem{
						Type: types.StreamToolCallDelta,
						ToolCallDelta: &types.ToolCallDelta{
							Index:     int(tc.Index),
							ID:        tc.ID,
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
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
				PromptTokens:     int(acc.Usage.PromptTokens),
				CompletionTokens: int(acc.Usage.CompletionTokens),
				TotalTokens:      int(acc.Usage.TotalTokens),
			},
		}
	}()
	return ch
}

func (p *OpenAIProvider) buildParams(c *types.Context) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(p.model),
		Messages: convertMessagesOpenAI(c.Messages),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	if len(c.Tools) > 0 {
		params.Tools = convertToolsOpenAI(c.Tools)
	}
	return params
}

func openaiMessageToYantra(msg openai.ChatCompletionMessage) types.Message {
	m := types.Message{
		Role:    types.RoleAssistant,
		Content: msg.Content,
	}
	for _, tc := range msg.ToolCalls {
		m.ToolCalls = append(m.ToolCalls, types.ToolCall{
			ID: tc.ID,
			Function: types.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return m
}

func convertMessagesOpenAI(msgs []types.Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case types.RoleSystem:
			out = append(out, openai.SystemMessage(m.Content))
		case types.RoleUser:
			out = append(out, openai.UserMessage(m.Content))
		case types.RoleAssistant:
			if len(m.ToolCalls) > 0 {
				// Build an assistant message with tool calls using the SDK's param types
				assistantMsg := openai.AssistantMessage(m.Content)
				// We need to set tool calls on the assistant param
				if assistantMsg.OfAssistant != nil {
					assistantMsg.OfAssistant.ToolCalls = buildOpenAIToolCallParams(m.ToolCalls)
				}
				out = append(out, assistantMsg)
			} else {
				out = append(out, openai.AssistantMessage(m.Content))
			}
		case types.RoleTool:
			out = append(out, openai.ToolMessage(m.ToolCallID, m.Content))
		}
	}
	return out
}

func buildOpenAIToolCallParams(tcs []types.ToolCall) []openai.ChatCompletionMessageToolCallUnionParam {
	out := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(tcs))
	for _, tc := range tcs {
		out = append(out, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID: tc.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			},
		})
	}
	return out
}

func convertToolsOpenAI(decls []types.FunctionDecl) []openai.ChatCompletionToolUnionParam {
	out := make([]openai.ChatCompletionToolUnionParam, 0, len(decls))
	for _, d := range decls {
		var params openai.FunctionParameters
		if len(d.Parameters) > 0 {
			if err := json.Unmarshal(d.Parameters, &params); err != nil {
				params = openai.FunctionParameters{"type": "object"}
			}
		} else {
			params = openai.FunctionParameters{"type": "object"}
		}
		out = append(out, openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
			Name:        d.Name,
			Description: openai.String(d.Description),
			Parameters:  params,
		}))
	}
	return out
}

// formatToolCallID generates a deterministic tool call ID for providers that don't supply one.
func formatToolCallID(name string, index int) string {
	return fmt.Sprintf("call_%s_%d", name, index)
}

var _ types.Provider = (*OpenAIProvider)(nil)
