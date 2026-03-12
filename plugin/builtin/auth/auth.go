// Package auth 实现了 API Key 鉴权插件。
// 从 HTTP 请求的 Authorization 头中提取 API Key 并进行验证。
package auth

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/nomand-zc/lumin-client/log"
	"github.com/nomand-zc/lumin-proxy/plugin"
)

func init() {
	plugin.Register("auth", &AuthPlugin{})
}

// AuthConfig 鉴权插件配置。
type AuthConfig struct {
	// Keys 有效的 API Key 列表（简单模式）
	Keys []string `yaml:"keys"`
	// Enabled 是否启用鉴权，默认 true
	Enabled *bool `yaml:"enabled"`
	// SkipPaths 不需要鉴权的路径前缀列表
	SkipPaths []string `yaml:"skip_paths"`
}

// AuthPlugin API Key 鉴权插件。
type AuthPlugin struct {
	mu        sync.RWMutex
	keys      map[string]bool
	enabled   bool
	skipPaths []string
}

// Type 返回插件类型。
func (p *AuthPlugin) Type() string {
	return "auth"
}

// Setup 初始化插件。
func (p *AuthPlugin) Setup(ctx context.Context, name string, dec plugin.Decoder) error {
	cfg := &AuthConfig{}
	if err := dec.Decode(cfg); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.keys = make(map[string]bool, len(cfg.Keys))
	for _, key := range cfg.Keys {
		p.keys[key] = true
	}

	// 默认启用
	p.enabled = true
	if cfg.Enabled != nil {
		p.enabled = *cfg.Enabled
	}

	// 默认跳过路径
	p.skipPaths = cfg.SkipPaths
	if len(p.skipPaths) == 0 {
		p.skipPaths = []string{"/healthz", "/metrics", "/ready"}
	}

	log.Infof("鉴权插件初始化完成: keys_count=%d, enabled=%v", len(p.keys), p.enabled)
	return nil
}

// Hooks 返回钩子集合。
func (p *AuthPlugin) Hooks() *plugin.Hooks {
	return &plugin.Hooks{
		BeforeRequest: p.beforeRequest,
	}
}

// HTTPMiddleware 返回 HTTP 层鉴权中间件。
func (p *AuthPlugin) HTTPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 如果未启用鉴权，直接放行
			if !p.isEnabled() {
				next.ServeHTTP(w, r)
				return
			}

			// 跳过不需要鉴权的路径
			if p.shouldSkip(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// 从 Authorization 头提取 API Key
			apiKey := extractAPIKey(r)
			if apiKey == "" {
				writeUnauthorized(w, "缺少 API Key，请在 Authorization 头中提供 Bearer token")
				return
			}

			// 验证 API Key
			if !p.validateKey(apiKey) {
				writeUnauthorized(w, "无效的 API Key")
				return
			}

			// 将 API Key 和用户信息注入上下文
			ctx := context.WithValue(r.Context(), apiKeyContextKey, apiKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// beforeRequest 是 BeforeRequest 钩子实现。
func (p *AuthPlugin) beforeRequest(ctx context.Context, req *plugin.RequestInfo) (context.Context, error) {
	// 从上下文中获取 API Key（由 HTTP 中间件注入）
	apiKey, _ := ctx.Value(apiKeyContextKey).(string)
	if apiKey != "" {
		req.APIKey = apiKey
		req.UserID = apiKey // 简单模式下 APIKey 即用户标识
	}
	return ctx, nil
}

// Reload 热更新配置。
func (p *AuthPlugin) Reload(ctx context.Context, dec plugin.Decoder) error {
	cfg := &AuthConfig{}
	if err := dec.Decode(cfg); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.keys = make(map[string]bool, len(cfg.Keys))
	for _, key := range cfg.Keys {
		p.keys[key] = true
	}
	if cfg.Enabled != nil {
		p.enabled = *cfg.Enabled
	}

	log.Infof("鉴权插件热更新完成: keys_count=%d", len(p.keys))
	return nil
}

// Close 关闭插件。
func (p *AuthPlugin) Close(ctx context.Context) error {
	return nil
}

// --- 内部辅助函数 ---

type contextKey string

const apiKeyContextKey contextKey = "api_key"

func (p *AuthPlugin) isEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.enabled
}

func (p *AuthPlugin) shouldSkip(path string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, prefix := range p.skipPaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (p *AuthPlugin) validateKey(key string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	// 如果没有配置任何 key，则放行所有请求
	if len(p.keys) == 0 {
		return true
	}
	return p.keys[key]
}

func extractAPIKey(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	// 支持 "Bearer <key>" 格式
	const prefix = "Bearer "
	if strings.HasPrefix(auth, prefix) {
		return strings.TrimSpace(auth[len(prefix):])
	}
	return auth
}

func writeUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":{"message":"` + message + `","type":"authentication_error"}}`))
}

// GetAPIKeyFromContext 从上下文中获取 API Key。
func GetAPIKeyFromContext(ctx context.Context) string {
	key, _ := ctx.Value(apiKeyContextKey).(string)
	return key
}
