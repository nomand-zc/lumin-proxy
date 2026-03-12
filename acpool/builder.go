// Package acpool 提供 lumin-acpool 接入 lumin-proxy 的桥接层。
// 通过配置驱动自动构建 Balancer 及其全部依赖组件，
// 将 YAML 配置中的策略名称映射为具体的策略实例。
package acpool

import (
	"fmt"
	"io"

	"github.com/nomand-zc/lumin-acpool/balancer"
	"github.com/nomand-zc/lumin-acpool/balancer/occupancy"
	"github.com/nomand-zc/lumin-acpool/circuitbreaker"
	"github.com/nomand-zc/lumin-acpool/cooldown"
	"github.com/nomand-zc/lumin-acpool/selector"
	accountstrategies "github.com/nomand-zc/lumin-acpool/selector/strategies/account"
	groupstrategies "github.com/nomand-zc/lumin-acpool/selector/strategies/group"
	"github.com/nomand-zc/lumin-acpool/storage"
	"github.com/nomand-zc/lumin-acpool/storage/memory/accountstore"
	"github.com/nomand-zc/lumin-acpool/storage/memory/providerstore"
	"github.com/nomand-zc/lumin-acpool/storage/memory/statsstore"
	"github.com/nomand-zc/lumin-acpool/storage/memory/usagestore"
	"github.com/nomand-zc/lumin-acpool/usagetracker"
	"github.com/nomand-zc/lumin-proxy/config"
)

// Dependencies 持有 acpool 初始化后的全部依赖。
// Server 层可通过此结构体访问 Balancer 和 Storage，
// 后者在 admin API 管理账号时需要直接使用。
type Dependencies struct {
	// Balancer 负载均衡器实例
	Balancer balancer.Balancer
	// AccountStorage 账号存储（admin API 管理账号时使用）
	AccountStorage storage.AccountStorage
	// ProviderStorage 供应商存储（admin API 管理供应商时使用）
	ProviderStorage storage.ProviderStorage
	// StatsStore 运行时统计存储
	StatsStore storage.StatsStore
	// UsageTracker 用量追踪器
	UsageTracker usagetracker.UsageTracker
	// usageStore 用量存储（构建过程中内部使用，不对外暴露）
	usageStore storage.UsageStore
	// closers 需要在关闭时释放的资源（如数据库连接）
	closers []io.Closer
}

// Close 释放全部资源（如数据库连接、Redis 连接等）。
func (d *Dependencies) Close() error {
	var firstErr error
	for _, c := range d.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Build 根据 ACPoolConfig 构建 Balancer 及其全部依赖。
// 这是整个 acpool 桥接层的核心函数，负责将 YAML 配置映射为具体的策略实例。
func Build(cfg config.ACPoolConfig) (*Dependencies, error) {
	deps := &Dependencies{}

	// ① 构建 Storage 层
	if err := deps.buildStorage(cfg.Storage); err != nil {
		return nil, fmt.Errorf("acpool: 构建存储层失败: %w", err)
	}

	// ② 组装 balancer.Option 列表
	opts := []balancer.Option{
		balancer.WithAccountStorage(deps.AccountStorage),
		balancer.WithProviderStorage(deps.ProviderStorage),
	}

	// StatsStore
	if deps.StatsStore != nil {
		opts = append(opts, balancer.WithStatsStore(deps.StatsStore))
	}

	// Selector（账号级选择策略）
	sel, err := deps.buildSelector(cfg.Balancer.Selector)
	if err != nil {
		return nil, err
	}
	if sel != nil {
		opts = append(opts, balancer.WithSelector(sel))
	}

	// GroupSelector（供应商级选择策略）
	gsel, err := deps.buildGroupSelector(cfg.Balancer.GroupSelector)
	if err != nil {
		return nil, err
	}
	if gsel != nil {
		opts = append(opts, balancer.WithGroupSelector(gsel))
	}

	// CircuitBreaker（熔断器）
	if cfg.Balancer.CircuitBreaker != nil && deps.StatsStore != nil {
		cb, err := deps.buildCircuitBreaker(cfg.Balancer.CircuitBreaker)
		if err != nil {
			return nil, err
		}
		opts = append(opts, balancer.WithCircuitBreaker(cb))
	}

	// CooldownManager（冷却管理器）
	if cfg.Balancer.Cooldown != nil {
		cm := deps.buildCooldownManager(cfg.Balancer.Cooldown)
		opts = append(opts, balancer.WithCooldownManager(cm))
	}

	// UsageTracker（用量追踪器）
	// 注意：若配了 Cooldown 但未配 UsageTracker，balancer.New() 内部会自动创建带冷却回调的实例
	if cfg.Balancer.UsageTracker != nil {
		deps.buildUsageTracker(cfg.Balancer.UsageTracker)
		opts = append(opts, balancer.WithUsageTracker(deps.UsageTracker))
	}

	// OccupancyController（占用控制器）
	oc, err := deps.buildOccupancy(cfg.Balancer.Occupancy)
	if err != nil {
		return nil, err
	}
	if oc != nil {
		opts = append(opts, balancer.WithOccupancyController(oc))
	}

	// 默认重试与故障转移
	if cfg.Balancer.DefaultMaxRetries > 0 {
		opts = append(opts, balancer.WithDefaultMaxRetries(cfg.Balancer.DefaultMaxRetries))
	}
	if cfg.Balancer.DefaultEnableFailover {
		opts = append(opts, balancer.WithDefaultFailover(true))
	}

	// ③ 构建 Balancer
	b, err := balancer.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("acpool: 构建 Balancer 失败: %w", err)
	}
	deps.Balancer = b

	return deps, nil
}

// --- Storage 构建 ---

// buildStorage 根据配置构建存储层的各个子 Store，并填充到 Dependencies 中。
func (d *Dependencies) buildStorage(cfg config.StorageDriverConfig) error {
	switch cfg.Driver {
	case "", "memory":
		d.AccountStorage = accountstore.NewStore()
		d.ProviderStorage = providerstore.NewStore()
		d.StatsStore = statsstore.NewMemoryStatsStore()
		d.usageStore = usagestore.NewMemoryUsageStore()
		return nil
	case "mysql":
		// TODO: 根据 cfg.DSN 构建 MySQL 存储实现
		return fmt.Errorf("acpool: mysql 存储驱动暂未实现")
	case "sqlite":
		// TODO: 根据 cfg.DSN 构建 SQLite 存储实现
		return fmt.Errorf("acpool: sqlite 存储驱动暂未实现")
	case "redis":
		// TODO: 根据 cfg.Addr 构建 Redis 存储实现
		return fmt.Errorf("acpool: redis 存储驱动暂未实现")
	default:
		return fmt.Errorf("acpool: 不支持的存储驱动: %s", cfg.Driver)
	}
}

// --- Selector 构建 ---

// buildSelector 根据策略名称构建账号级选择器。
func (d *Dependencies) buildSelector(name string) (selector.Selector, error) {
	switch name {
	case "", "round_robin":
		// 默认策略，返回 nil 让 balancer.New() 使用内置默认值
		if name == "" {
			return nil, nil
		}
		return accountstrategies.NewRoundRobin(), nil
	case "priority":
		return accountstrategies.NewPriority(), nil
	case "weighted":
		return accountstrategies.NewWeighted(), nil
	case "least_used":
		if d.StatsStore == nil {
			return nil, fmt.Errorf("acpool: least_used 策略需要 StatsStore 支持")
		}
		return accountstrategies.NewLeastUsed(d.StatsStore), nil
	case "affinity":
		return accountstrategies.NewAffinity(), nil
	default:
		return nil, fmt.Errorf("acpool: 不支持的 selector 策略: %s", name)
	}
}

// buildGroupSelector 根据策略名称构建供应商级选择器。
func (d *Dependencies) buildGroupSelector(name string) (selector.GroupSelector, error) {
	switch name {
	case "", "group_priority":
		// 默认策略，返回 nil 让 balancer.New() 使用内置默认值
		if name == "" {
			return nil, nil
		}
		return groupstrategies.NewGroupPriority(), nil
	case "group_round_robin":
		return groupstrategies.NewGroupRoundRobin(), nil
	case "group_weighted":
		return groupstrategies.NewGroupWeighted(), nil
	case "group_most_available":
		return groupstrategies.NewGroupMostAvailable(), nil
	case "group_affinity":
		return groupstrategies.NewGroupAffinity(), nil
	default:
		return nil, fmt.Errorf("acpool: 不支持的 group_selector 策略: %s", name)
	}
}

// --- CircuitBreaker 构建 ---

// buildCircuitBreaker 根据配置构建熔断器。
func (d *Dependencies) buildCircuitBreaker(cfg *config.CBConfig) (circuitbreaker.CircuitBreaker, error) {
	cbOpts := []circuitbreaker.Option{
		circuitbreaker.WithStatsStore(d.StatsStore),
	}
	if cfg.DefaultThreshold > 0 {
		cbOpts = append(cbOpts, circuitbreaker.WithDefaultThreshold(cfg.DefaultThreshold))
	}
	if cfg.DefaultTimeout > 0 {
		cbOpts = append(cbOpts, circuitbreaker.WithDefaultTimeout(cfg.DefaultTimeout))
	}
	if cfg.ThresholdRatio > 0 {
		cbOpts = append(cbOpts, circuitbreaker.WithThresholdRatio(cfg.ThresholdRatio))
	}
	if cfg.MinThreshold > 0 {
		cbOpts = append(cbOpts, circuitbreaker.WithMinThreshold(cfg.MinThreshold))
	}
	cb, err := circuitbreaker.NewCircuitBreaker(cbOpts...)
	if err != nil {
		return nil, fmt.Errorf("acpool: 构建熔断器失败: %w", err)
	}
	return cb, nil
}

// --- CooldownManager 构建 ---

// buildCooldownManager 根据配置构建冷却管理器。
func (d *Dependencies) buildCooldownManager(cfg *config.CooldownConfig) cooldown.CooldownManager {
	var cmOpts []cooldown.Option
	if cfg.DefaultDuration > 0 {
		cmOpts = append(cmOpts, cooldown.WithDefaultDuration(cfg.DefaultDuration))
	}
	return cooldown.NewCooldownManager(cmOpts...)
}

// --- UsageTracker 构建 ---

// buildUsageTracker 根据配置构建用量追踪器，并设置到 Dependencies 中。
func (d *Dependencies) buildUsageTracker(cfg *config.UTConfig) {
	var utOpts []usagetracker.Option
	if cfg.SafetyRatio > 0 {
		utOpts = append(utOpts, usagetracker.WithSafetyRatio(cfg.SafetyRatio))
	}
	if d.usageStore != nil {
		utOpts = append(utOpts, usagetracker.WithUsageStore(d.usageStore))
	}
	d.UsageTracker = usagetracker.NewUsageTracker(utOpts...)
}

// --- Occupancy 构建 ---

// buildOccupancy 根据配置构建占用控制器。
func (d *Dependencies) buildOccupancy(cfg config.OccupancyConfig) (occupancy.Controller, error) {
	switch cfg.Strategy {
	case "", "unlimited":
		// 默认策略，返回 nil 让 balancer.New() 使用内置默认值
		if cfg.Strategy == "" {
			return nil, nil
		}
		return occupancy.NewUnlimited(), nil
	case "fixed_limit":
		limit := cfg.DefaultLimit
		if limit <= 0 {
			limit = 5 // 默认并发上限
		}
		return occupancy.NewFixedLimit(limit), nil
	case "adaptive_limit":
		if d.UsageTracker == nil {
			return nil, fmt.Errorf("acpool: adaptive_limit 策略需要配置 usage_tracker")
		}
		var adaptiveOpts []occupancy.AdaptiveLimitOption
		if cfg.Factor > 0 {
			adaptiveOpts = append(adaptiveOpts, occupancy.WithFactor(cfg.Factor))
		}
		if cfg.MinLimit > 0 {
			adaptiveOpts = append(adaptiveOpts, occupancy.WithMinLimit(cfg.MinLimit))
		}
		if cfg.MaxLimit > 0 {
			adaptiveOpts = append(adaptiveOpts, occupancy.WithMaxLimit(cfg.MaxLimit))
		}
		if cfg.FallbackLimit > 0 {
			adaptiveOpts = append(adaptiveOpts, occupancy.WithFallbackLimit(cfg.FallbackLimit))
		}
		return occupancy.NewAdaptiveLimit(d.UsageTracker, adaptiveOpts...), nil
	default:
		return nil, fmt.Errorf("acpool: 不支持的 occupancy 策略: %s", cfg.Strategy)
	}
}
