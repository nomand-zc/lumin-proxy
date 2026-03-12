package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"github.com/go-kratos/kratos/v2"

	"github.com/nomand-zc/lumin-proxy/bootstrap"
	"github.com/nomand-zc/lumin-proxy/config"

	// 通过 init() 注册协议适配器
	_ "github.com/nomand-zc/lumin-proxy/protocol/openai"

	// 通过 init() 注册内置插件
	_ "github.com/nomand-zc/lumin-proxy/plugin/builtin/auth"
)

var (
	configPath string
)

func init() {
	flag.StringVar(&configPath, "config", "lumin_proxy.yaml", "配置文件路径")
}

func main() {
	flag.Parse()

	// 初始化日志
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("lumin-proxy 启动中...", "config", configPath)

	// ① 加载配置
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("加载配置失败", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// ② Bootstrap: 初始化所有依赖
	app, err := bootstrap.Init(ctx, cfg)
	if err != nil {
		slog.Error("初始化服务失败", "error", err)
		os.Exit(1)
	}

	// ③ 构建 Kratos App
	kratosApp := kratos.New(
		kratos.Name("lumin-proxy"),
		kratos.Server(app.HTTPServer),
		kratos.AfterStop(func(ctx context.Context) error {
			slog.Info("正在关闭插件...")
			return app.Shutdown(ctx)
		}),
	)

	// ④ 启动
	slog.Info("lumin-proxy 已启动", "address", cfg.Server.Address)
	if err := kratosApp.Run(); err != nil {
		slog.Error("服务运行错误", "error", err)
		os.Exit(1)
	}
}
