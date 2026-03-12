package bootstrap

import (
	"github.com/nomand-zc/lumin-acpool/balancer"
	"github.com/nomand-zc/lumin-proxy/proxy"
)

// Option 是 Bootstrap 的配置选项。
type Option func(*options)

type options struct {
	balancer         balancer.Balancer
	providerRegistry proxy.ProviderRegistry
}

// WithBalancer 设置 Balancer 实例。
func WithBalancer(b balancer.Balancer) Option {
	return func(o *options) {
		o.balancer = b
	}
}

// WithProviderRegistry 设置 ProviderRegistry 实例。
func WithProviderRegistry(r proxy.ProviderRegistry) Option {
	return func(o *options) {
		o.providerRegistry = r
	}
}
