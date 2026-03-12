package app

import (
	"github.com/nomand-zc/lumin-proxy/proxy"
)

// Option 是 App 的配置选项。
type Option func(*options)

type options struct {
	providerRegistry proxy.ProviderRegistry
	signal           string // 信号命令：restart / stop（-s 参数）
}

// WithProviderRegistry 设置 ProviderRegistry 实例。
func WithProviderRegistry(r proxy.ProviderRegistry) Option {
	return func(o *options) {
		o.providerRegistry = r
	}
}

// WithSignal 设置信号命令（-s 参数：restart / stop）。
func WithSignal(sig string) Option {
	return func(o *options) {
		o.signal = sig
	}
}
