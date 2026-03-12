// Package plugin 实现了配置驱动的插件系统。
// 借鉴 trpc-go 的插件工厂注册表和依赖管理机制，
// 在 Kratos 的生命周期钩子中完成插件的初始化和关闭。
package plugin

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"gopkg.in/yaml.v3"
)

// Factory 插件工厂接口。
// 每种插件需要实现此接口，并通过 Register() 注册到全局注册表。
type Factory interface {
	// Type 返回插件类型，用于分类，如 "auth", "billing", "logging"。
	Type() string
	// Setup 根据配置初始化插件。
	// name 是插件实例名（支持同一类型多实例），dec 用于延迟解码插件专属配置。
	Setup(ctx context.Context, name string, dec Decoder) error
}

// Decoder 配置解码器接口，用于将 YAML 节点延迟解码为插件自定义配置结构体。
type Decoder interface {
	Decode(cfg interface{}) error
}

// YamlNodeDecoder 是基于 yaml.Node 的 Decoder 实现。
type YamlNodeDecoder struct {
	Node *yaml.Node
}

// Decode 将 yaml.Node 解码到指定结构体。
func (d *YamlNodeDecoder) Decode(cfg interface{}) error {
	if d.Node == nil {
		return fmt.Errorf("yaml node 为空")
	}
	return d.Node.Decode(cfg)
}

// Closer 可选接口，插件需要释放资源时实现。
type Closer interface {
	Close(ctx context.Context) error
}

// Reloadable 可选接口，支持热更新的插件实现。
type Reloadable interface {
	Reload(ctx context.Context, dec Decoder) error
}

// HookProvider 可选接口，插件通过实现此接口来注册钩子。
type HookProvider interface {
	// Hooks 返回插件提供的钩子集合。
	Hooks() *Hooks
}

// HTTPMiddlewareProvider 可选接口，插件通过实现此接口来提供 HTTP 中间件。
type HTTPMiddlewareProvider interface {
	// HTTPMiddleware 返回 HTTP 级别中间件。
	HTTPMiddleware() func(http.Handler) http.Handler
}

// HookRunner 钩子运行器接口，定义了插件钩子的执行行为。
// 代理核心层通过此接口与插件系统交互，而不直接依赖 Manager 实现。
type HookRunner interface {
	// RunBeforeRequest 按顺序执行所有 BeforeRequest 钩子。
	RunBeforeRequest(ctx context.Context, req *RequestInfo) (context.Context, error)
	// RunAfterResponse 按顺序执行所有 AfterResponse 钩子。
	RunAfterResponse(ctx context.Context, req *RequestInfo, resp *ResponseInfo, err error)
	// RunOnStreamChunk 按顺序执行所有 OnStreamChunk 钩子。
	RunOnStreamChunk(ctx context.Context, req *RequestInfo, chunk *ResponseInfo)
}

// LifecycleManager 插件生命周期管理接口，定义了插件的初始化、关闭和中间件获取等行为。
// Server 层通过此接口与插件系统交互。
type LifecycleManager interface {
	HookRunner
	// HTTPMiddlewares 返回所有插件提供的 HTTP 中间件。
	HTTPMiddlewares() []func(http.Handler) http.Handler
	// HasPlugins 返回是否有已初始化的插件。
	HasPlugins() bool
	// CloseAll 按逆序关闭所有实现了 Closer 接口的插件。
	CloseAll(ctx context.Context) error
}

// Hooks 钩子函数集合。
type Hooks struct {
	// BeforeRequest 请求前置钩子
	BeforeRequest BeforeRequestHook
	// AfterResponse 响应后置钩子
	AfterResponse AfterResponseHook
	// OnStreamChunk 流式 chunk 钩子
	OnStreamChunk OnStreamChunkHook
}

// BeforeRequestHook 请求前置钩子类型。
// 可修改请求、注入上下文、拒绝请求（返回 error）。
type BeforeRequestHook func(ctx context.Context, req *RequestInfo) (context.Context, error)

// AfterResponseHook 响应后置钩子类型。
// 可记录日志/指标，不应修改响应。
type AfterResponseHook func(ctx context.Context, req *RequestInfo, resp *ResponseInfo, err error)

// OnStreamChunkHook 流式 chunk 钩子类型（可选，用于流式计费/审计）。
type OnStreamChunkHook func(ctx context.Context, req *RequestInfo, chunk *ResponseInfo)

// RequestInfo 是插件可访问的请求上下文信息。
type RequestInfo struct {
	// Model 请求的模型名称
	Model string
	// Stream 是否为流式请求
	Stream bool
	// UserID 用户标识
	UserID string
	// APIKey 原始 API Key（由鉴权插件解析）
	APIKey string
	// Metadata 额外元数据
	Metadata map[string]any
}

// ResponseInfo 是插件可访问的响应上下文信息。
type ResponseInfo struct {
	// AccountID 使用的账号 ID
	AccountID string
	// ProviderType 使用的供应商类型
	ProviderType string
	// ProviderName 使用的供应商名称
	ProviderName string
	// Model 实际使用的模型
	Model string
	// PromptTokens prompt token 数
	PromptTokens int
	// CompletionTokens completion token 数
	CompletionTokens int
	// TotalTokens 总 token 数
	TotalTokens int
	// StatusCode HTTP 状态码
	StatusCode int
}

// --- 全局注册表 ---

var (
	mu        sync.RWMutex
	factories = make(map[string]Factory) // name => Factory
)

// Register 注册一个插件工厂到全局注册表。
// 通常在 init() 函数中调用。
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := factories[name]; exists {
		panic(fmt.Sprintf("插件 %q 已注册", name))
	}
	factories[name] = f
}

// Get 根据名称获取已注册的插件工厂。
func Get(name string) (Factory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	f, ok := factories[name]
	return f, ok
}

// List 列出所有已注册的插件名。
func List() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(factories))
	for name := range factories {
		names = append(names, name)
	}
	return names
}
