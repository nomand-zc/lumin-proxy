package main

import (
	"context"
	"os"

	flag "github.com/spf13/pflag"

	"github.com/nomand-zc/lumin-client/log"
	"github.com/nomand-zc/lumin-proxy/app"
	"github.com/nomand-zc/lumin-proxy/config"

	// 通过 init() 注册协议适配器
	_ "github.com/nomand-zc/lumin-proxy/protocol/openai"

	// 通过 init() 注册内置插件
	_ "github.com/nomand-zc/lumin-proxy/plugin/builtin/auth"
)

var (
	configPath string
	signalCmd  string
)

func init() {
	flag.StringVarP(&configPath, "config", "c", "lumin_proxy.yaml", "配置文件路径")
	flag.StringVarP(&signalCmd, "signal", "s", "", "信号命令: restart(优雅重启) / stop(优雅停止)")
	flag.Parse()
}

func main() {
	log.Infof("lumin-proxy 启动中..., config=%s, pid=%d", configPath, os.Getpid())

	// ① 加载配置
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Errorf("加载配置失败: %v", err)
		os.Exit(1)
	}
	log.SetLevel(cfg.Log.Level)

	// ② 创建应用
	ctx := context.Background()
	var opts []app.Option
	if signalCmd != "" {
		opts = append(opts, app.WithSignal(signalCmd))
	}

	application, err := app.New(ctx, cfg, opts...)
	if err != nil {
		log.Errorf("初始化服务失败: %v", err)
		os.Exit(1)
	}

	// ③ 启动（内部完成 tableflip、信号监听、优雅重启等全部流程）
	if err := application.Run(); err != nil {
		log.Errorf("服务运行失败: %v", err)
		os.Exit(1)
	}
}
