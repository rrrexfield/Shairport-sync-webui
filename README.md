# shairport-webui

Shairport Sync（AirPlay 音频接收器）的轻量 Web 管理界面。Apple 风格中文界面，手机/桌面自适应。

专为资源受限的嵌入式设备设计：**单二进制 + 内嵌前端**，常驻内存 <10MB（`GOMEMLIMIT=20MiB`），可在 **骁龙410 + 300MB RAM** 的 Debian（armhf/arm64）上运行，亦支持 Ubuntu 与 amd64。

## 功能

- **服务控制**：查看运行状态（systemd/sysvinit 自适应）、启动/停止/重启 shairport-sync
- **播放状态**：当前播放歌曲（标题/艺人/专辑）、客户端、进度、协议（AirPlay / AirPlay 2）、**音频音质（采样率/位深/格式）**
- **配置管理**：可视化修改 `/etc/shairport-sync.conf`（设备名称、输出设备、混音器、音量曲线、采样率/格式等 20 项）+ 带行号的原始文本编辑，保存自动备份（保留最近 10 份）、一键恢复默认（弹窗确认）
- **系统信息**：内存占用、负载百分比、Wi-Fi SSID、元数据管道状态、shairport-sync 版本

> 音量调节不在 WebUI 内：AirPlay 音量由手机端滑块控制，shairport-sync 内部处理（默认软件音量，或 conf 中配置硬件 mixer）。

## 目录

- [部署教程](#部署教程)
- [使用说明](#使用说明)
- [配置](#配置)
- [音质信息与版本兼容](#音质信息与版本兼容)
- [安全说明](#安全说明)
- [开发与构建](#开发与构建)
- [常见问题](#常见问题)

## 部署教程

### 准备：确定 shairport-sync 版本

| 方案 | 适用 | shairport-sync 来源 |
|---|---|---|
| **A. 快速部署** | 系统里已有 shairport-sync（apt 安装即可） | 发行版源（如 `apt install shairport-sync`，Ubuntu 24.04 为 3.3.8） |
| **B. 完整部署**（推荐） | 需要 AirPlay 2 或最新特性 | 源码编译 5.x |

WebUI 对两种方案均完全支持，音质信息来源随版本自动切换（见[版本兼容](#音质信息与版本兼容)）。

### 第 1 步：安装 shairport-sync

**方案 A（已有或 apt 安装）**

```sh
sudo apt install shairport-sync avahi-daemon alsa-utils
sudo systemctl enable --now shairport-sync
```

**方案 B（编译 5.x + AirPlay 2，以 5.2.1 为例）**

```sh
# 编译依赖（AirPlay 2 需要 ffmpeg 库，体积较大）
sudo apt install build-essential autoconf automake libtool pkg-config \
  libasound2-dev libavahi-client-dev libssl-dev libpopt-dev libconfig-dev \
  libsoxr-dev libplist-dev libplist-utils libsodium-dev libgcrypt20-dev \
  libglib2.0-dev libavcodec-dev libavformat-dev libavutil-dev libswresample-dev

# AirPlay 2 必需的 PTP 时钟守护进程 nqptp
git clone https://github.com/mikebrady/nqptp && cd nqptp
autoreconf -fi && ./configure --with-systemd-startup && make && sudo make install
sudo systemctl enable --now nqptp

# shairport-sync
cd .. && curl -L -o s.tar.gz https://github.com/mikebrady/shairport-sync/archive/refs/tags/5.2.1.tar.gz
tar xzf s.tar.gz && cd shairport-sync-5.2.1
autoreconf -fi
./configure --with-airplay-2 --with-ssl=openssl \
  --with-dbus-interface --with-mpris-interface --with-metadata \
  --with-metadata-pipe --with-soxr --with-avahi --with-alsa \
  --with-pipe --with-stdout --sysconfdir=/etc
make && sudo make install
```

> 若此前装过 apt 版：`sudo apt remove shairport-sync` 后编译安装，并确认 systemd unit 的 `ExecStart` 指向 `/usr/local/bin/shairport-sync`。
> **300MB RAM 设备**：编译用 `make -j1` 防 OOM；或参考[交叉编译 WebUI](#第-2-步获取-webui-二进制)的方式在其他机器上编译。

验证：

```sh
shairport-sync -V     # 应输出含 AirPlay2 的版本串
sudo systemctl restart shairport-sync && systemctl is-active shairport-sync nqptp
```

### 第 2 步：获取 WebUI 二进制

**方式一：交叉编译（推荐，在任意 Linux/WSL 上；目标设备无需 Go）**

```sh
# 获取源码：git clone https://github.com/rrrexfield/Shairport-sync-webui 或直接拷贝目录
cd shairport-webui
./deploy/build.sh            # 全部架构：armhf(GOARM=7) + arm64 + amd64
./deploy/build.sh armhf      # 仅 ARMv7 32 位（骁龙410 老 Debian 常见形态）
VERSION=1.1.0 ./deploy/build.sh   # 自定义版本号（默认 1.0.0，显示在页脚）
UPX=1 ./deploy/build.sh      # 可选 upx 压缩（约 6MB → 1.5MB）
```

产物在 `dist/`：`shairport-webui-{armhf,arm64,amd64}`。二进制为 `CGO_ENABLED=0` 静态链接，**目标设备无需安装 Go、无需匹配 glibc 版本**（老 Debian 8/9 可直接运行），只需拷贝对应架构的单个文件。

**方式二（可选）：在目标设备上直接编译**——仅当设备兼作构建机时使用，需自行安装 Go 1.22+

```sh
cd shairport-webui   # 拷贝源码目录到设备
go mod vendor && ./deploy/build.sh "$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/;s/armv6l\|armv7l/armhf/')"
```

### 第 3 步：安装 WebUI

将 `dist/` 与 `deploy/` 目录一同拷贝到目标机（如 `scp -r dist deploy user@设备:/tmp/webui/`），然后：

```sh
cd /tmp/webui
sudo sh deploy/install.sh
```

install.sh **幂等可重跑**，自动完成：

1. 按 `uname -m` 选择二进制安装到 `/usr/local/bin/shairport-webui`
2. 探测 init 系统：systemctl（路径自适应 `/usr/bin` 与 `/bin`）或 sysvinit（自动装 `/etc/init.d` 脚本）
3. 创建专用用户 `shairport-webui`（nologin）并加入 `audio` 组
4. 安装 sudoers 白名单（仅允许控制 shairport-sync 服务 + 执行固定路径的写配置脚本，`!requiretty`）
5. 安装 `write-config.sh`（零参数 stdin 传内容、校验后原子替换、自动备份）
6. 安装并启动 systemd 服务或 sysvinit 脚本（`GOMEMLIMIT=20MiB`）
7. 生成 `/etc/shairport-webui.conf`

### 第 4 步：访问

浏览器打开 `http://<设备IP>:8080`。页面底部显示 WebUI 版本号即部署成功。

### 第 5 步（推荐）：启用歌曲/音质信息

- **shairport-sync 5.x**：开箱即用（信息走 D-Bus，播放时自动显示）
- **3.3.x/4.x**：页面「配置 → 常用设置 → 启用元数据管道」设为"是"→ 保存 → 「服务 → 重启」

## 使用说明

| 卡片 | 说明 |
|---|---|
| 正在播放 | 状态标签、音质（如 `44100 Hz / 16 bit` 或 `44100/16`）、曲目/艺人、协议、客户端、进度 |
| 服务 | 运行状态、启动时间、init 系统；启动/停止/重启按钮 |
| 系统 | 主机名、Wi-Fi SSID、内存、负载百分比、元数据管道状态、shairport-sync 版本 |
| 配置 | 折叠卡：常用设置/高级设置（iOS 开关=使用自定义值，下拉为自定义组件）/原始编辑（带行号）；保存按钮仅在**有未保存修改时点亮**；危险操作区「恢复默认配置」弹窗确认 |

## 配置

WebUI 自身配置 `/etc/shairport-webui.conf`（JSON，参考 `webui.conf.example`）：

| 字段 | 默认 | 说明 |
|---|---|---|
| `listen` | `:8080` | 监听地址。仅内网使用；需限制时改 `127.0.0.1:8080` |
| `shairport_service` | `shairport-sync` | 受控服务名 |
| `shairport_conf` | `/etc/shairport-sync.conf` | shairport 配置文件路径 |
| `metadata_pipe` | 自动 | 元数据管道路径（留空从 conf 解析） |
| `mixer_control` | 自动 | 混音器控制名（配置页 mixer 字段的下拉参考） |
| `systemctl_path` | 自动 | 老系统 usrmerge 前为 `/bin/systemctl`（install.sh 自动写入） |

修改后 `sudo systemctl restart shairport-webui` 生效。

## 音质信息与版本兼容

| shairport-sync 版本 | 音质来源 | 歌曲信息 | 协议显示 |
|---|---|---|---|
| **5.x**（推荐） | D-Bus `SourceFormat` 属性（直读） | D-Bus/MPRIS | ✓ AirPlay/AirPlay 2 |
| 4.x | D-Bus `GetInfo` active_session（自动探测） | MPRIS | ✓ |
| 3.3.x | metadata pipe `asfm` 码（需启用管道） | pipe | ✗ |
| 2.x | 不可用（自动降级隐藏） | ✗ | ✗ |

所有版本降级自动，服务控制与配置管理不受版本影响。5.x 的 metadata pipe 为多行 XML 输出（WebUI 已适配）。

## 安全说明

- WebUI **无登录认证**，请仅在可信局域网内使用，勿暴露公网
- 运行身份为专用用户 `shairport-webui`，权限经 sudoers 白名单最小化：
  - 服务控制仅限 `systemctl start/stop/restart shairport-sync`
  - 写配置仅能执行固定路径的 `write-config.sh`（目标路径硬编码、零参数、内容校验）
- 配置保存/恢复默认始终自动备份到 `shairport-sync.conf.bak-<时间戳>`（保留最近 10 份）

## 开发与构建

```sh
go build -buildvcs=false ./... && go test ./...   # 构建与测试
go run . -conf /tmp/webui-dev.conf                # 本地运行（可配 listen 与 shairport_conf 副本）
```

单测覆盖：libconfig 解析/编辑（注释保真、插入、幂等、字面量合法性）、pipe XML 帧解析（3.3.8 单行 + 5.x 多行混流）与 FIFO 重连状态机、systemctl/sysvinit 降级链、D-Bus 会话解析、amixer 输出解析。

## 常见问题

| 现象 | 处理 |
|---|---|
| 顶栏显示"失败" | shairport-sync 服务启动失败：`systemctl status shairport-sync` 查看原因（常见：配置文件语法错误、端口被占）。可用「恢复默认配置」+ 重启 |
| 音质/歌曲不显示（5.x） | 播放时才有数据；确认 shairport-sync 为 5.x（`shairport-sync -V`） |
| 音质/歌曲不显示（3.3.x/4.x） | 启用元数据管道并重启服务；系统卡「元数据管道」应为"已连接" |
| AirPlay 2 无法连接 | 确认 nqptp 服务运行中（`systemctl is-active nqptp`）；AirPlay 2 不支持 Windows iTunes |
| 端口 8080 无法访问 | 检查防火墙；确认 `systemctl is-active shairport-webui` |
| 配置保存报错 | 非 root 运行时依赖 sudoers 白名单，重跑 install.sh 修复 |
| 页面显示"连接中断" | WebUI 服务被停止或网络异常，页面自动重试 |

## 目标设备注意事项（骁龙410 / 300MB RAM）

- 架构选择：老 Debian 镜像（armhf 32 位）用 `dist/shairport-webui-armhf`；较新镜像用 arm64
- 常驻 RSS 预期 5–8MB，2s 轮询的 CPU 占用可忽略
- 编译 shairport-sync 5.x 时用 `make -j1`；磁盘紧张的设备可省略 AirPlay 2（不传 `--with-airplay-2`，省去 ffmpeg 依赖）
- 无 systemd 的老系统自动走 sysvinit 脚本，功能不受影响
