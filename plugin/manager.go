package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// SetupTimeout 每个插件初始化的超时时间。
var SetupTimeout = 10 * time.Second

// Manager 插件管理器，管理插件的初始化、钩子执行和生命周期。
type Manager struct {
	mu sync.RWMutex

	// 已初始化的插件工厂列表（按初始化顺序排列）
	initialized []initializedPlugin

	// 钩子链
	beforeRequest []BeforeRequestHook
	afterResponse []AfterResponseHook
	onStreamChunk []OnStreamChunkHook

	// HTTP 中间件
	httpMiddlewares []func(http.Handler) http.Handler
}

// initializedPlugin 记录已初始化的插件信息。
type initializedPlugin struct {
	name    string
	factory Factory
}

// NewManager 创建一个新的插件管理器。
func NewManager() *Manager {
	return &Manager{}
}

// SetupAll 根据配置初始化所有插件。
// 借鉴 trpc-go 的 SetupClosables 流程：加载 → 初始化 → 收集钩子。
func (m *Manager) SetupAll(ctx context.Context, configs map[string]yaml.Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, cfgNode := range configs {
		f, ok := Get(name)
		if !ok {
			slog.Warn("插件未注册，跳过", "name", name)
			continue
		}

		// 带超时的插件初始化
		if err := m.setupOne(ctx, name, f, &cfgNode); err != nil {
			return fmt.Errorf("初始化插件 %q 失败: %w", name, err)
		}

		m.initialized = append(m.initialized, initializedPlugin{
			name:    name,
			factory: f,
		})

		// 收集钩子
		if hp, ok := f.(HookProvider); ok {
			hooks := hp.Hooks()
			if hooks != nil {
				if hooks.BeforeRequest != nil {
					m.beforeRequest = append(m.beforeRequest, hooks.BeforeRequest)
				}
				if hooks.AfterResponse != nil {
					m.afterResponse = append(m.afterResponse, hooks.AfterResponse)
				}
				if hooks.OnStreamChunk != nil {
					m.onStreamChunk = append(m.onStreamChunk, hooks.OnStreamChunk)
				}
			}
		}

		// 收集 HTTP 中间件
		if mp, ok := f.(HTTPMiddlewareProvider); ok {
			if mw := mp.HTTPMiddleware(); mw != nil {
				m.httpMiddlewares = append(m.httpMiddlewares, mw)
			}
		}

		slog.Info("插件初始化完成", "name", name, "type", f.Type())
	}

	return nil
}

// setupOne 初始化单个插件（带超时保护）。
func (m *Manager) setupOne(ctx context.Context, name string, f Factory, cfgNode *yaml.Node) error {
	ch := make(chan error, 1)
	go func() {
		ch <- f.Setup(ctx, name, &YamlNodeDecoder{Node: cfgNode})
	}()

	select {
	case err := <-ch:
		return err
	case <-time.After(SetupTimeout):
		return fmt.Errorf("插件 %q 初始化超时（%v）", name, SetupTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RunBeforeRequest 按顺序执行所有 BeforeRequest 钩子。
func (m *Manager) RunBeforeRequest(ctx context.Context, req *RequestInfo) (context.Context, error) {
	m.mu.RLock()
	hooks := m.beforeRequest
	m.mu.RUnlock()

	for _, hook := range hooks {
		var err error
		ctx, err = hook(ctx, req)
		if err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

// RunAfterResponse 按顺序执行所有 AfterResponse 钩子。
func (m *Manager) RunAfterResponse(ctx context.Context, req *RequestInfo, resp *ResponseInfo, err error) {
	m.mu.RLock()
	hooks := m.afterResponse
	m.mu.RUnlock()

	for _, hook := range hooks {
		hook(ctx, req, resp, err)
	}
}

// RunOnStreamChunk 按顺序执行所有 OnStreamChunk 钩子。
func (m *Manager) RunOnStreamChunk(ctx context.Context, req *RequestInfo, chunk *ResponseInfo) {
	m.mu.RLock()
	hooks := m.onStreamChunk
	m.mu.RUnlock()

	for _, hook := range hooks {
		hook(ctx, req, chunk)
	}
}

// HTTPMiddlewares 返回所有插件提供的 HTTP 中间件。
func (m *Manager) HTTPMiddlewares() []func(http.Handler) http.Handler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]func(http.Handler) http.Handler, len(m.httpMiddlewares))
	copy(result, m.httpMiddlewares)
	return result
}

// HasPlugins 返回是否有已初始化的插件。
func (m *Manager) HasPlugins() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.initialized) > 0
}

// CloseAll 按逆序关闭所有实现了 Closer 接口的插件。
func (m *Manager) CloseAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := len(m.initialized) - 1; i >= 0; i-- {
		p := m.initialized[i]
		if closer, ok := p.factory.(Closer); ok {
			if err := closer.Close(ctx); err != nil {
				slog.Error("关闭插件失败", "name", p.name, "error", err)
			} else {
				slog.Info("插件已关闭", "name", p.name)
			}
		}
	}
	return nil
}

// ReloadAll 对所有实现了 Reloadable 接口的插件执行热更新。
func (m *Manager) ReloadAll(ctx context.Context, configs map[string]yaml.Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, p := range m.initialized {
		reloadable, ok := p.factory.(Reloadable)
		if !ok {
			continue
		}
		cfgNode, exists := configs[p.name]
		if !exists {
			continue
		}
		if err := reloadable.Reload(ctx, &YamlNodeDecoder{Node: &cfgNode}); err != nil {
			slog.Error("热更新插件失败", "name", p.name, "error", err)
			return fmt.Errorf("热更新插件 %q 失败: %w", p.name, err)
		}
		slog.Info("插件热更新完成", "name", p.name)
	}

	// 重新收集钩子
	m.beforeRequest = m.beforeRequest[:0]
	m.afterResponse = m.afterResponse[:0]
	m.onStreamChunk = m.onStreamChunk[:0]
	m.httpMiddlewares = m.httpMiddlewares[:0]

	for _, p := range m.initialized {
		if hp, ok := p.factory.(HookProvider); ok {
			hooks := hp.Hooks()
			if hooks != nil {
				if hooks.BeforeRequest != nil {
					m.beforeRequest = append(m.beforeRequest, hooks.BeforeRequest)
				}
				if hooks.AfterResponse != nil {
					m.afterResponse = append(m.afterResponse, hooks.AfterResponse)
				}
				if hooks.OnStreamChunk != nil {
					m.onStreamChunk = append(m.onStreamChunk, hooks.OnStreamChunk)
				}
			}
		}
		if mp, ok := p.factory.(HTTPMiddlewareProvider); ok {
			if mw := mp.HTTPMiddleware(); mw != nil {
				m.httpMiddlewares = append(m.httpMiddlewares, mw)
			}
		}
	}

	return nil
}
