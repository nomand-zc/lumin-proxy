// Package bootstrap 负责从配置一站式组装所有依赖。
// 初始化顺序: Config → PluginManager → Proxy → Router → Server
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"

	"github.com/nomand-zc/lumin-proxy/config"
	"github.com/nomand-zc/lumin-proxy/handler"
	"github.com/nomand-zc/lumin-proxy/plugin"
	"github.com/nomand-zc/lumin-proxy/protocol"
	"github.com/nomand-zc/lumin-proxy/proxy"
)

// App 持有服务运行所需的全部依赖。
type App struct {
	Config        *config.Config
	HTTPServer    *kratoshttp.Server
	PluginManager *plugin.Manager
	Proxy         proxy.Proxy
}

// Init 根据配置初始化所有依赖。
func Init(ctx context.Context, cfg *config.Config, opts ...Option) (*App, error) {
	app := &App{
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
	app.PluginManager = pm

	// ② 初始化代理核心层
	proxyOpts := []proxy.DefaultProxyOption{
		proxy.WithPluginManager(pm),
	}
	if o.balancer != nil {
		proxyOpts = append(proxyOpts, proxy.WithBalancer(o.balancer))
	}
	if o.providerRegistry != nil {
		proxyOpts = append(proxyOpts, proxy.WithProviderRegistry(o.providerRegistry))
	}
	app.Proxy = proxy.NewDefaultProxy(proxyOpts...)

	// ③ 构建 HTTP Server
	httpServer, err := buildHTTPServer(cfg, pm, app.Proxy)
	if err != nil {
		return nil, fmt.Errorf("构建 HTTP Server 失败: %w", err)
	}
	app.HTTPServer = httpServer

	return app, nil
}

// Shutdown 优雅关闭所有组件。
func (a *App) Shutdown(ctx context.Context) error {
	if a.PluginManager != nil {
		a.PluginManager.CloseAll(ctx)
	}
	return nil
}

// buildHTTPServer 根据配置构建 Kratos HTTP Server。
func buildHTTPServer(cfg *config.Config, pm *plugin.Manager, p proxy.Proxy) (*kratoshttp.Server, error) {
	// 收集插件提供的 HTTP 中间件
	var filters []kratoshttp.FilterFunc
	for _, mw := range pm.HTTPMiddlewares() {
		filters = append(filters, kratoshttp.FilterFunc(mw))
	}

	// 添加基础中间件：Recovery
	filters = append([]kratoshttp.FilterFunc{recoveryFilter()}, filters...)

	// 创建 Kratos HTTP Server
	serverOpts := []kratoshttp.ServerOption{
		kratoshttp.Address(cfg.Server.Address),
		kratoshttp.Filter(filters...),
	}
	if cfg.Server.WriteTimeout > 0 {
		serverOpts = append(serverOpts, kratoshttp.Timeout(cfg.Server.WriteTimeout))
	}

	srv := kratoshttp.NewServer(serverOpts...)

	// 注册路由
	registerRoutes(srv, cfg, p)

	return srv, nil
}

// registerRoutes 根据配置注册协议路由。
func registerRoutes(srv *kratoshttp.Server, cfg *config.Config, p proxy.Proxy) {
	for _, protoCfg := range cfg.Proxy.Protocols {
		if !protoCfg.Enable {
			continue
		}

		adapter, ok := protocol.GetAdapter(protoCfg.Name)
		if !ok {
			slog.Warn("协议适配器未注册，跳过", "name", protoCfg.Name)
			continue
		}

		h := handler.NewProxyHandler(adapter, p)
		prefix := protoCfg.Prefix
		if prefix == "" {
			prefix = "/" + protoCfg.Name
		}

		// 注册 chat completions 路由
		switch protoCfg.Name {
		case "openai":
			srv.Handle(prefix+"/chat/completions", h)
			srv.HandleFunc(prefix+"/models", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"object":"list","data":[]}`))
			})
		default:
			srv.HandlePrefix(prefix, h)
		}

		slog.Info("注册协议路由", "protocol", protoCfg.Name, "prefix", prefix)
	}

	// 健康检查
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
}

// recoveryFilter 返回 panic 恢复中间件。
func recoveryFilter() kratoshttp.FilterFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					slog.Error("请求处理 panic", "error", err, "path", r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"error":{"message":"Internal Server Error","type":"internal_error"}}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
