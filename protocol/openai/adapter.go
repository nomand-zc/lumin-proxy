// Package openai 实现了 OpenAI 兼容协议的适配器。
// 支持 /v1/chat/completions 接口的标准请求和 SSE 流式响应。
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/nomand-zc/lumin-client/providers"
	"github.com/nomand-zc/lumin-client/queue"
	"github.com/nomand-zc/lumin-proxy/errs"
	"github.com/nomand-zc/lumin-proxy/protocol"
)

func init() {
	protocol.RegisterAdapter(&Adapter{})
}

// Adapter 是 OpenAI 协议的适配器实现。
type Adapter struct{}

// Name 返回协议名称。
func (a *Adapter) Name() string {
	return "openai"
}

// Routes 返回 OpenAI 协议需要注册的路由列表。
func (a *Adapter) Routes(defaultHandler http.Handler) []protocol.Route {
	return []protocol.Route{
		{
			Pattern: "/chat/completions",
			Handler: defaultHandler,
		},
		{
			Pattern: "/models",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"object":"list","data":[]}`))
			}),
		},
	}
}

// ParseRequest 将 OpenAI 格式的 HTTP 请求解析为统一的内部请求。
func (a *Adapter) ParseRequest(r *http.Request) (*protocol.Request, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInvalidRequest, "读取请求体失败", err)
	}
	defer r.Body.Close()

	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, errs.Wrap(errs.CodeInvalidRequest, "解析请求体失败", err)
	}

	if req.Model == "" {
		return nil, errs.New(errs.CodeInvalidRequest, "model 字段不能为空")
	}

	// 转换消息
	messages, err := convertMessages(req.Messages)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInvalidRequest, "转换消息失败", err)
	}

	// 转换为 lumin-client 的 providers.Request
	provReq := providers.Request{
		Model:    req.Model,
		Messages: messages,
		GenerationConfig: providers.GenerationConfig{
			MaxTokens:        req.MaxTokens,
			Temperature:      req.Temperature,
			TopP:             req.TopP,
			Stream:           req.Stream,
			Stop:             req.Stop,
			PresencePenalty:   req.PresencePenalty,
			FrequencyPenalty:  req.FrequencyPenalty,
			ReasoningEffort:   req.ReasoningEffort,
			ThinkingEnabled:   req.ThinkingEnabled,
			ThinkingTokens:    req.ThinkingTokens,
		},
		Tools: convertTools(req.Tools),
	}

	metadata := make(map[string]any)
	if req.ToolChoice != nil {
		metadata["tool_choice"] = req.ToolChoice
	}
	if req.ResponseFormat != nil {
		metadata["response_format"] = req.ResponseFormat
	}
	if req.User != "" {
		metadata["user"] = req.User
	}

	// 将 metadata 也传递给 providerRequest
	provReq.Metadata = metadata

	return &protocol.Request{
		Model:           req.Model,
		Stream:          req.Stream,
		ProviderRequest: &provReq,
		RawBody:         body,
		Metadata:        metadata,
	}, nil
}

// WriteResponse 写入非流式响应（转换为 OpenAI Chat Completion 格式）。
// 参考 trpc-agent-go/model/openai 中 handleNonStreamingResponse 的反向转换逻辑。
func (a *Adapter) WriteResponse(ctx context.Context, w http.ResponseWriter, resp *providers.Response) error {
	w.Header().Set("Content-Type", "application/json")

	if resp.Error != nil {
		w.WriteHeader(http.StatusBadRequest)
		return json.NewEncoder(w).Encode(ErrorResponse{
			Error: ErrorDetail{
				Message: resp.Error.Message,
				Type:    resp.Error.Type,
				Param:   resp.Error.Param,
				Code:    resp.Error.Code,
			},
		})
	}

	oaiResp := buildChatCompletionResponse(resp)
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(oaiResp)
}

// WriteStreamResponse 写入 SSE 流式响应（转换为 OpenAI Chat Completion Chunk 格式）。
// 参考 trpc-agent-go/model/openai 中 createPartialResponse 的反向转换逻辑。
func (a *Adapter) WriteStreamResponse(ctx context.Context, w http.ResponseWriter, stream queue.Consumer[*providers.Response]) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errs.New(errs.CodeInternal, "ResponseWriter 不支持 Flush")
	}

	// 设置 SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// 逐 chunk 写入
	err := stream.Each(ctx, func(resp *providers.Response) error {
		if resp.Error != nil {
			// 写入错误事件（OpenAI 格式）
			errResp := ErrorResponse{
				Error: ErrorDetail{
					Message: resp.Error.Message,
					Type:    resp.Error.Type,
					Param:   resp.Error.Param,
					Code:    resp.Error.Code,
				},
			}
			data, _ := json.Marshal(errResp)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			return nil
		}

		chunkResp := buildChatCompletionChunkResponse(resp)
		data, err := json.Marshal(chunkResp)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		return nil
	})

	// 写入 [DONE] 标记
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	return err
}

// WriteError 写入错误响应（OpenAI 格式）。
func (a *Adapter) WriteError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")

	pe, ok := errs.IsProxyError(err)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error: ErrorDetail{
				Message: err.Error(),
				Type:    "internal_error",
			},
		})
		return
	}

	w.WriteHeader(pe.Code.HTTPStatus())
	errType := "invalid_request_error"
	switch pe.Code {
	case errs.CodeUnauthorized:
		errType = "authentication_error"
	case errs.CodeForbidden:
		errType = "permission_error"
	case errs.CodeRateLimited:
		errType = "rate_limit_error"
	case errs.CodeUpstreamError, errs.CodeNoAvailableAccount:
		errType = "server_error"
	case errs.CodeInternal:
		errType = "internal_error"
	}
	json.NewEncoder(w).Encode(ErrorResponse{
		Error: ErrorDetail{
			Message: pe.Message,
			Type:    errType,
		},
	})
}

// --- 响应转换辅助函数 ---

// buildChatCompletionResponse 将 providers.Response 转换为 OpenAI Chat Completion 格式的响应。
// 参考 trpc-agent-go handleNonStreamingResponse 中的 response → model.Response 构建逻辑（反向）。
func buildChatCompletionResponse(resp *providers.Response) *ChatCompletionResponse {
	oaiResp := &ChatCompletionResponse{
		ID:                resp.ID,
		Object:            providers.ObjectChatCompletion,
		Created:           resp.Created,
		Model:             resp.Model,
		SystemFingerprint: resp.SystemFingerprint,
	}

	// 转换 choices
	if len(resp.Choices) > 0 {
		oaiResp.Choices = make([]ChatCompletionChoice, len(resp.Choices))
		for i, choice := range resp.Choices {
			msg := choice.Message
			oaiChoice := ChatCompletionChoice{
				Index:        choice.Index,
				FinishReason: choice.FinishReason,
				Message: ChatCompletionMessage{
					Role: string(msg.Role),
				},
			}

			// content: 使用指针以区分 null 和空字符串
			if msg.Content != "" {
				oaiChoice.Message.Content = &msg.Content
			} else if len(msg.ToolCalls) == 0 {
				// 没有工具调用时，content 为 null
				oaiChoice.Message.Content = nil
			}

			// reasoning_content（DeepSeek / o1 等模型的思考内容）
			if msg.ReasoningContent != "" {
				oaiChoice.Message.ReasoningContent = &msg.ReasoningContent
			}

			// tool_calls
			if len(msg.ToolCalls) > 0 {
				oaiChoice.Message.ToolCalls = make([]ChatToolCall, len(msg.ToolCalls))
				for j, tc := range msg.ToolCalls {
					oaiChoice.Message.ToolCalls[j] = ChatToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: ChatToolCallFunc{
							Name:      tc.Function.Name,
							Arguments: string(tc.Function.Arguments),
						},
					}
				}
			}

			// 确保 role 有值
			if oaiChoice.Message.Role == "" {
				oaiChoice.Message.Role = "assistant"
			}

			oaiResp.Choices[i] = oaiChoice
		}
	}

	// 转换 usage
	if resp.Usage != nil {
		oaiResp.Usage = convertUsageToOpenAI(resp.Usage)
	}

	return oaiResp
}

// buildChatCompletionChunkResponse 将 providers.Response 转换为 OpenAI Chat Completion Chunk 格式。
// 参考 trpc-agent-go createPartialResponse 中的 chunk → model.Response 构建逻辑（反向）。
func buildChatCompletionChunkResponse(resp *providers.Response) *ChatCompletionChunkResponse {
	// 确定 object 类型
	object := providers.ObjectChatCompletionChunk
	if resp.Object != "" {
		object = resp.Object
	}

	chunkResp := &ChatCompletionChunkResponse{
		ID:                resp.ID,
		Object:            object,
		Created:           resp.Created,
		Model:             resp.Model,
		SystemFingerprint: resp.SystemFingerprint,
	}

	// 转换 choices
	if len(resp.Choices) > 0 {
		chunkResp.Choices = make([]ChatCompletionChunkChoice, len(resp.Choices))
		for i, choice := range resp.Choices {
			delta := choice.Delta
			oaiChoice := ChatCompletionChunkChoice{
				Index:        choice.Index,
				FinishReason: choice.FinishReason,
				Delta: ChatCompletionDelta{
					Role: string(delta.Role),
				},
			}

			// content: 仅在有内容时设置
			if delta.Content != "" {
				oaiChoice.Delta.Content = &delta.Content
			}

			// reasoning_content
			if delta.ReasoningContent != "" {
				oaiChoice.Delta.ReasoningContent = &delta.ReasoningContent
			}

			// tool_calls（流式增量）
			if len(delta.ToolCalls) > 0 {
				oaiChoice.Delta.ToolCalls = make([]ChatToolCallDelta, len(delta.ToolCalls))
				for j, tc := range delta.ToolCalls {
					oaiChoice.Delta.ToolCalls[j] = ChatToolCallDelta{
						Index: tc.Index,
						ID:    tc.ID,
						Type:  tc.Type,
						Function: ChatToolCallFunc{
							Name:      tc.Function.Name,
							Arguments: string(tc.Function.Arguments),
						},
					}
				}
			}

			chunkResp.Choices[i] = oaiChoice
		}
	}

	// 转换 usage（部分提供者在流式 chunk 中也会包含 usage）
	if resp.Usage != nil {
		chunkResp.Usage = convertUsageToOpenAI(resp.Usage)
	}

	return chunkResp
}

// convertUsageToOpenAI 将 providers.Usage 转换为 OpenAI 格式的 ChatCompletionUsage。
// 参考 trpc-agent-go completionUsageToModelUsage 的反向转换。
func convertUsageToOpenAI(usage *providers.Usage) *ChatCompletionUsage {
	if usage == nil {
		return nil
	}
	oaiUsage := &ChatCompletionUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}
	// CachedTokens 和 CacheReadTokens 都转换到 OpenAI 的 prompt_tokens_details.cached_tokens
	cachedTokens := usage.PromptTokensDetails.CachedTokens
	if usage.PromptTokensDetails.CacheReadTokens > cachedTokens {
		cachedTokens = usage.PromptTokensDetails.CacheReadTokens
	}
	if cachedTokens > 0 {
		oaiUsage.PromptTokensDetails = &ChatPromptTokensDetails{
			CachedTokens: cachedTokens,
		}
	}
	return oaiUsage
}

// --- 请求类型转换辅助函数 ---

// convertMessages 将 OpenAI 格式的消息列表转换为 providers.Message 列表。
// 支持纯文本、多模态（text + image_url）、工具调用、工具响应等场景。
func convertMessages(msgs []ChatMessage) ([]providers.Message, error) {
	result := make([]providers.Message, 0, len(msgs))
	for _, msg := range msgs {
		m, err := convertMessage(msg)
		if err != nil {
			return nil, err
		}
		result = append(result, *m)
	}
	return result, nil
}

// convertMessage 将单条 OpenAI 消息转换为 providers.Message。
func convertMessage(msg ChatMessage) (*providers.Message, error) {
	m := &providers.Message{
		Role: providers.Role(msg.Role),
	}

	// 解析 Content 字段（支持 string 或 []contentPart）
	if len(msg.Content) > 0 {
		// 尝试作为 string 解析
		var strContent string
		if err := json.Unmarshal(msg.Content, &strContent); err == nil {
			m.Content = strContent
		} else {
			// 尝试作为 []contentPart 解析（多模态）
			var parts []contentPart
			if err := json.Unmarshal(msg.Content, &parts); err == nil {
				for _, part := range parts {
					switch part.Type {
					case "text":
						if part.Text != "" {
							// 多个 text 块拼接
							if m.Content == "" {
								m.Content = part.Text
							} else {
								m.Content += "\n" + part.Text
							}
						}
					case "image_url":
						if part.ImageURL != nil && part.ImageURL.URL != "" {
							m.AddImageURL(part.ImageURL.URL, part.ImageURL.Detail)
						}
					}
				}
			}
			// 如果都解析失败，将原始内容作为字符串使用
			if m.Content == "" && len(m.ContentParts) == 0 {
				m.Content = string(msg.Content)
			}
		}
	}

	// 处理 tool_calls（assistant 消息中的工具调用）
	if len(msg.ToolCalls) > 0 {
		m.ToolCalls = make([]providers.ToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, providers.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: providers.FunctionDefinitionParam{
					Name:      tc.Function.Name,
					Arguments: []byte(tc.Function.Arguments),
				},
			})
		}
	}

	// 处理 tool 消息的 tool_call_id 和 name
	if msg.ToolCallID != "" {
		m.ToolID = msg.ToolCallID
		m.ToolName = msg.Name
	}

	return m, nil
}

func convertTools(tools []ChatTool) []providers.Tool {
	if len(tools) == 0 {
		return nil
	}
	result := make([]providers.Tool, 0, len(tools))
	for _, t := range tools {
		tool := providers.Tool{
			Name:        t.Function.Name,
			Type:        t.Type,
			Description: t.Function.Description,
		}
		if t.Function.Parameters != nil {
			var schema providers.Schema
			json.Unmarshal(t.Function.Parameters, &schema)
			tool.Parameters = schema
		}
		result = append(result, tool)
	}
	return result
}
