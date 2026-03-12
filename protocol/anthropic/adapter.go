// Package anthropic 实现了 Anthropic Messages API 兼容协议的适配器。
// 支持 /v1/messages 接口的标准请求和 SSE 流式响应。
// 参考 trpc-agent-go/model/anthropic 中的协议转换逻辑实现。
package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nomand-zc/lumin-client/providers"
	"github.com/nomand-zc/lumin-client/queue"
	"github.com/nomand-zc/lumin-proxy/errs"
	"github.com/nomand-zc/lumin-proxy/protocol"
)

func init() {
	protocol.RegisterAdapter(&Adapter{})
}

// Adapter 是 Anthropic 协议的适配器实现。
type Adapter struct{}

// Name 返回协议名称。
func (a *Adapter) Name() string {
	return "anthropic"
}

// Routes 返回 Anthropic 协议需要注册的路由列表。
func (a *Adapter) Routes(defaultHandler http.Handler) []protocol.Route {
	return []protocol.Route{
		{
			Pattern: "/messages",
			Handler: defaultHandler,
		},
	}
}

// ParseRequest 将 Anthropic 格式的 HTTP 请求解析为统一的内部请求。
func (a *Adapter) ParseRequest(r *http.Request) (*protocol.Request, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInvalidRequest, "读取请求体失败", err)
	}
	defer r.Body.Close()

	var req MessageRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, errs.Wrap(errs.CodeInvalidRequest, "解析请求体失败", err)
	}

	if req.Model == "" {
		return nil, errs.New(errs.CodeInvalidRequest, "model 字段不能为空")
	}

	// 转换消息
	messages, err := convertMessages(req.Messages, req.System)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInvalidRequest, "转换消息失败", err)
	}

	// 构造 MaxTokens 指针
	var maxTokens *int
	if req.MaxTokens > 0 {
		maxTokens = &req.MaxTokens
	}

	// 转换为 lumin-client 的 providers.Request
	provReq := providers.Request{
		Model:    req.Model,
		Messages: messages,
		GenerationConfig: providers.GenerationConfig{
			MaxTokens:   maxTokens,
			Temperature: req.Temperature,
			TopP:        req.TopP,
			Stream:      req.Stream,
			Stop:        req.StopSequences,
		},
		Tools: convertTools(req.Tools),
	}

	// 处理 thinking 配置
	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		enabled := true
		provReq.GenerationConfig.ThinkingEnabled = &enabled
		if req.Thinking.BudgetTokens > 0 {
			provReq.GenerationConfig.ThinkingTokens = &req.Thinking.BudgetTokens
		}
	}

	metadata := make(map[string]any)
	if req.ToolChoice != nil {
		metadata["tool_choice"] = req.ToolChoice
	}
	if req.Metadata != nil && req.Metadata.UserID != "" {
		metadata["user_id"] = req.Metadata.UserID
	}

	return &protocol.Request{
		Model:           req.Model,
		Stream:          req.Stream,
		ProviderRequest: &provReq,
		RawBody:         body,
		Metadata:        metadata,
	}, nil
}

// WriteResponse 写入非流式响应（Anthropic Messages API 格式）。
func (a *Adapter) WriteResponse(ctx context.Context, w http.ResponseWriter, resp *providers.Response) error {
	w.Header().Set("Content-Type", "application/json")

	if resp.Error != nil {
		w.WriteHeader(http.StatusBadRequest)
		return json.NewEncoder(w).Encode(ErrorResponse{
			Type: "error",
			Error: ErrorDetail{
				Type:    mapErrorType(resp.Error.Type),
				Message: resp.Error.Message,
			},
		})
	}

	// 构建 Anthropic 格式的响应
	anthropicResp := buildAnthropicResponse(resp)

	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(anthropicResp)
}

// WriteStreamResponse 写入 SSE 流式响应（Anthropic Messages API 格式）。
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

	// 流式状态跟踪
	state := &streamState{}

	err := stream.Each(ctx, func(resp *providers.Response) error {
		if resp.Error != nil {
			// 写入错误事件
			writeSSEEvent(w, "error", ErrorResponse{
				Type: "error",
				Error: ErrorDetail{
					Type:    mapErrorType(resp.Error.Type),
					Message: resp.Error.Message,
				},
			})
			flusher.Flush()
			return nil
		}

		// 写入流式事件
		a.writeStreamChunks(w, flusher, resp, state)
		return nil
	})

	// 发送 message_stop 事件
	writeSSEEvent(w, "message_stop", map[string]string{"type": "message_stop"})
	flusher.Flush()

	return err
}

// WriteError 写入错误响应（Anthropic 格式）。
func (a *Adapter) WriteError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")

	pe, ok := errs.IsProxyError(err)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{
			Type: "error",
			Error: ErrorDetail{
				Type:    "api_error",
				Message: err.Error(),
			},
		})
		return
	}

	w.WriteHeader(pe.Code.HTTPStatus())
	errType := mapProxyErrorType(pe.Code)
	json.NewEncoder(w).Encode(ErrorResponse{
		Type: "error",
		Error: ErrorDetail{
			Type:    errType,
			Message: pe.Message,
		},
	})
}

// --- 类型转换辅助函数 ---

// convertMessages 将 Anthropic 格式的消息列表转换为 providers.Message 列表。
// 同时处理 system 消息（Anthropic 的 system 是顶级字段而非消息）。
func convertMessages(msgs []Message, systemRaw json.RawMessage) ([]providers.Message, error) {
	result := make([]providers.Message, 0, len(msgs)+1)

	// 处理 system 字段
	if len(systemRaw) > 0 {
		systemMsgs, err := parseSystemPrompt(systemRaw)
		if err != nil {
			return nil, fmt.Errorf("解析 system 字段失败: %w", err)
		}
		result = append(result, systemMsgs...)
	}

	// 处理消息列表
	for _, msg := range msgs {
		converted, err := convertMessage(msg)
		if err != nil {
			return nil, err
		}
		result = append(result, converted...)
	}
	return result, nil
}

// parseSystemPrompt 解析 Anthropic 的 system 字段。
// 支持 string 和 []SystemBlock 两种格式。
func parseSystemPrompt(raw json.RawMessage) ([]providers.Message, error) {
	// 尝试作为 string 解析
	var strContent string
	if err := json.Unmarshal(raw, &strContent); err == nil {
		if strContent != "" {
			return []providers.Message{providers.NewSystemMessage(strContent)}, nil
		}
		return nil, nil
	}

	// 尝试作为 []SystemBlock 解析
	var blocks []SystemBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("system 字段格式无效: %s", string(raw))
	}

	var texts []string
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	if len(texts) > 0 {
		return []providers.Message{providers.NewSystemMessage(strings.Join(texts, "\n"))}, nil
	}
	return nil, nil
}

// convertMessage 将单条 Anthropic 消息转换为 providers.Message 列表。
// Anthropic 的一条消息可能包含多个 content block，tool_result 需要拆分为独立的 tool 消息。
func convertMessage(msg Message) ([]providers.Message, error) {
	role := msg.Role

	// 尝试作为纯文本 string 解析
	var strContent string
	if err := json.Unmarshal(msg.Content, &strContent); err == nil {
		return []providers.Message{
			{
				Role:    providers.Role(role),
				Content: strContent,
			},
		}, nil
	}

	// 作为 []ContentBlock 解析
	var blocks []ContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		// 如果都解析失败，将原始内容作为字符串使用
		return []providers.Message{
			{
				Role:    providers.Role(role),
				Content: string(msg.Content),
			},
		}, nil
	}

	switch role {
	case "assistant":
		return convertAssistantBlocks(blocks)
	case "user":
		return convertUserBlocks(blocks)
	default:
		return convertUserBlocks(blocks)
	}
}

// convertAssistantBlocks 将 assistant 消息的内容块转换为统一消息。
// 参考 trpc-agent-go 中 convertAssistantMessageContent 的逻辑。
func convertAssistantBlocks(blocks []ContentBlock) ([]providers.Message, error) {
	m := providers.Message{
		Role: providers.RoleAssistant,
	}

	var textParts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "thinking":
			if block.Thinking != "" {
				m.ReasoningContent += block.Thinking
			}
		case "tool_use":
			tc := providers.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: providers.FunctionDefinitionParam{
					Name: block.Name,
				},
			}
			if len(block.Input) > 0 {
				tc.Function.Arguments = []byte(block.Input)
			}
			m.ToolCalls = append(m.ToolCalls, tc)
		}
	}
	if len(textParts) > 0 {
		m.Content = strings.Join(textParts, "")
	}
	return []providers.Message{m}, nil
}

// convertUserBlocks 将 user 消息的内容块转换为统一消息。
// tool_result 块会被拆分为独立的 tool 消息，其余内容合并为 user 消息。
// 参考 trpc-agent-go 中 convertMessages 对 tool result 合并到 user message 的逻辑。
func convertUserBlocks(blocks []ContentBlock) ([]providers.Message, error) {
	var result []providers.Message
	var textParts []string
	var hasUserContent bool

	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
				hasUserContent = true
			}
		case "image":
			hasUserContent = true
			// 图片处理：如果有 source，构建图片 URL
			if block.Source != nil && block.Source.Type == "base64" {
				// 将 base64 图片作为 data URI
				dataURI := fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, block.Source.Data)
				userMsg := providers.Message{Role: providers.RoleUser}
				userMsg.AddImageURL(dataURI, "auto")
				result = append(result, userMsg)
			}
		case "tool_result":
			// tool_result 转换为独立的 tool 消息
			content := extractToolResultContent(block)
			result = append(result, providers.Message{
				Role:    providers.RoleTool,
				ToolID:  block.ToolUseID,
				Content: content,
			})
		}
	}

	// 如果有文本或其他用户内容，创建 user 消息
	if hasUserContent && len(textParts) > 0 {
		userMsg := providers.Message{
			Role:    providers.RoleUser,
			Content: strings.Join(textParts, "\n"),
		}
		// 将 user 消息插入到 tool 消息前面
		result = append([]providers.Message{userMsg}, result...)
	}

	return result, nil
}

// extractToolResultContent 从 tool_result 内容块中提取文本内容。
func extractToolResultContent(block ContentBlock) string {
	if len(block.Content) == 0 {
		return ""
	}

	// 尝试作为 string 解析
	var strContent string
	if err := json.Unmarshal(block.Content, &strContent); err == nil {
		return strContent
	}

	// 尝试作为 []ContentBlock 解析
	var innerBlocks []ContentBlock
	if err := json.Unmarshal(block.Content, &innerBlocks); err == nil {
		var texts []string
		for _, inner := range innerBlocks {
			if inner.Type == "text" && inner.Text != "" {
				texts = append(texts, inner.Text)
			}
		}
		return strings.Join(texts, "\n")
	}

	return string(block.Content)
}

// convertTools 将 Anthropic 工具定义转换为统一的 providers.Tool。
// 参考 trpc-agent-go 中 convertTools 的逻辑。
func convertTools(tools []Tool) []providers.Tool {
	if len(tools) == 0 {
		return nil
	}
	result := make([]providers.Tool, 0, len(tools))
	for _, t := range tools {
		tool := providers.Tool{
			Name:        t.Name,
			Type:        "function",
			Description: t.Description,
		}
		// 转换 input_schema 为 providers.Schema
		if t.InputSchema.Properties != nil {
			schema := providers.Schema{
				Type:     t.InputSchema.Type,
				Required: t.InputSchema.Required,
			}
			// 将 map[string]any 转换为 map[string]*Schema
			if props, err := convertProperties(t.InputSchema.Properties); err == nil {
				schema.Properties = props
			}
			tool.Parameters = schema
		}
		result = append(result, tool)
	}
	return result
}

// convertProperties 将 map[string]any 转换为 map[string]*providers.Schema。
func convertProperties(props map[string]any) (map[string]*providers.Schema, error) {
	result := make(map[string]*providers.Schema, len(props))
	for key, val := range props {
		data, err := json.Marshal(val)
		if err != nil {
			continue
		}
		var schema providers.Schema
		if err := json.Unmarshal(data, &schema); err != nil {
			continue
		}
		result[key] = &schema
	}
	return result, nil
}

// --- 响应构建辅助函数 ---

// buildAnthropicResponse 将统一的 providers.Response 转换为 Anthropic MessageResponse。
// 参考 trpc-agent-go 中 handleNonStreamingResponse 和 convertContentBlock 的逻辑。
func buildAnthropicResponse(resp *providers.Response) *MessageResponse {
	anthropicResp := &MessageResponse{
		ID:    resp.ID,
		Type:  "message",
		Role:  "assistant",
		Model: resp.Model,
	}

	// 构建 content 列表
	var contents []ResponseContent
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		msg := choice.Message

		// 如果有 reasoning_content（thinking），添加 thinking 块
		if msg.ReasoningContent != "" {
			contents = append(contents, ResponseContent{
				Type:     "thinking",
				Thinking: msg.ReasoningContent,
			})
		}

		// 添加文本内容
		if msg.Content != "" {
			contents = append(contents, ResponseContent{
				Type: "text",
				Text: msg.Content,
			})
		}

		// 添加工具调用
		for _, tc := range msg.ToolCalls {
			contents = append(contents, ResponseContent{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: tc.Function.Arguments,
			})
		}

		// 设置 stop_reason
		if choice.FinishReason != nil {
			reason := mapFinishReason(*choice.FinishReason)
			anthropicResp.StopReason = &reason
		}
	}

	if len(contents) == 0 {
		// 确保 content 不为空
		contents = append(contents, ResponseContent{Type: "text", Text: ""})
	}
	anthropicResp.Content = contents

	// 设置 usage
	if resp.Usage != nil {
		anthropicResp.Usage = &MessageUsage{
			InputTokens:              resp.Usage.PromptTokens,
			OutputTokens:             resp.Usage.CompletionTokens,
			CacheCreationInputTokens: resp.Usage.PromptTokensDetails.CacheCreationTokens,
			CacheReadInputTokens:     resp.Usage.PromptTokensDetails.CacheReadTokens,
		}
	}

	return anthropicResp
}

// --- 流式响应辅助函数 ---

// streamState 流式响应状态跟踪。
type streamState struct {
	messageStarted bool
	blockIndex     int
	blockStarted   bool
	blockType      string // 当前块类型
	model          string
	id             string
}

// writeStreamChunks 将统一的 providers.Response 转换为 Anthropic SSE 事件写入。
// 参考 trpc-agent-go 中 handleStreamingResponse 和 buildStreamingPartialResponse 的逻辑。
func (a *Adapter) writeStreamChunks(w http.ResponseWriter, flusher http.Flusher, resp *providers.Response, state *streamState) {
	if !state.messageStarted {
		// 发送 message_start 事件
		state.messageStarted = true
		state.model = resp.Model
		state.id = resp.ID
		writeSSEEvent(w, "message_start", MessageStartEvent{
			Type: "message_start",
			Message: MessageResponse{
				ID:      resp.ID,
				Type:    "message",
				Role:    "assistant",
				Model:   resp.Model,
				Content: []ResponseContent{},
			},
		})
		flusher.Flush()
	}

	if len(resp.Choices) == 0 {
		return
	}

	choice := resp.Choices[0]
	delta := choice.Delta

	// 处理 reasoning_content（thinking）
	if delta.ReasoningContent != "" {
		if !state.blockStarted || state.blockType != "thinking" {
			// 如果之前有其他类型的块，先关闭
			if state.blockStarted {
				writeSSEEvent(w, "content_block_stop", ContentBlockStopEvent{
					Type:  "content_block_stop",
					Index: state.blockIndex,
				})
				state.blockIndex++
			}
			state.blockStarted = true
			state.blockType = "thinking"
			writeSSEEvent(w, "content_block_start", ContentBlockStartEvent{
				Type:         "content_block_start",
				Index:        state.blockIndex,
				ContentBlock: ResponseContent{Type: "thinking", Thinking: ""},
			})
		}
		writeSSEEvent(w, "content_block_delta", ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: state.blockIndex,
			Delta: Delta{Type: "thinking_delta", Thinking: delta.ReasoningContent},
		})
		flusher.Flush()
	}

	// 处理文本内容
	if delta.Content != "" {
		if !state.blockStarted || state.blockType != "text" {
			// 如果之前有其他类型的块，先关闭
			if state.blockStarted {
				writeSSEEvent(w, "content_block_stop", ContentBlockStopEvent{
					Type:  "content_block_stop",
					Index: state.blockIndex,
				})
				state.blockIndex++
			}
			state.blockStarted = true
			state.blockType = "text"
			writeSSEEvent(w, "content_block_start", ContentBlockStartEvent{
				Type:         "content_block_start",
				Index:        state.blockIndex,
				ContentBlock: ResponseContent{Type: "text", Text: ""},
			})
		}
		writeSSEEvent(w, "content_block_delta", ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: state.blockIndex,
			Delta: Delta{Type: "text_delta", Text: delta.Content},
		})
		flusher.Flush()
	}

	// 处理工具调用（流式中的 tool_use）
	for _, tc := range delta.ToolCalls {
		if tc.ID != "" {
			// 新的 tool_use 块开始
			if state.blockStarted {
				writeSSEEvent(w, "content_block_stop", ContentBlockStopEvent{
					Type:  "content_block_stop",
					Index: state.blockIndex,
				})
				state.blockIndex++
			}
			state.blockStarted = true
			state.blockType = "tool_use"
			writeSSEEvent(w, "content_block_start", ContentBlockStartEvent{
				Type:  "content_block_start",
				Index: state.blockIndex,
				ContentBlock: ResponseContent{
					Type: "tool_use",
					ID:   tc.ID,
					Name: tc.Function.Name,
				},
			})
			flusher.Flush()
		}
		// 如果有参数增量
		if len(tc.Function.Arguments) > 0 {
			writeSSEEvent(w, "content_block_delta", ContentBlockDeltaEvent{
				Type:  "content_block_delta",
				Index: state.blockIndex,
				Delta: Delta{Type: "input_json_delta", PartialJSON: string(tc.Function.Arguments)},
			})
			flusher.Flush()
		}
	}

	// 处理 finish_reason
	if choice.FinishReason != nil {
		// 关闭当前块
		if state.blockStarted {
			writeSSEEvent(w, "content_block_stop", ContentBlockStopEvent{
				Type:  "content_block_stop",
				Index: state.blockIndex,
			})
			state.blockStarted = false
		}

		// 发送 message_delta（包含 stop_reason）
		reason := mapFinishReason(*choice.FinishReason)
		writeSSEEvent(w, "message_delta", MessageDeltaEvent{
			Type:  "message_delta",
			Delta: MessageDelta{StopReason: &reason},
		})
		flusher.Flush()
	}
}

// writeSSEEvent 写入一个 SSE 事件。
func writeSSEEvent(w http.ResponseWriter, eventType string, data any) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, jsonData)
}

// --- 错误类型映射 ---

// mapErrorType 将内部错误类型映射为 Anthropic 错误类型。
func mapErrorType(errType string) string {
	switch errType {
	case "invalid_request_error":
		return "invalid_request_error"
	case "authentication_error":
		return "authentication_error"
	case "permission_error":
		return "permission_error"
	case "rate_limit_error":
		return "rate_limit_error"
	case "not_found_error":
		return "not_found_error"
	case "server_error", "internal_error":
		return "api_error"
	default:
		return "api_error"
	}
}

// mapProxyErrorType 将 ProxyError 错误码映射为 Anthropic 错误类型。
func mapProxyErrorType(code errs.Code) string {
	switch code {
	case errs.CodeInvalidRequest, errs.CodeProtocolError:
		return "invalid_request_error"
	case errs.CodeUnauthorized:
		return "authentication_error"
	case errs.CodeForbidden:
		return "permission_error"
	case errs.CodeNotFound, errs.CodeModelNotSupported:
		return "not_found_error"
	case errs.CodeRateLimited:
		return "rate_limit_error"
	case errs.CodeUpstreamError, errs.CodeNoAvailableAccount:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

// mapFinishReason 将统一的 finish_reason 映射为 Anthropic 的 stop_reason。
// Anthropic 使用: "end_turn", "max_tokens", "stop_sequence", "tool_use"
func mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "end_turn"
	default:
		return "end_turn"
	}
}

// --- SSE 解析辅助（用于将上游 Anthropic SSE 响应解析为统一格式） ---

// ParseSSEStream 解析 Anthropic SSE 流式响应。
// 用于 proxy 从上游 Anthropic API 接收 SSE 响应时使用。
func ParseSSEStream(body io.Reader) <-chan *providers.Response {
	ch := make(chan *providers.Response, 64)
	go func() {
		defer close(ch)

		scanner := bufio.NewScanner(body)
		var currentEvent string

		for scanner.Scan() {
			line := scanner.Text()

			// 空行表示事件结束
			if line == "" {
				currentEvent = ""
				continue
			}

			// 解析 event: 行
			if strings.HasPrefix(line, "event: ") {
				currentEvent = strings.TrimPrefix(line, "event: ")
				continue
			}

			// 解析 data: 行
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				resp := parseSSEEventToResponse(currentEvent, []byte(data))
				if resp != nil {
					ch <- resp
				}
			}
		}
	}()
	return ch
}

// parseSSEEventToResponse 将单个 Anthropic SSE 事件解析为统一的 providers.Response。
// 参考 trpc-agent-go 中 buildStreamingPartialResponse 的逻辑。
func parseSSEEventToResponse(eventType string, data []byte) *providers.Response {
	now := time.Now()

	switch eventType {
	case "message_start":
		var event MessageStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil
		}
		resp := &providers.Response{
			ID:        event.Message.ID,
			Object:    providers.ObjectChatCompletionChunk,
			Created:   now.Unix(),
			Model:     event.Message.Model,
			Timestamp: now,
			IsPartial: true,
			Choices: []providers.Choice{
				{
					Index: 0,
					Delta: providers.Message{Role: providers.RoleAssistant},
				},
			},
		}
		// 设置输入 token 的 usage
		if event.Message.Usage != nil {
			resp.Usage = &providers.Usage{
				PromptTokens: event.Message.Usage.InputTokens,
				PromptTokensDetails: providers.PromptTokensDetails{
					CacheCreationTokens: event.Message.Usage.CacheCreationInputTokens,
					CacheReadTokens:     event.Message.Usage.CacheReadInputTokens,
					CachedTokens:        event.Message.Usage.CacheReadInputTokens,
				},
			}
		}
		return resp

	case "content_block_delta":
		var event ContentBlockDeltaEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil
		}

		resp := &providers.Response{
			Object:    providers.ObjectChatCompletionChunk,
			Created:   now.Unix(),
			Timestamp: now,
			IsPartial: true,
			Choices: []providers.Choice{
				{
					Index: 0,
					Delta: providers.Message{Role: providers.RoleAssistant},
				},
			},
		}

		switch event.Delta.Type {
		case "text_delta":
			if event.Delta.Text == "" {
				return nil
			}
			resp.Choices[0].Delta.Content = event.Delta.Text
		case "thinking_delta":
			if event.Delta.Thinking == "" {
				return nil
			}
			resp.Choices[0].Delta.ReasoningContent = event.Delta.Thinking
		case "input_json_delta":
			// 工具调用参数增量
			if event.Delta.PartialJSON == "" {
				return nil
			}
			resp.Choices[0].Delta.ToolCalls = []providers.ToolCall{
				{
					Index:    &event.Index,
					Type:     "function",
					Function: providers.FunctionDefinitionParam{Arguments: []byte(event.Delta.PartialJSON)},
				},
			}
		default:
			return nil
		}
		return resp

	case "content_block_start":
		var event ContentBlockStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil
		}
		// tool_use 块开始时，发送工具调用的 ID 和 Name
		if event.ContentBlock.Type == "tool_use" {
			return &providers.Response{
				Object:    providers.ObjectChatCompletionChunk,
				Created:   now.Unix(),
				Timestamp: now,
				IsPartial: true,
				Choices: []providers.Choice{
					{
						Index: 0,
						Delta: providers.Message{
							Role: providers.RoleAssistant,
							ToolCalls: []providers.ToolCall{
								{
									Index:    &event.Index,
									ID:       event.ContentBlock.ID,
									Type:     "function",
									Function: providers.FunctionDefinitionParam{Name: event.ContentBlock.Name},
								},
							},
						},
					},
				},
			}
		}
		return nil

	case "message_delta":
		var event MessageDeltaEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil
		}

		resp := &providers.Response{
			Object:    providers.ObjectChatCompletionChunk,
			Created:   now.Unix(),
			Timestamp: now,
			IsPartial: true,
			Choices: []providers.Choice{
				{
					Index: 0,
					Delta: providers.Message{Role: providers.RoleAssistant},
				},
			},
		}

		if event.Delta.StopReason != nil {
			// 将 Anthropic stop_reason 映射回统一的 finish_reason
			reason := mapStopReasonToFinishReason(*event.Delta.StopReason)
			resp.Choices[0].FinishReason = &reason
		}
		if event.Usage != nil {
			resp.Usage = &providers.Usage{
				CompletionTokens: event.Usage.OutputTokens,
			}
		}
		return resp

	case "message_stop":
		return &providers.Response{
			Object:    providers.ObjectChatCompletion,
			Created:   now.Unix(),
			Timestamp: now,
			Done:      true,
		}

	default:
		// ping, content_block_stop 等事件忽略
		return nil
	}
}

// mapStopReasonToFinishReason 将 Anthropic 的 stop_reason 映射回统一的 finish_reason。
func mapStopReasonToFinishReason(stopReason string) string {
	switch stopReason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "stop_sequence":
		return "stop"
	default:
		return "stop"
	}
}
