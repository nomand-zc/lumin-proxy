// Package handler 提供了将协议适配器和代理核心层连接起来的 HTTP Handler。
package handler

import (
	"context"
	"net/http"

	"github.com/nomand-zc/lumin-client/log"
	"github.com/nomand-zc/lumin-proxy/protocol"
	"github.com/nomand-zc/lumin-proxy/proxy"
)

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
