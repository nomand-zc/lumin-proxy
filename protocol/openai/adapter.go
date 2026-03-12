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

// --- 请求类型定义 ---

// ChatCompletionRequest OpenAI Chat Completion 请求体。
type ChatCompletionRequest struct {
	Model            string          `json:"model"`
	Messages         []ChatMessage   `json:"messages"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	Stream           bool            `json:"stream"`
	Stop             []string        `json:"stop,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	Tools            []ChatTool      `json:"tools,omitempty"`
	ReasoningEffort  *string         `json:"reasoning_effort,omitempty"`
}

// ChatMessage OpenAI 消息。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

// ChatTool OpenAI 工具定义。
type ChatTool struct {
	Type     string         `json:"type"`
	Function ChatToolFunc   `json:"function"`
}

// ChatToolFunc 工具函数定义。
type ChatToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// --- 错误响应类型 ---

// ErrorResponse OpenAI 格式的错误响应。
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail 错误详情。
type ErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param,omitempty"`
	Code    *string `json:"code,omitempty"`
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

	// 转换为 lumin-client 的 providers.Request
	provReq := providers.Request{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		GenerationConfig: providers.GenerationConfig{
			MaxTokens:        req.MaxTokens,
			Temperature:      req.Temperature,
			TopP:             req.TopP,
			Stream:           req.Stream,
			Stop:             req.Stop,
			PresencePenalty:   req.PresencePenalty,
			FrequencyPenalty:  req.FrequencyPenalty,
			ReasoningEffort:   req.ReasoningEffort,
		},
		Tools: convertTools(req.Tools),
	}

	return &protocol.Request{
		Model:           req.Model,
		Stream:          req.Stream,
		ProviderRequest: provReq,
		RawBody:         body,
		Metadata:        make(map[string]any),
	}, nil
}

// WriteResponse 写入非流式响应。
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

	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(resp)
}

// WriteStreamResponse 写入 SSE 流式响应。
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
			// 写入错误事件
			data, _ := json.Marshal(resp)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			return nil
		}

		data, err := json.Marshal(resp)
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

// --- 类型转换辅助函数 ---

func convertMessages(msgs []ChatMessage) []providers.Message {
	result := make([]providers.Message, 0, len(msgs))
	for _, msg := range msgs {
		result = append(result, providers.Message{
			Role:    providers.Role(msg.Role),
			Content: msg.Content,
		})
	}
	return result
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

