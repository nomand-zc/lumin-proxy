// Package app 负责从配置一站式编排所有依赖并启动服务。
// 初始化顺序: Config → PluginManager → ACPool → Proxy → Transport → Router → Run
package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudflare/tableflip"
	kratos "github.com/go-kratos/kratos/v2"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"

	"github.com/nomand-zc/lumin-client/log"
	"github.com/nomand-zc/lumin-proxy/acpool"
	"github.com/nomand-zc/lumin-proxy/config"
	"github.com/nomand-zc/lumin-proxy/plugin"
	"github.com/nomand-zc/lumin-proxy/proxy"
	"github.com/nomand-zc/lumin-proxy/router"
	transporthttp "github.com/nomand-zc/lumin-proxy/transport/http"
)

const (
	// pidFile PID 文件路径
	pidFile = "lumin-proxy.pid"
	// upgradeTimeout 新进程就绪超时时间
	upgradeTimeout = 60 * time.Second
	// stopTimeout 优雅关闭超时时间（等待存量请求排空）
	stopTimeout = 60 * time.Second

	// signalStart 启动命令
	signalStart = "start"
	// signalRestart 热重启命令
	signalRestart = "restart"
	// signalStop 优雅停止命令
	signalStop = "stop"
)

// App 持有服务运行所需的全部依赖。
// 负责依赖编排，而不关心具体传输层实现。
type App struct {
	Config        *config.Config
	opts          *options
	upg           *tableflip.Upgrader // tableflip 优雅重启管理器
	httpServer    *kratoshttp.Server
	kratosApp     *kratos.App
	PluginManager plugin.LifecycleManager
	Proxy         proxy.Proxy
	acpoolDeps    *acpool.Dependencies // 账号池依赖（由配置驱动自动构建）
}

// New 根据配置初始化所有依赖，返回 App 实例。
// 初始化顺序: Config → PluginManager → ACPool → Proxy → initServer(tableflip → Listener → HTTP Server → Router → Kratos App)
func New(ctx context.Context, cfg *config.Config, opts ...Option) (*App, error) {
	a := &App{
		Config: cfg,
	}

	// 应用选项
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	a.opts = o

	// 如果是 -s 命令模式，跳过业务依赖初始化（只需要发信号）
	if o.signal != "" && o.signal != signalStart {
		return a, nil
	}

	// ① 初始化插件管理器
	pm := plugin.NewManager()
	if len(cfg.Plugins) > 0 {
		if err := pm.SetupAll(ctx, cfg.Plugins); err != nil {
			return nil, fmt.Errorf("初始化插件失败: %w", err)
		}
	}
	a.PluginManager = pm

	// ② 从配置自动构建 acpool 依赖（Balancer + Storage）
	deps, err := acpool.Build(cfg.ACPool)
	if err != nil {
		return nil, fmt.Errorf("初始化 acpool 失败: %w", err)
	}
	a.acpoolDeps = deps

	// ③ 构建 Proxy
	proxyOpts := []proxy.DefaultProxyOption{
		proxy.WithHookRunner(pm),
		proxy.WithBalancer(deps.Balancer),
	}
	if o.providerRegistry != nil {
		proxyOpts = append(proxyOpts, proxy.WithProviderRegistry(o.providerRegistry))
	}
	a.Proxy = proxy.NewDefaultProxy(proxyOpts...)

	// ④ 初始化 Server（tableflip + Listener + HTTP Server + 路由 + Kratos App）
	if err := a.initServer(); err != nil {
		return nil, err
	}

	return a, nil
}

// initServer 初始化服务相关依赖：tableflip Upgrader、Listener、HTTP Server、路由、Kratos App。
// 这些组件存在依赖链（tableflip → Listener → HTTP Server → Kratos App），封装在一起。
func (a *App) initServer() error {
	// ① 初始化 tableflip Upgrader
	upg, err := tableflip.New(tableflip.Options{
		UpgradeTimeout: upgradeTimeout,
	})
	if err != nil {
		return fmt.Errorf("初始化优雅重启管理器失败: %w", err)
	}
	a.upg = upg

	// ② 通过 tableflip 获取或继承 Listener
	ln, err := upg.Listen("tcp", a.Config.Server.Address)
	if err != nil {
		return fmt.Errorf("监听地址失败: %w", err)
	}

	// ③ 构建 HTTP Server（注入 tableflip 管理的 Listener）
	filters := buildFilters(a.PluginManager)
	a.httpServer = transporthttp.NewServer(transporthttp.ServerConfig{
		Address:      a.Config.Server.Address,
		WriteTimeout: a.Config.Server.WriteTimeout,
		Filters:      filters,
		Listener:     ln,
	})

	// ④ 注册路由
	router.Register(a.httpServer, a.Config, a.Proxy, a.PluginManager)

	// ⑤ 构建 Kratos App
	// Signal() 不能传空（空参数会导致 signal.Notify 监听所有信号，服务会立刻退出）
	// 传入一个不会被外部常规触发的 SIGUSR2，由 App.waitForExit() 统一管理真正的退出信号
	// StopTimeout：控制 Shutdown 等待存量请求排空的超时时间
	// AfterStart：Server 启动后标记进程就绪 + 写 PID 文件
	a.kratosApp = kratos.New(
		kratos.Name("lumin-proxy"),
		kratos.Server(a.httpServer),
		kratos.Signal(syscall.SIGUSR2),
		kratos.StopTimeout(stopTimeout),
		kratos.AfterStart(func(ctx context.Context) error {
			// Server 已启动并开始 Accept，标记进程就绪（通知旧进程可以退出）
			if err := a.upg.Ready(); err != nil {
				return fmt.Errorf("标记进程就绪失败: %w", err)
			}
			if err := writePIDFile(); err != nil {
				log.Errorf("写入 PID 文件失败: %v", err)
			}
			log.Infof("lumin-proxy 已就绪, address=%s, pid=%d", a.Config.Server.Address, os.Getpid())
			return nil
		}),
	)

	return nil
}

// Run 启动服务（阻塞直到收到终止信号或出错）。
// kratosApp.Run() 在主线程阻塞，信号监听在独立 goroutine 中处理。
func (a *App) Run() error {
	// 处理 -s 命令
	if a.opts.signal != "" && a.opts.signal != signalStart {
		return a.handleSignalCommand(a.opts.signal)
	}

	// ① 启动信号监听（独立 goroutine）
	go a.waitForExit()

	// ② 主线程阻塞：启动 Kratos App（内部 errgroup.Wait 等待 Stop 或错误）
	// AfterStart 钩子会在 Server 启动后标记进程就绪 + 写 PID 文件
	err := a.kratosApp.Run()

	// ③ Run 返回后执行清理（业务组件 + tableflip + PID 文件）
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if shutdownErr := a.Shutdown(ctx); shutdownErr != nil {
		log.Errorf("关闭资源失败: %v", shutdownErr)
	}

	return err
}

// Shutdown 优雅关闭所有资源（业务组件 + tableflip + PID 文件）。
func (a *App) Shutdown(ctx context.Context) error {
	// 关闭业务组件
	if a.PluginManager != nil {
		a.PluginManager.CloseAll(ctx)
	}
	// 释放 acpool 资源（如数据库连接、Redis 连接等）
	if a.acpoolDeps != nil {
		if err := a.acpoolDeps.Close(); err != nil {
			log.Errorf("关闭 acpool 资源失败: %v", err)
		}
	}

	// 停止 tableflip Upgrader
	if a.upg != nil {
		a.upg.Stop()
	}

	// 清理 PID 文件
	removePIDFile()

	return nil
}

// ACPoolDeps 返回 acpool 依赖实例（供 admin API 等外部模块访问 Storage）。
func (a *App) ACPoolDeps() *acpool.Dependencies {
	return a.acpoolDeps
}

// waitForExit 统一监听信号，处理热重启和优雅关闭。
// 收到退出信号后调用 kratosApp.Stop()，使主线程的 Run() 返回。
func (a *App) waitForExit() {
	sigHUP := make(chan os.Signal, 1)
	signal.Notify(sigHUP, syscall.SIGHUP)

	sigTerm := make(chan os.Signal, 1)
	signal.Notify(sigTerm, syscall.SIGTERM, syscall.SIGINT)

	for {
		select {
		case <-sigHUP:
			// 收到 SIGHUP → 触发热重启（fork 新进程 + 传递 fd）
			log.Infof("收到 SIGHUP 信号, 开始优雅重启... pid=%d", os.Getpid())
			if err := a.upg.Upgrade(); err != nil {
				log.Errorf("优雅重启失败: %v", err)
			}

		case <-a.upg.Exit():
			// 新进程已就绪，旧进程优雅退出
			log.Infof("新进程已接管, 旧进程优雅退出中... pid=%d", os.Getpid())
			if err := a.kratosApp.Stop(); err != nil {
				log.Errorf("停止 Kratos App 失败: %v", err)
			}
			return

		case s := <-sigTerm:
			// 收到终止信号，直接优雅关闭
			log.Infof("收到信号 %v, 开始优雅关闭... pid=%d", s, os.Getpid())
			if err := a.kratosApp.Stop(); err != nil {
				log.Errorf("停止 Kratos App 失败: %v", err)
			}
			return
		}
	}
}

// handleSignalCommand 处理 -s 命令（restart / stop）。
func (a *App) handleSignalCommand(sig string) error {
	pid, err := readPIDFile()
	if err != nil {
		return fmt.Errorf("读取 PID 文件失败: %w（服务可能未运行）", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("查找进程失败: pid=%d, %w", pid, err)
	}

	switch strings.ToLower(sig) {
	case signalRestart:
		log.Infof("发送 SIGHUP 信号到进程 %d, 触发优雅重启...", pid)
		if err := process.Signal(syscall.SIGHUP); err != nil {
			return fmt.Errorf("发送 SIGHUP 信号失败: %w", err)
		}
		log.Infof("优雅重启信号已发送")

	case signalStop:
		log.Infof("发送 SIGTERM 信号到进程 %d, 触发优雅关闭...", pid)
		if err := process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("发送 SIGTERM 信号失败: %w", err)
		}
		log.Infof("优雅关闭信号已发送")

	default:
		return fmt.Errorf("未知的信号命令: %s（支持: %s, %s）", sig, signalRestart, signalStop)
	}

	return nil
}

// writePIDFile 将当前进程 PID 写入文件。
func writePIDFile() error {
	return os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)
}

// readPIDFile 从 PID 文件读取进程 ID。
func readPIDFile() (int, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// removePIDFile 删除 PID 文件。
func removePIDFile() {
	if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
		log.Errorf("删除 PID 文件失败: %v", err)
	}
}

// buildFilters 收集并构建 HTTP 中间件过滤器链。
func buildFilters(pm plugin.LifecycleManager) []kratoshttp.FilterFunc {
	var filters []kratoshttp.FilterFunc

	// 基础中间件：Recovery（始终排在最前面）
	filters = append(filters, transporthttp.RecoveryFilter())

	// 收集插件提供的 HTTP 中间件
	for _, mw := range pm.HTTPMiddlewares() {
		filters = append(filters, kratoshttp.FilterFunc(mw))
	}

	return filters
}
