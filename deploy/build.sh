#!/bin/sh
# 交叉编译脚本。用法:
#   ./build.sh            # 全部架构
#   ./build.sh armhf      # 仅 ARMv7 32 位（骁龙410 老 Debian 常见形态）
#   ./build.sh arm64      # 仅 ARMv8 64 位
#   ./build.sh amd64      # 本机测试
#   UPX=1 ./build.sh      # 启用 upx 压缩（检测不到则自动跳过）
set -e

cd "$(dirname "$0")/.."
mkdir -p dist

export CGO_ENABLED=0
export GOOS=linux
# 版本号：VERSION=1.2.3 ./build.sh 覆盖，默认 1.0.0
VERSION="${VERSION:-1.0.0}"
LDFLAGS="-s -w -X main.version=$VERSION"
echo ">>> 版本: $VERSION"

build_one() {
    arch="$1"
    goarch="$2"
    goarm="$3"
    echo ">>> 构建 linux/$arch ..."
    if [ -n "$goarm" ]; then
        GOARCH="$goarch" GOARM="$goarm" go build -buildvcs=false -trimpath -ldflags "$LDFLAGS" -o "dist/shairport-webui-$arch" .
    else
        GOARCH="$goarch" go build -buildvcs=false -trimpath -ldflags "$LDFLAGS" -o "dist/shairport-webui-$arch" .
    fi
    size=$(wc -c < "dist/shairport-webui-$arch")
    echo "    产物: dist/shairport-webui-$arch ($size bytes)"
}

targets="${1:-all}"
case "$targets" in
    armhf|arm64|amd64|all) ;;
    *) echo "未知目标: $1（可选 armhf/arm64/amd64/all）"; exit 1 ;;
esac
if [ "$targets" = "all" ] || [ "$targets" = "armhf" ]; then
    build_one armhf arm 7
fi
if [ "$targets" = "all" ] || [ "$targets" = "arm64" ]; then
    build_one arm64 arm64 ""
fi
if [ "$targets" = "all" ] || [ "$targets" = "amd64" ]; then
    build_one amd64 amd64 ""
fi

# upx 压缩（可选）：检测不到命令则跳过
if [ "${UPX:-0}" = "1" ] && command -v upx >/dev/null 2>&1; then
    echo ">>> upx 压缩 ..."
    upx --best dist/shairport-webui-* 2>/dev/null || true
    ls -la dist/
fi

echo "完成。部署：在目标机上以 root 运行 deploy/install.sh（与 dist/ 一同拷贝）"
