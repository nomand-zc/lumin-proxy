package protocol

import (
	"fmt"
	"sync"
)

// --- 协议适配器注册表 ---

var (
	adapterMu  sync.RWMutex
	adapters   = make(map[string]Adapter) // name => Adapter
)

// RegisterAdapter 注册一个协议适配器到全局注册表。
func RegisterAdapter(a Adapter) {
	adapterMu.Lock()
	defer adapterMu.Unlock()
	name := a.Name()
	if _, exists := adapters[name]; exists {
		panic(fmt.Sprintf("协议适配器 %q 已注册", name))
	}
	adapters[name] = a
}

// GetAdapter 根据名称获取已注册的协议适配器。
func GetAdapter(name string) (Adapter, bool) {
	adapterMu.RLock()
	defer adapterMu.RUnlock()
	a, ok := adapters[name]
	return a, ok
}

// ListAdapters 列出所有已注册的协议适配器名。
func ListAdapters() []string {
	adapterMu.RLock()
	defer adapterMu.RUnlock()
	names := make([]string, 0, len(adapters))
	for name := range adapters {
		names = append(names, name)
	}
	return names
}
