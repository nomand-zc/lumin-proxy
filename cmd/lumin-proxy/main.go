package main

import (
	"context"
	"flag"
	"os"

	"github.com/nomand-zc/lumin-client/log"
	"github.com/nomand-zc/lumin-proxy/config"
	"github.com/nomand-zc/lumin-proxy/server"

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

	log.Infof("lumin-proxy 启动中..., config=%s", configPath)

	// ① 加载配置
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Errorf("加载配置失败: %v", err)
		os.Exit(1)
	}

	// 根据配置设置日志级别
	log.SetLevel(cfg.Log.Level)

	ctx := context.Background()

	// ② 创建 Server 实例（初始化所有依赖）
	srv, err := server.New(ctx, cfg)
	if err != nil {
		log.Errorf("初始化服务失败: %v", err)
		os.Exit(1)
	}

	// ③ 启动服务
	if err := srv.Run(); err != nil {
		log.Errorf("服务运行错误: %v", err)
		os.Exit(1)
	}
}
