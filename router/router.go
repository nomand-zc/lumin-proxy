// Package router 独立管理路由注册逻辑。
// 将协议适配器与代理核心层通过路由连接起来，支持插件的路由级钩子扩展。
package router

import (
	"context"
	"net/http"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"

	"github.com/nomand-zc/lumin-client/log"
	"github.com/nomand-zc/lumin-proxy/config"
	"github.com/nomand-zc/lumin-proxy/plugin"
	"github.com/nomand-zc/lumin-proxy/protocol"
	"github.com/nomand-zc/lumin-proxy/proxy"
)

// Register 根据配置注册所有协议路由到 HTTP Server。
// 支持通过 PluginManager 的路由级钩子进行扩展。
func Register(srv *kratoshttp.Server, cfg *config.Config, p proxy.Proxy, pm plugin.LifecycleManager) {
	for _, protoCfg := range cfg.Proxy.Protocols {
		if !protoCfg.Enable {
			continue
		}

		adapter, ok := protocol.GetAdapter(protoCfg.Name)
		if !ok {
			log.Warnf("协议适配器未注册，跳过: name=%s", protoCfg.Name)
			continue
		}

		h := NewProxyHandler(adapter, p)
		prefix := protoCfg.Prefix
		if prefix == "" {
			prefix = "/" + protoCfg.Name
		}

		// 触发路由级钩子（如果插件管理器支持）
		if rp, ok := pm.(plugin.RouteHookProvider); ok {
			rp.RunOnRouteRegister(protoCfg.Name, prefix)
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

// ProxyHandler 将协议适配器与代理核心层连接起来的 HTTP Handler。
type ProxyHandler struct {
	adapter protocol.Adapter
	proxy   proxy.Proxy
}

// NewProxyHandler 创建一个新的 ProxyHandler。
func NewProxyHandler(adapter protocol.Adapter, p proxy.Proxy) *ProxyHandler {
	return &ProxyHandler{
		adapter: adapter,
		proxy:   p,
	}
}

// ServeHTTP 实现 http.Handler 接口。
func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// ① 解析请求
	req, err := h.adapter.ParseRequest(r)
	if err != nil {
		log.Errorf("解析请求失败: error=%v, protocol=%s", err, h.adapter.Name())
		h.adapter.WriteError(w, err)
		return
	}

	// ② 根据是否流式分别处理
	if req.Stream {
		h.handleStream(ctx, w, req)
	} else {
		h.handleNonStream(ctx, w, req)
	}
}

// handleNonStream 处理非流式请求。
func (h *ProxyHandler) handleNonStream(ctx context.Context, w http.ResponseWriter, req *protocol.Request) {
	result, err := h.proxy.Handle(ctx, req)
	if err != nil {
		log.Errorf("代理请求失败: error=%v, model=%s", err, req.Model)
		h.adapter.WriteError(w, err)
		return
	}

	if err := h.adapter.WriteResponse(ctx, w, result.Response); err != nil {
		log.Errorf("写入响应失败: error=%v", err)
	}
}

// handleStream 处理流式请求。
func (h *ProxyHandler) handleStream(ctx context.Context, w http.ResponseWriter, req *protocol.Request) {
	result, err := h.proxy.HandleStream(ctx, req)
	if err != nil {
		log.Errorf("流式代理请求失败: error=%v, model=%s", err, req.Model)
		h.adapter.WriteError(w, err)
		return
	}

	if err := h.adapter.WriteStreamResponse(ctx, w, result.Stream); err != nil {
		log.Errorf("写入流式响应失败: error=%v", err)
	}
}
