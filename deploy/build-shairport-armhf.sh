#!/bin/sh
# 在 WSL/Ubuntu 上用 QEMU 为骁龙410 等 armhf 老设备交叉编译 shairport-sync 5.2.1
# 前提：已执行 debootstrap --arch=armhf bullseye /opt/bullseye-armhf http://deb.debian.org/debian
# 用法：sudo sh deploy/build-shairport-armhf.sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ROOTFS=/opt/bullseye-armhf
VERSION=5.2.1
NQPTP_VERSION=1.2.8
# AIRPLAY2=0 可编译经典 AirPlay（无 ffmpeg 依赖）；默认带 AirPlay 2
WITH_AIRPLAY2="${AIRPLAY2:-1}"

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

echo "==> chroot 内安装依赖并编译（AirPlay 2: $WITH_AIRPLAY2）"
cat > "$ROOTFS/build/build.sh" <<'EOF'
#!/bin/sh
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y build-essential autoconf automake libtool pkg-config \
    libasound2-dev libavahi-client-dev libssl-dev libpopt-dev libconfig-dev \
    libsoxr-dev libglib2.0-dev vim-common

if [ "$WITH_AIRPLAY2" = "1" ]; then
    apt-get install -y libavcodec-dev libavformat-dev libavutil-dev libswresample-dev \
        libplist-dev libplist-utils libsodium-dev libgcrypt20-dev
    CONFIG_EXTRA="--with-airplay-2"
fi

cd /build
autoreconf -fi
./configure --with-ssl=openssl --with-dbus-interface --with-mpris-interface \
    --with-metadata --with-metadata-pipe --with-soxr --with-avahi \
    --with-alsa --with-pipe --with-stdout --sysconfdir=/etc $CONFIG_EXTRA
make -j4
make install DESTDIR=/pkg

# AirPlay 2 必需：编译 nqptp（PTP 时钟守护进程）
if [ "$WITH_AIRPLAY2" = "1" ]; then
    echo "==> 编译 nqptp"
    cd /build
    curl -sL -o nqptp.tar.gz "https://github.com/mikebrady/nqptp/archive/refs/tags/$NQPTP_VERSION.tar.gz"
    tar xzf nqptp.tar.gz
    cd "nqptp-$NQPTP_VERSION"
    autoreconf -fi
    ./configure --with-systemd-startup
    make -j4
    make install DESTDIR=/pkg
fi

# 收集运行时依赖库（供目标设备安装清单）
echo "==> 依赖库清单（写入 /pkg/deps.txt）"
ldd /pkg/usr/local/bin/shairport-sync | awk '{print $1}' | grep -v "^$" > /pkg/deps.txt
if [ "$WITH_AIRPLAY2" = "1" ]; then
    ldd /pkg/usr/local/bin/nqptp | awk '{print $1}' | grep -v "^$" > /pkg/deps-nqptp.txt
fi
EOF
chmod +x "$ROOTFS/build/build.sh"
export WITH_AIRPLAY2 NQPTP_VERSION
chmod +x "$ROOTFS/build/build.sh"

# 挂载虚拟文件系统后 chroot（qemu-user 经 binfmt 自动模拟 ARM）
mkdir -p "$ROOTFS/proc" "$ROOTFS/dev" "$ROOTFS/sys"
mount -t proc proc "$ROOTFS/proc" 2>/dev/null || true
mount --bind /dev "$ROOTFS/dev" 2>/dev/null || true
mount --bind /sys "$ROOTFS/sys" 2>/dev/null || true

echo "==> 进入 chroot 编译（QEMU 模拟 ARM，较慢，请耐心等待）"
chroot "$ROOTFS" /usr/bin/env WITH_AIRPLAY2="$WITH_AIRPLAY2" NQPTP_VERSION="$NQPTP_VERSION" /build/build.sh

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
编译特性：$( [ "$WITH_AIRPLAY2" = "1" ] && echo "AirPlay 2 + 经典 AirPlay" || echo "经典 AirPlay" )

目标设备安装步骤：
1. 拷贝整个 shairport-sync-armhf 目录到设备（如 /tmp/）
2. 卸载旧版（保留配置）：sudo apt remove shairport-sync
3. 安装运行时依赖（目标设备上执行，均为 bullseye 标准包）：
   apt install libasound2 libavahi-client3 libavahi-common3 libssl1.1 \\
     libpopt0 libconfig9 libsoxr0 libglib2.0-0
$( [ "$WITH_AIRPLAY2" = "1" ] && echo "   AirPlay 2 额外依赖：" && echo "   apt install libavcodec58 libavformat58 libavutil56 libswresample3 \\\\" && echo "     libplist3 libsodium23 libgcrypt20" )
4. 安装文件：
   cd /tmp/shairport-sync-armhf
   sudo cp usr/local/bin/shairport-sync /usr/local/bin/
$( [ "$WITH_AIRPLAY2" = "1" ] && echo "   sudo cp usr/local/bin/nqptp /usr/local/bin/" )
   sudo cp etc/dbus-1/system.d/*.conf /etc/dbus-1/system.d/
5. 重建 systemd unit（见下方模板）并启动：
   sudo systemctl daemon-reload
$( [ "$WITH_AIRPLAY2" = "1" ] && echo "   sudo systemctl enable --now nqptp   # AirPlay 2 必需！" )
   sudo systemctl enable --now shairport-sync
6. 验证：
   /usr/local/bin/shairport-sync -V   # 应显示 $VERSION 且含 AirPlay2
$( [ "$WITH_AIRPLAY2" = "1" ] && echo "   systemctl is-active nqptp          # 应显示 active" )
EOF
cat >> dist-armhf/shairport-sync-armhf/README.txt <<'EOF'

---- shairport-sync systemd unit ----
[Unit]
Description=Shairport Sync - AirPlay Audio Receiver
Requires=avahi-daemon.service
After=avahi-daemon.service
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=/usr/local/bin/shairport-sync $DAEMON_ARGS
User=shairport-sync
Group=shairport-sync
EnvironmentFile=-/etc/default/shairport-sync
Restart=on-failure
RestartSec=1s

[Install]
WantedBy=multi-user.target

---- nqptp systemd unit（AirPlay 2 必需）----
[Unit]
Description=Timing services for IEEE 802.1AS
After=network-online.target

[Service]
ExecStart=/usr/local/bin/nqptp
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
echo "完成: dist-armhf/shairport-sync-armhf/（含二进制、配置样例、unit、dbus policy）"
