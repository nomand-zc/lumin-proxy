// Package protocol 定义了协议适配层的核心接口。
// 每种外部 AI 协议（OpenAI、Anthropic 等）需要实现 Adapter 接口，
// 负责将协议特定的 HTTP 请求/响应与内部统一模型之间进行转换。
package protocol

import (
	"context"
	"net/http"

	"github.com/nomand-zc/lumin-client/providers"
	"github.com/nomand-zc/lumin-client/queue"
)

// Adapter 协议适配器接口。
// 每种外部协议（OpenAI / Anthropic / ...）实现一个 Adapter。
type Adapter interface {
	// Name 返回协议名称，如 "openai", "anthropic"。
	Name() string

	// ParseRequest 将 HTTP 请求解析为统一的内部代理请求。
	ParseRequest(r *http.Request) (*Request, error)

	// WriteResponse 将统一的内部响应写回 HTTP 响应（非流式）。
	WriteResponse(ctx context.Context, w http.ResponseWriter, resp *providers.Response) error

	// WriteStreamResponse 将流式响应逐 chunk 写回 HTTP 响应（SSE 格式）。
	WriteStreamResponse(ctx context.Context, w http.ResponseWriter, stream queue.Consumer[*providers.Response]) error

	// WriteError 将错误写回 HTTP 响应（遵循该协议的错误格式）。
	WriteError(w http.ResponseWriter, err error)

	// Routes 返回该协议适配器需要注册的路由列表。
	// 每个 Route 包含路径模式和对应的处理函数。
	// defaultHandler 是默认的代理处理器，Route 中的 Handler 字段为 nil 时使用它。
	Routes(defaultHandler http.Handler) []Route
}

// Route 描述一条协议路由。
type Route struct {
	// Pattern 路由路径模式，如 "/chat/completions"
	Pattern string
	// Handler 路由的 HTTP Handler；如果为 nil，则使用传入的默认 Handler
	Handler http.Handler
	// IsPrefix 是否为前缀匹配模式
	IsPrefix bool
}

// Request 是协议无关的统一代理请求。
type Request struct {
	// Model 请求的模型名称
	Model string
	// Stream 是否为流式请求
	Stream bool
	// ProviderRequest 转换后的统一请求体（lumin-client 格式）
	ProviderRequest *providers.Request
	// RawBody 原始请求体（用于透传或审计）
	RawBody []byte
	// Metadata 额外元数据（供插件消费）
	Metadata map[string]any
}
