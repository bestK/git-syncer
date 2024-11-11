#!/bin/bash


# 添加 URL 编码函数
url_encode() {
    echo -n "$1" | curl -Gso /dev/null -w %{url_effective} --data-urlencode @- "" | cut -c 3-
}

# 检测系统架构
detect_arch() {
    local arch=$(uname -m)
    case $arch in
        x86_64)
            GOARCH="amd64"
            ;;
        aarch64)
            GOARCH="arm64"
            ;;
        *)
            echo "不支持的架构: $arch"
            exit 1
            ;;
    esac
}

# 检测操作系统
detect_os() {
    local os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case $os in
        linux)
            GOOS="linux"
            ;;
        darwin)
            GOOS="darwin"
            ;;
        *)
            echo "不支持的操作系统: $os"
            exit 1
            ;;
    esac
}

# 设置系统信息
detect_os
detect_arch

# 检查并安装必要的命令
install_jq() {
    echo "正在安装 jq..."
    if command -v apt-get &> /dev/null; then
        sudo apt-get update && sudo apt-get install -y jq
    elif command -v yum &> /dev/null; then
        sudo yum install -y jq
    elif command -v brew &> /dev/null; then
        brew install jq
    else
        echo "错误: 无法自动安装 jq，请手动安装"
        exit 1
    fi
}

# 检查 jq 是否已安装，如果没有则安装
if ! command -v jq &> /dev/null; then
    install_jq
fi

# 检查必要的命令是否存在
check_command() {
    if ! command -v "$1" &> /dev/null; then
        echo "错误: 未找到命令 '$1'"
        echo "请先安装 $1"
        exit 1
    fi
}

# 检查必要的命令
check_command curl

# 检查是否有 -p 参数
USE_PROXY=false
while getopts "p" opt; do
  case ${opt} in
    p ) USE_PROXY=true ;;
    \? ) echo "用法: $0 [-p]"; exit 1 ;;
  esac
done

# 判断是否使用代理
if [ "$USE_PROXY" = true ]; then
    GITHUB_API_URL="https://proxy.linkof.link/$(url_encode "https://api.github.com/repos/bestk/git-syncer/releases/latest")"
else
    GITHUB_API_URL="https://api.github.com/repos/bestk/git-syncer/releases/latest"
fi

# 获取版本信息并添加调试输出
echo "正在获取最新版本信息... $GITHUB_API_URL"
RESPONSE=$(curl -s "$GITHUB_API_URL")
echo "API 响应: $RESPONSE"

VERSION=$(echo "$RESPONSE" | jq -r '.tag_name')
if [ "$VERSION" = "null" ] || [ -z "$VERSION" ]; then
    echo "错误: 无法获取有效的版本信息"
    echo "API 返回: $RESPONSE"
    exit 1
fi

# 构建下载 URL
if [ "$USE_PROXY" = true ]; then
    DOWNLOAD_URL="https://proxy.linkof.link/$(url_encode "https://github.com/bestk/git-syncer/releases/download/$VERSION/git-syncer-$GOOS-$GOARCH")"
else
    DOWNLOAD_URL="https://github.com/bestk/git-syncer/releases/download/$VERSION/git-syncer-$GOOS-$GOARCH"
fi

echo "版本: $VERSION"
echo "下载地址: $DOWNLOAD_URL"

# 下载最新版本
if ! curl -L "$DOWNLOAD_URL" -o git-syncer; then
  echo "下载失败"
  exit 1
fi

# 检查文件是否存在
if [ ! -f git-syncer ]; then
  echo "下载文件不存在"
  exit 1
fi

# 移动到 /usr/local/bin
if ! sudo mv git-syncer /usr/local/bin; then
  echo "移动文件失败"
  exit 1
fi

# 设置权限
if ! sudo chmod +x /usr/local/bin/git-syncer; then
  echo "设置权限失败"
  exit 1
fi

echo "git-syncer 安装成功" 