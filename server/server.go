// Package server 负责从配置一站式组装所有依赖并启动服务。
// 初始化顺序: Config → PluginManager → Proxy → Router → Server
package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-kratos/kratos/v2"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"

	"github.com/nomand-zc/lumin-client/log"
	"github.com/nomand-zc/lumin-proxy/config"
	"github.com/nomand-zc/lumin-proxy/handler"
	"github.com/nomand-zc/lumin-proxy/plugin"
	"github.com/nomand-zc/lumin-proxy/protocol"
	"github.com/nomand-zc/lumin-proxy/proxy"
)

// Server 持有服务运行所需的全部依赖。
type Server struct {
	Config        *config.Config
	httpServer    *kratoshttp.Server
	kratosApp     *kratos.App
	PluginManager plugin.LifecycleManager
	Proxy         proxy.Proxy
}

// New 根据配置初始化所有依赖，返回 Server 实例。
func New(ctx context.Context, cfg *config.Config, opts ...Option) (*Server, error) {
	s := &Server{
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
	s.PluginManager = pm

	// ② 初始化代理核心层
	proxyOpts := []proxy.DefaultProxyOption{
		proxy.WithHookRunner(pm),
	}
	if o.balancer != nil {
		proxyOpts = append(proxyOpts, proxy.WithBalancer(o.balancer))
	}
	if o.providerRegistry != nil {
		proxyOpts = append(proxyOpts, proxy.WithProviderRegistry(o.providerRegistry))
	}
	s.Proxy = proxy.NewDefaultProxy(proxyOpts...)

	// ③ 构建 HTTP Server
	httpServer, err := buildHTTPServer(cfg, pm, s.Proxy)
	if err != nil {
		return nil, fmt.Errorf("构建 HTTP Server 失败: %w", err)
	}
	s.httpServer = httpServer

	// ④ 构建 Kratos App
	s.kratosApp = kratos.New(
		kratos.Name("lumin-proxy"),
		kratos.Server(s.httpServer),
		kratos.AfterStop(func(ctx context.Context) error {
			log.Info("正在关闭插件...")
			return s.Shutdown(ctx)
		}),
	)

	return s, nil
}

// Run 启动服务（阻塞直到收到终止信号或出错）。
func (s *Server) Run() error {
	log.Infof("lumin-proxy 已启动, address=%s", s.Config.Server.Address)
	return s.kratosApp.Run()
}

// Shutdown 优雅关闭所有组件。
func (s *Server) Shutdown(ctx context.Context) error {
	if s.PluginManager != nil {
		s.PluginManager.CloseAll(ctx)
	}
	return nil
}

// buildHTTPServer 根据配置构建 Kratos HTTP Server。
func buildHTTPServer(cfg *config.Config, pm plugin.LifecycleManager, p proxy.Proxy) (*kratoshttp.Server, error) {
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
			log.Warnf("协议适配器未注册，跳过: name=%s", protoCfg.Name)
			continue
		}

		h := handler.NewProxyHandler(adapter, p)
		prefix := protoCfg.Prefix
		if prefix == "" {
			prefix = "/" + protoCfg.Name
		}

		// 由适配器声明路由规则
		for _, route := range adapter.Routes(h) {
			routeHandler := route.Handler
			if routeHandler == nil {
				routeHandler = h
			}
			if route.IsPrefix {
				srv.HandlePrefix(prefix+route.Pattern, routeHandler)
			} else {
				srv.Handle(prefix+route.Pattern, routeHandler)
			}
		}

		log.Infof("注册协议路由: protocol=%s, prefix=%s", protoCfg.Name, prefix)
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
					log.Errorf("请求处理 panic: error=%v, path=%s", err, r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"error":{"message":"Internal Server Error","type":"internal_error"}}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
