#!/bin/sh
# shairport-webui 的 sudo 白名单写配置脚本。
# 由 WebUI 进程经 sudo 调用：stdin 传入新配置全文，零命令行参数（攻击面最小）。
# 目标路径硬编码，与 sudoers 白名单配合使用。
# 输出：备份文件名（成功）或错误信息（stderr，非零退出码）。

set -e
TARGET="/etc/shairport-sync.conf"
TMP="$(mktemp /tmp/webui-conf.XXXXXX)"
trap 'rm -f "$TMP"' EXIT

cat > "$TMP"

# ---- 基本校验：非空、大小、关键段、括号与引号平衡 ----
[ -s "$TMP" ] || { echo "错误: 内容为空" >&2; exit 1; }
[ "$(wc -c < "$TMP")" -lt 65536 ] || { echo "错误: 内容超过 64KB" >&2; exit 2; }
grep -q 'general' "$TMP" || { echo "错误: 找不到 general 段" >&2; exit 3; }

O=$(sed 's,//.*,,' "$TMP" | tr -cd '{' | wc -c)
C=$(sed 's,//.*,,' "$TMP" | tr -cd '}' | wc -c)
[ "$O" = "$C" ] || { echo "错误: 括号不匹配 ($O vs $C)" >&2; exit 4; }

Q=$(sed 's,//.*,,' "$TMP" | tr -cd '"' | wc -c)
[ $((Q % 2)) -eq 0 ] || { echo "错误: 引号不配对" >&2; exit 5; }

# ---- 权限与原子替换 ----
chmod 644 "$TMP"
chown root:root "$TMP" 2>/dev/null || true

TS=$(date +%Y%m%d%H%M%S)
BACKUP="$TARGET.bak-$TS"
if [ -f "$BACKUP" ]; then
    # 同一秒内再次保存：追加序号，不覆盖上一份
    N=1
    while [ -f "$BACKUP.$N" ]; do N=$((N+1)); done
    BACKUP="$BACKUP.$N"
fi
cp -a "$TARGET" "$BACKUP" 2>/dev/null || true
mv "$TMP" "$TARGET"

# 只保留最近 10 份备份
ls -1t "$TARGET".bak-* 2>/dev/null | tail -n +11 | xargs -r rm -f

echo "$BACKUP"
