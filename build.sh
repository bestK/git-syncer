
#!/bin/bash

# 获取版本信息
VERSION=$(git describe --tags --always --dirty)
COMMIT=$(git rev-parse --short HEAD)
BUILD_TIME=$(date -u '+%Y-%m-%d_%H:%M:%S')

# 构建标志
LDFLAGS="-X main.Version=$VERSION -X main.GitCommit=$COMMIT -X main.BuildTime=$BUILD_TIME"

# 清理旧的构建文件
rm -f git-syncer

# 执行构建
echo "Building git-syncer..."
go build -ldflags "$LDFLAGS" -o git-syncer

if [ $? -eq 0 ]; then
    echo "Build successful!"
    echo "Version: $VERSION"
    echo "Commit: $COMMIT"
    echo "Build time: $BUILD_TIME"
else
    echo "Build failed!"
    exit 1
fi
