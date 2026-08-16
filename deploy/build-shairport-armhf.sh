#!/bin/sh
# 在 WSL/Ubuntu 上用 QEMU 为骁龙410 等 armhf 老设备交叉编译 shairport-sync 5.2.1
# 前提：已执行 debootstrap --arch=armhf bullseye /opt/bullseye-armhf http://deb.debian.org/debian
# 用法：sudo sh deploy/build-shairport-armhf.sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ROOTFS=/opt/bullseye-armhf
VERSION=5.2.1

echo "==> 准备 armhf rootfs"
if [ ! -d "$ROOTFS/debootstrap" ] && [ ! -d "$ROOTFS/usr/bin" ]; then
    echo "错误: $ROOTFS 不存在，先执行: debootstrap --arch=armhf bullseye $ROOTFS http://deb.debian.org/debian"
    exit 1
fi

echo "==> 拷贝 shairport-sync $VERSION 源码"
rm -rf "$ROOTFS/build"
mkdir -p "$ROOTFS/build"
cd /tmp
[ -f "shairport-sync-$VERSION.tar.gz" ] || curl -sL -o "shairport-sync-$VERSION.tar.gz" \
    "https://github.com/mikebrady/shairport-sync/archive/refs/tags/$VERSION.tar.gz"
tar xzf "shairport-sync-$VERSION.tar.gz" -C "$ROOTFS/build" --strip-components=1

echo "==> chroot 内安装依赖并编译"
cat > "$ROOTFS/build/build.sh" <<'EOF'
#!/bin/sh
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y build-essential autoconf automake libtool pkg-config \
    libasound2-dev libavahi-client-dev libssl-dev libpopt-dev libconfig-dev \
    libsoxr-dev libglib2.0-dev

cd /build
autoreconf -fi
./configure --with-ssl=openssl --with-dbus-interface --with-mpris-interface \
    --with-metadata --with-metadata-pipe --with-soxr --with-avahi \
    --with-alsa --with-pipe --with-stdout --sysconfdir=/etc
make -j4
make install DESTDIR=/pkg

# 收集运行时依赖库（供目标设备安装清单）
echo "==> 依赖库清单（写入 /pkg/deps.txt）"
ldd /pkg/usr/local/bin/shairport-sync | awk '{print $1}' | grep -v "^$" > /pkg/deps.txt
EOF
chmod +x "$ROOTFS/build/build.sh"

# 挂载虚拟文件系统后 chroot（qemu-user 经 binfmt 自动模拟 ARM）
mkdir -p "$ROOTFS/proc" "$ROOTFS/dev" "$ROOTFS/sys"
mount -t proc proc "$ROOTFS/proc" 2>/dev/null || true
mount --bind /dev "$ROOTFS/dev" 2>/dev/null || true
mount --bind /sys "$ROOTFS/sys" 2>/dev/null || true

echo "==> 进入 chroot 编译（QEMU 模拟 ARM，较慢，请耐心等待）"
chroot "$ROOTFS" /build/build.sh

umount "$ROOTFS/proc" 2>/dev/null || true
umount "$ROOTFS/dev" 2>/dev/null || true
umount "$ROOTFS/sys" 2>/dev/null || true

echo "==> 打包交付物"
cd "$SCRIPT_DIR"
mkdir -p dist-armhf
rm -rf dist-armhf/shairport-sync-armhf
mkdir -p dist-armhf/shairport-sync-armhf
cp -a "$ROOTFS/pkg/." dist-armhf/shairport-sync-armhf/
cat > dist-armhf/shairport-sync-armhf/README.txt <<EOF
shairport-sync $VERSION (armhf / Debian 11 bullseye) 预编译包

目标设备安装步骤：
1. 拷贝整个 shairport-sync-armhf 目录到设备（如 /tmp/）
2. 安装运行时依赖（目标设备上执行）：
   apt install libasound2 libavahi-client3 libssl1.1 libpopt0 libconfig9 libsoxr0 libglib2.0-0
   （如个别包名在设备源中不同，按提示调整）
3. 安装：
   cd /tmp/shairport-sync-armhf
   sudo cp -a usr/local/bin/shairport-sync /usr/local/bin/
   sudo cp -a etc/ /etc/   （注意：不会覆盖已存在的 /etc/shairport-sync.conf）
   sudo cp -a usr/local/share/ /usr/local/
4. 启动（systemd 或手动）：
   sudo systemctl daemon-reload
   sudo systemctl restart shairport-sync
   或手动: /usr/local/bin/shairport-sync -d
5. 验证: /usr/local/bin/shairport-sync -V（应显示 $VERSION）
EOF
echo "完成: dist-armhf/shairport-sync-armhf/（含二进制、配置样例、unit、dbus policy）"
