// Package app 负责从配置一站式编排所有依赖并启动服务。
// 初始化顺序: Config → PluginManager → ACPool → Proxy → Transport → Router → Run
package app

import (
	"context"
	"fmt"

	kratos "github.com/go-kratos/kratos/v2"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"

	"github.com/nomand-zc/lumin-client/log"
	"github.com/nomand-zc/lumin-proxy/acpool"
	"github.com/nomand-zc/lumin-proxy/config"
	"github.com/nomand-zc/lumin-proxy/plugin"
	"github.com/nomand-zc/lumin-proxy/proxy"
	"github.com/nomand-zc/lumin-proxy/router"
	transporthttp "github.com/nomand-zc/lumin-proxy/transport/http"
)

// App 持有服务运行所需的全部依赖。
// 负责依赖编排，而不关心具体传输层实现。
type App struct {
	Config        *config.Config
	httpServer    *kratoshttp.Server
	kratosApp     *kratos.App
	PluginManager plugin.LifecycleManager
	Proxy         proxy.Proxy
	acpoolDeps    *acpool.Dependencies // 账号池依赖（由配置驱动自动构建）
}

// New 根据配置初始化所有依赖，返回 App 实例。
// 编排顺序: PluginManager → ACPool → Proxy → HTTP Server → Router → Kratos App
func New(ctx context.Context, cfg *config.Config, opts ...Option) (*App, error) {
	a := &App{
		Config: cfg,
	}

	// 应用选项
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	// ① 初始化插件管理器
	pm := plugin.NewManager()
	if len(cfg.Plugins) > 0 {
		if err := pm.SetupAll(ctx, cfg.Plugins); err != nil {
			return nil, fmt.Errorf("初始化插件失败: %w", err)
		}
	}
	a.PluginManager = pm

	// ② 从配置自动构建 acpool 依赖（Balancer + Storage）
	deps, err := acpool.Build(cfg.ACPool)
	if err != nil {
		return nil, fmt.Errorf("初始化 acpool 失败: %w", err)
	}
	a.acpoolDeps = deps

	// ③ 构建 Proxy
	proxyOpts := []proxy.DefaultProxyOption{
		proxy.WithHookRunner(pm),
		proxy.WithBalancer(deps.Balancer),
	}
	if o.providerRegistry != nil {
		proxyOpts = append(proxyOpts, proxy.WithProviderRegistry(o.providerRegistry))
	}
	a.Proxy = proxy.NewDefaultProxy(proxyOpts...)

	// ④ 构建 HTTP Server（传输层）
	filters := buildFilters(pm)
	a.httpServer = transporthttp.NewServer(transporthttp.ServerConfig{
		Address:      cfg.Server.Address,
		WriteTimeout: cfg.Server.WriteTimeout,
		Filters:      filters,
	})

	// ⑤ 注册路由
	router.Register(a.httpServer, cfg, a.Proxy, pm)

	// ⑥ 构建 Kratos App（生命周期管理）
	a.kratosApp = kratos.New(
		kratos.Name("lumin-proxy"),
		kratos.Server(a.httpServer),
		kratos.AfterStop(func(ctx context.Context) error {
			log.Info("正在关闭服务...")
			return a.Shutdown(ctx)
		}),
	)

	return a, nil
}

// Run 启动服务（阻塞直到收到终止信号或出错）。
func (a *App) Run() error {
	log.Infof("lumin-proxy 已启动, address=%s", a.Config.Server.Address)
	return a.kratosApp.Run()
}

// Shutdown 优雅关闭所有组件。
func (a *App) Shutdown(ctx context.Context) error {
	if a.PluginManager != nil {
		a.PluginManager.CloseAll(ctx)
	}
	// 释放 acpool 资源（如数据库连接、Redis 连接等）
	if a.acpoolDeps != nil {
		if err := a.acpoolDeps.Close(); err != nil {
			log.Errorf("关闭 acpool 资源失败: %v", err)
		}
	}
	return nil
}

// ACPoolDeps 返回 acpool 依赖实例（供 admin API 等外部模块访问 Storage）。
func (a *App) ACPoolDeps() *acpool.Dependencies {
	return a.acpoolDeps
}

// buildFilters 收集并构建 HTTP 中间件过滤器链。
func buildFilters(pm plugin.LifecycleManager) []kratoshttp.FilterFunc {
	var filters []kratoshttp.FilterFunc

	// 基础中间件：Recovery（始终排在最前面）
	filters = append(filters, transporthttp.RecoveryFilter())

	// 收集插件提供的 HTTP 中间件
	for _, mw := range pm.HTTPMiddlewares() {
		filters = append(filters, kratoshttp.FilterFunc(mw))
	}

	return filters
}
