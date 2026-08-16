#!/bin/sh
# shairport-webui 安装脚本（必须以 root 运行，幂等可重跑）。
# 用法：将 dist/ 与 deploy/ 一同拷贝到目标机，然后在 dist/ 所在目录执行:
#   sudo sh deploy/install.sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DIST_DIR="$(dirname "$SCRIPT_DIR")/dist"
SBIN="/usr/local/bin/shairport-webui"
LIBEXEC="/usr/libexec/shairport-webui"
SERVICE="${SHAIRPORT_SERVICE:-shairport-sync}"
CONF_TARGET="/etc/shairport-sync.conf"

echo "==> shairport-webui 安装"

# ---------- 1. 架构检测与二进制选择 ----------
MACHINE="$(uname -m)"
case "$MACHINE" in
    armv7l|armv6l) ARCH=armhf ;;
    aarch64)       ARCH=arm64 ;;
    x86_64)        ARCH=amd64 ;;
    *) echo "错误: 未知架构 $MACHINE"; exit 1 ;;
esac
BIN="$DIST_DIR/shairport-webui-$ARCH"
[ -f "$BIN" ] || { echo "错误: 找不到二进制 $BIN（请先运行 build.sh）"; exit 1; }

# ---------- 2. 安装二进制 ----------
install -m 0755 "$BIN" "$SBIN"
echo "    二进制 -> $SBIN ($ARCH)"

# ---------- 3. init 系统探测 ----------
SYSTEMCTL=""
for p in /usr/bin/systemctl /bin/systemctl; do
    [ -x "$p" ] && SYSTEMCTL="$p" && break
done
[ -z "$SYSTEMCTL" ] && SYSTEMCTL="$(command -v systemctl 2>/dev/null || true)"
INIT=systemd
if [ -z "$SYSTEMCTL" ]; then
    INIT=sysvinit
    SERVICE_CMD="$(command -v service || true)"
    [ -z "$SERVICE_CMD" ] && { echo "错误: 未找到 systemctl 或 service 命令"; exit 1; }
fi
echo "    init 系统: $INIT"

# ---------- 4. 专用用户与 audio 组 ----------
if ! id shairport-webui >/dev/null 2>&1; then
    useradd -r -s /usr/sbin/nologin -d / shairport-webui
    echo "    创建用户 shairport-webui"
fi
usermod -aG audio shairport-webui 2>/dev/null || true

# ---------- 5. 确保配置文件可读 ----------
if [ -f "$CONF_TARGET" ]; then
    chmod 644 "$CONF_TARGET"
else
    echo "警告: $CONF_TARGET 不存在，请先安装 shairport-sync"
fi

# ---------- 6. sudoers 白名单 ----------
if ! command -v sudo >/dev/null 2>&1; then
    echo "错误: 未安装 sudo（apt install sudo）"
    exit 1
fi
mkdir -p "$LIBEXEC"
install -m 0755 "$SCRIPT_DIR/write-config.sh" "$LIBEXEC/write-config.sh"

SUDOERS_FRAG="/etc/sudoers.d/shairport-webui"
if [ "$INIT" = "systemd" ]; then
    CMD_ALIAS="$SYSTEMCTL start $SERVICE, $SYSTEMCTL stop $SERVICE, $SYSTEMCTL restart $SERVICE"
else
    CMD_ALIAS="$SERVICE_CMD $SERVICE start, $SERVICE_CMD $SERVICE stop, $SERVICE_CMD $SERVICE restart"
fi
{
    echo "Defaults:shairport-webui !requiretty"
    echo "Cmnd_Alias SH_CTL = $CMD_ALIAS"
    echo "shairport-webui ALL=(root) NOPASSWD: SH_CTL, $LIBEXEC/write-config.sh"
} > "$SUDOERS_FRAG"
chmod 440 "$SUDOERS_FRAG"
visudo -c >/dev/null || { echo "错误: sudoers 校验失败"; exit 1; }
echo "    sudoers -> $SUDOERS_FRAG"

# ---------- 7. 安装服务 ----------
if [ "$INIT" = "systemd" ]; then
    install -m 0644 "$SCRIPT_DIR/shairport-webui.service" /etc/systemd/system/shairport-webui.service
    systemctl daemon-reload
    systemctl enable shairport-webui.service >/dev/null 2>&1 || true
    systemctl restart shairport-webui.service
    echo "    systemd 服务已安装并启动"
else
    install -m 0755 "$SCRIPT_DIR/shairport-webui.init.d" /etc/init.d/shairport-webui
    if command -v update-rc.d >/dev/null 2>&1; then
        update-rc.d shairport-webui defaults
    else
        for n in 2 3 4 5; do ln -sf ../init.d/shairport-webui "/etc/rc$n.d/S95shairport-webui"; done
    fi
    /etc/init.d/shairport-webui restart || true
    echo "    sysvinit 服务已安装并启动"
fi

# ---------- 8. WebUI 配置 ----------
WEBCONF="/etc/shairport-webui.conf"
if [ ! -f "$WEBCONF" ]; then
    {
        echo '{'
        echo '  "listen": ":8080",'
        echo "  \"shairport_service\": \"$SERVICE\","
        echo "  \"shairport_conf\": \"$CONF_TARGET\","
        if [ "$INIT" = "systemd" ]; then
            echo "  \"systemctl_path\": \"$SYSTEMCTL\","
        fi
        echo '  "sudo_path": "/usr/bin/sudo",'
        echo '  "write_script": "/usr/libexec/shairport-webui/write-config.sh"'
        echo '}'
    } > "$WEBCONF"
    chmod 644 "$WEBCONF"
    echo "    生成配置 $WEBCONF（如需修改端口请编辑 listen）"
else
    echo "    $WEBCONF 已存在，保留"
fi

# ---------- 9. 自检 ----------
sleep 1
echo "==> 自检:"
if command -v curl >/dev/null 2>&1; then
    curl -s --max-time 3 http://localhost:8080/api/status | head -c 300 || true
    echo
    echo "==> 完成。浏览器访问 http://<设备IP>:8080 即可管理。"
else
    echo "完成。安装 curl 后可执行 curl http://localhost:8080/api/status 自检。"
fi
