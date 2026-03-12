// Package http 封装 Kratos HTTP Server 的创建和生命周期管理。
// 只关注 HTTP 传输层的创建和启停，不涉及业务依赖编排。
package http

import (
	"net/http"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"

	"github.com/nomand-zc/lumin-client/log"
)

// ServerConfig HTTP Server 创建所需的配置。
type ServerConfig struct {
	// Address 监听地址
	Address string
	// WriteTimeout 写超时
	WriteTimeout time.Duration
	// Filters HTTP 过滤器（中间件）列表
	Filters []kratoshttp.FilterFunc
}

// NewServer 根据配置创建 Kratos HTTP Server 实例。
// 返回的 Server 还未注册路由，由 router 层负责注册。
func NewServer(cfg ServerConfig) *kratoshttp.Server {
	opts := []kratoshttp.ServerOption{
		kratoshttp.Address(cfg.Address),
	}

	// 添加中间件
	if len(cfg.Filters) > 0 {
		opts = append(opts, kratoshttp.Filter(cfg.Filters...))
	}

	// 写超时
	if cfg.WriteTimeout > 0 {
		opts = append(opts, kratoshttp.Timeout(cfg.WriteTimeout))
	}

	srv := kratoshttp.NewServer(opts...)
	log.Infof("HTTP Server 已创建: address=%s", cfg.Address)
	return srv
}

// RecoveryFilter 返回 panic 恢复中间件。
func RecoveryFilter() kratoshttp.FilterFunc {
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
