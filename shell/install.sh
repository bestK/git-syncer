#!/bin/bash

# 检查是否有 -p 参数
USE_PROXY=false
while getopts "p" opt; do
  case ${opt} in
    p ) USE_PROXY=true ;;
    \? ) echo "用法: $0 [-p]"; exit 1 ;;
  esac
done

function url_encode() {
  echo "$1" | jq -s -R -f urlencode.jq
}   


# 判断是否使用代理
if [ "$USE_PROXY" = true ]; then
  # 获取最新版本
  VERSION=$(curl -s "https://proxy.linkof.link/$(url_encode https://api.github.com/repos/bestk/git-syncer/releases/latest)" | jq -r '.tag_name')
  # 使用 gh-proxy.com 代理下载
  DOWNLOAD_URL="https://proxy.linkof.link/$(url_encode https://github.com/bestk/git-syncer/releases/download/$VERSION/git-syncer-$GOOS-$GOARCH)"
else
  VERSION=$(curl -s "https://api.github.com/repos/bestk/git-syncer/releases/latest" | jq -r '.tag_name')
  # 不使用代理
  DOWNLOAD_URL="https://github.com/bestk/git-syncer/releases/download/$VERSION/git-syncer-$GOOS-$GOARCH"
fi

# 下载最新版本
curl -L $DOWNLOAD_URL -o git-syncer

# 移动到 /usr/local/bin
sudo mv git-syncer /usr/local/bin

# 设置权限
sudo chmod +x /usr/local/bin/git-syncer

echo "git-syncer 安装成功"
