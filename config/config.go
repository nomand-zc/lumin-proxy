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

// PluginConfigs 插件配置列表。
type PluginConfigs map[string]yaml.Node

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
