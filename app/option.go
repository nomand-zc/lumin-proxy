package app

import (
	"github.com/nomand-zc/lumin-proxy/proxy"
)

// Option 是 App 的配置选项。
type Option func(*options)

type options struct {
	providerRegistry proxy.ProviderRegistry
}

// WithProviderRegistry 设置 ProviderRegistry 实例。
func WithProviderRegistry(r proxy.ProviderRegistry) Option {
	return func(o *options) {
		o.providerRegistry = r
	}
}
