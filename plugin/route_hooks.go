package plugin

// RouteHookProvider 路由级钩子提供者接口。
// 当路由注册时，router 层通过此接口通知插件系统。
type RouteHookProvider interface {
	// RunOnRouteRegister 在路由注册时触发。
	// protocolName 是协议名称，prefix 是路由前缀。
	RunOnRouteRegister(protocolName string, prefix string)
}

// OnRouteRegisterHook 路由注册钩子类型。
type OnRouteRegisterHook func(protocolName string, prefix string)
