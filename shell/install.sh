#!/bin/bash

# 获取最新版本
VERSION=$(curl -s https://api.github.com/repos/bestk/git-syncer/releases/latest | jq -r '.tag_name')

# 下载最新版本
curl -L https://github.com/gitee/git-syncer/releases/download/$VERSION/git-syncer-$GOOS-$GOARCH -o git-syncer

# 移动到 /usr/local/bin
sudo mv git-syncer /usr/local/bin

# 设置权限
sudo chmod +x /usr/local/bin/git-syncer

echo "git-syncer 安装成功"
