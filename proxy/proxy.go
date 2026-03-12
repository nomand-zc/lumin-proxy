// Package proxy 定义了代理核心层的接口。
// 代理层编排 Pick → Hook → Invoke → Hook → Report 的完整流程。
package proxy

import (
	"context"

	"github.com/nomand-zc/lumin-client/providers"
	"github.com/nomand-zc/lumin-client/queue"
	"github.com/nomand-zc/lumin-proxy/protocol"
)

// ProviderRegistry 是获取 Provider 实例的接口。

type ProviderRegistry func (providerType, providerName string) (providers.Provider, error)

// Proxy 是代理核心接口。
type Proxy interface {
	// Handle 处理一个非流式代理请求。
	Handle(ctx context.Context, req *protocol.Request) (*Result, error)

	// HandleStream 处理一个流式代理请求，返回流式响应消费者。
	HandleStream(ctx context.Context, req *protocol.Request) (*StreamResult, error)
}

// Result 非流式代理结果。
type Result struct {
	// Response 统一响应体
	Response *providers.Response
	// AccountID 使用的账号 ID
	AccountID string
	// ProviderType 供应商类型
	ProviderType string
	// ProviderName 供应商名称
	ProviderName string
}

// StreamResult 流式代理结果。
type StreamResult struct {
	// Stream 流式响应消费者
	Stream queue.Consumer[*providers.Response]
	// AccountID 使用的账号 ID
	AccountID string
	// ProviderType 供应商类型
	ProviderType string
	// ProviderName 供应商名称
	ProviderName string
}
