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
	// ACPool 账号池配置（lumin-acpool）
	ACPool ACPoolConfig `yaml:"acpool"`
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

// --- ACPool 配置结构体 ---

// ACPoolConfig 账号池（lumin-acpool）配置。
// 通过配置驱动自动构建 Balancer 及其全部依赖组件。
type ACPoolConfig struct {
	// Storage 存储驱动配置
	Storage StorageDriverConfig `yaml:"storage"`
	// Balancer 负载均衡行为配置
	Balancer BalancerConfig `yaml:"balancer"`
}

// StorageDriverConfig 存储驱动配置。
type StorageDriverConfig struct {
	// Driver 存储驱动类型: "memory" | "mysql" | "sqlite" | "redis"
	Driver string `yaml:"driver"`
	// DSN 数据源名称，统一使用 DSN 格式配置所有存储驱动
	// MySQL 示例: "user:password@tcp(127.0.0.1:3306)/dbname?parseTime=true"
	// SQLite 示例: "./data/acpool.db"
	// Redis 示例: "redis://:password@127.0.0.1:6379/0"
	DSN string `yaml:"dsn"`
	// MaxOpenConns 最大打开连接数（MySQL 场景使用）
	MaxOpenConns int `yaml:"max_open_conns"`
	// MaxIdleConns 最大空闲连接数（MySQL 场景使用）
	MaxIdleConns int `yaml:"max_idle_conns"`
}

// BalancerConfig Balancer 行为配置。
type BalancerConfig struct {
	// Selector 账号级选择策略: "round_robin" | "priority" | "weighted" | "least_used" | "affinity"
	Selector string `yaml:"selector"`
	// GroupSelector 供应商级选择策略: "group_priority" | "group_round_robin" | "group_weighted" | "group_most_available" | "group_affinity"
	GroupSelector string `yaml:"group_selector"`
	// Occupancy 占用控制策略配置
	Occupancy OccupancyConfig `yaml:"occupancy"`
	// CircuitBreaker 熔断器配置（nil 表示不启用）
	CircuitBreaker *CBConfig `yaml:"circuit_breaker"`
	// Cooldown 冷却管理器配置（nil 表示不启用）
	Cooldown *CooldownConfig `yaml:"cooldown"`
	// UsageTracker 用量追踪器配置（nil 表示不启用）
	UsageTracker *UTConfig `yaml:"usage_tracker"`
	// DefaultMaxRetries 默认最大重试次数
	DefaultMaxRetries int `yaml:"default_max_retries"`
	// DefaultEnableFailover 是否默认启用故障转移
	DefaultEnableFailover bool `yaml:"default_enable_failover"`
}

// OccupancyConfig 占用控制策略配置。
type OccupancyConfig struct {
	// Strategy 策略类型: "unlimited" | "fixed_limit" | "adaptive_limit"
	Strategy string `yaml:"strategy"`
	// DefaultLimit 固定并发上限（fixed_limit 专用）
	DefaultLimit int64 `yaml:"default_limit"`
	// Factor 调控因子（adaptive_limit 专用，默认 1.0）
	Factor float64 `yaml:"factor"`
	// MinLimit 最小并发上限（adaptive_limit 专用，默认 1）
	MinLimit int64 `yaml:"min_limit"`
	// MaxLimit 最大并发上限（adaptive_limit 专用，默认 0 不限制）
	MaxLimit int64 `yaml:"max_limit"`
	// FallbackLimit 回退并发上限（adaptive_limit 专用，默认 1）
	FallbackLimit int64 `yaml:"fallback_limit"`
}

// CBConfig 熔断器配置。
type CBConfig struct {
	// DefaultThreshold 默认连续失败阈值
	DefaultThreshold int `yaml:"default_threshold"`
	// DefaultTimeout 默认熔断恢复超时时间
	DefaultTimeout time.Duration `yaml:"default_timeout"`
	// ThresholdRatio 动态阈值比例（默认 0.5）
	ThresholdRatio float64 `yaml:"threshold_ratio"`
	// MinThreshold 最小阈值（默认 3）
	MinThreshold int `yaml:"min_threshold"`
}

// CooldownConfig 冷却管理器配置。
type CooldownConfig struct {
	// DefaultDuration 默认冷却时长
	DefaultDuration time.Duration `yaml:"default_duration"`
}

// UTConfig 用量追踪器配置。
type UTConfig struct {
	// SafetyRatio 安全阈值比例（0.0 ~ 1.0，默认 0.95）
	SafetyRatio float64 `yaml:"safety_ratio"`
}
