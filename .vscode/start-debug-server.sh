#!/bin/bash

# lumin-proxy 远程调试启动脚本
# 用于启动调试服务器，支持 VS Code 远程调试

echo "启动 lumin-proxy 调试服务器..."

# 检查是否安装了 delve
dlv version >/dev/null 2>&1
if [ $? -ne 0 ]; then
    echo "错误: 未找到 delve 调试器，请先安装:"
    echo "go install github.com/go-delve/delve/cmd/dlv@latest"
    exit 1
fi

# 构建项目（可选）
echo "构建项目..."
go build -o bin/lumin-proxy-debug ./cmd/lumin-proxy

# 启动调试服务器
echo "启动调试服务器 (端口: 2345)..."
dlv debug --headless --listen=:2345 --api-version=2 --accept-multiclient ./cmd/lumin-proxy -- -c lumin_proxy.yaml

echo "调试服务器已启动，可在 VS Code 中使用远程调试配置连接"