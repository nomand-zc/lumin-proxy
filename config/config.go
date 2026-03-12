package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是 lumin-proxy 的顶层配置结构体。
type Config struct {
	// Server HTTP 服务配置
	Server ServerConfig `yaml:"server"`
	// Proxy 代理核心配置
	Proxy ProxyConfig `yaml:"proxy"`
	// Plugins 插件配置列表
	Plugins PluginConfigs `yaml:"plugins"`
	// Admin 运维接口配置
	Admin AdminConfig `yaml:"admin"`
	// Log 日志配置
	Log LogConfig `yaml:"log"`
}

// ServerConfig HTTP 服务配置。
type ServerConfig struct {
	// Address 监听地址，如 ":8080"
	Address string `yaml:"address"`
	// ReadTimeout 读超时
	ReadTimeout time.Duration `yaml:"read_timeout"`
	// WriteTimeout 写超时（流式响应需要较长超时）
	WriteTimeout time.Duration `yaml:"write_timeout"`
	// IdleTimeout 空闲超时
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	// TLS TLS 配置（可选）
	TLS *TLSConfig `yaml:"tls"`
}

// TLSConfig TLS 配置。
type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// ProxyConfig 代理核心配置。
type ProxyConfig struct {
	// Protocols 协议适配器列表
	Protocols []ProtocolConfig `yaml:"protocols"`
}

// ProtocolConfig 协议适配器配置。
type ProtocolConfig struct {
	// Name 协议名称: "openai" | "anthropic"
	Name string `yaml:"name"`
	// Enable 是否启用
	Enable bool `yaml:"enable"`
	// Prefix 路由前缀，如 "/v1"
	Prefix string `yaml:"prefix"`
}

// PluginEntry 单个插件配置项，包含名称和配置内容。
type PluginEntry struct {
	// Name 插件名称
	Name string
	// Config 插件配置内容（延迟解码）
	Config yaml.Node
}

// PluginConfigs 有序的插件配置列表。
// YAML 中仍使用 map 格式书写，解码时保留声明顺序。
type PluginConfigs []PluginEntry

// UnmarshalYAML 自定义解码：将 YAML mapping 按声明顺序解码为有序切片。
func (pc *PluginConfigs) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("plugins 必须是映射类型")
	}
	// MappingNode 的 Content 是 [key, value, key, value, ...] 交替排列
	for i := 0; i+1 < len(value.Content); i += 2 {
		keyNode := value.Content[i]
		valNode := value.Content[i+1]
		*pc = append(*pc, PluginEntry{
			Name:   keyNode.Value,
			Config: *valNode,
		})
	}
	return nil
}

// ToMap 转换为 map 格式，用于需要按名称查找的场景（如热更新）。
func (pc PluginConfigs) ToMap() map[string]yaml.Node {
	m := make(map[string]yaml.Node, len(pc))
	for _, entry := range pc {
		m[entry.Name] = entry.Config
	}
	return m
}

// AdminConfig 运维接口配置。
type AdminConfig struct {
	// Enable 是否启用
	Enable bool `yaml:"enable"`
	// Address 监听地址，如 ":8081"
	Address string `yaml:"address"`
}

// LogConfig 日志配置。
type LogConfig struct {
	// Level 日志级别: "debug" | "info" | "warn" | "error"
	Level string `yaml:"level"`
}

// Load 从 YAML 文件加载配置。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("配置校验失败: %w", err)
	}
	return cfg, nil
}

// Validate 校验配置合法性。
func (c *Config) Validate() error {
	if c.Server.Address == "" {
		return fmt.Errorf("server.address 不能为空")
	}
	return nil
}

// setDefaults 填充默认值。
func (c *Config) setDefaults() {
	if c.Server.Address == "" {
		c.Server.Address = ":8080"
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 30 * time.Second
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 120 * time.Second
	}
	if c.Server.IdleTimeout == 0 {
		c.Server.IdleTimeout = 120 * time.Second
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Admin.Address == "" {
		c.Admin.Address = ":8081"
	}
}
