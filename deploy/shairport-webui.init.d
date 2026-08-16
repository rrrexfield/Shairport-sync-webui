#!/bin/sh
# sysvinit 启动脚本（systemd 不可用时的降级方案，install.sh 自动选用）
### BEGIN INIT INFO
# Provides:          shairport-webui
# Required-Start:    $network $remote_fs
# Required-Stop:     $network
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: Shairport Sync WebUI
### END INIT INFO

DAEMON=/usr/local/bin/shairport-webui
PIDFILE=/run/shairport-webui.pid
USER=shairport-webui

. /lib/lsb/init-functions 2>/dev/null || true

case "$1" in
    start)
        log_daemon_msg "Starting shairport-webui"
        start-stop-daemon --start --quiet --background --make-pidfile \
            --pidfile "$PIDFILE" --chuid "$USER" --exec "$DAEMON"
        log_end_msg $?
        ;;
    stop)
        log_daemon_msg "Stopping shairport-webui"
        start-stop-daemon --stop --quiet --pidfile "$PIDFILE" --retry 5
        log_end_msg $?
        rm -f "$PIDFILE"
        ;;
    restart|force-reload)
        $0 stop
        sleep 1
        $0 start
        ;;
    status)
        status_of_proc -p "$PIDFILE" "$DAEMON" shairport-webui
        ;;
    *)
        echo "用法: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac
exit 0
