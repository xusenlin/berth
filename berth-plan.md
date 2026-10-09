# Berth 开发计划

本地桌面软件，通过 SSH 把二进制程序部署到多台 Linux 服务器并管理其运行。服务器上不安装任何常驻程序。

## 1. 目标与边界

**要做的**

- 管理多台服务器，支持密码、私钥文件、ssh-agent 三种登录方式
- 服务器概况：系统、CPU、内存、磁盘、负载
- 部署任意 Linux 二进制（Go、Rust 等），可附带配置文件
- 启动、停止、重启、开机自启、崩溃自动拉起
- 实时与历史日志查看
- 版本保留与回滚

**不做的**

- 域名绑定、反向代理、HTTPS 证书
- 容器、镜像
- 云厂商安全组管理
- 放到服务器上通过浏览器远程访问

## 2. 技术栈

| 层 | 选型 | 说明 |
|---|---|---|
| 语言 | Go 1.27+（当前 1.27.2） | MyGo v0.3.x 要求 Go ≥ 1.27.1 |
| 桌面壳 | MyGo（`egoist/mygo`，v0.3.5） | 纯 Go 无 cgo，使用系统 webview；项目很新，见风险一节 |
| 前端 | React + Vite + Tailwind + shadcn/ui | 以 sidebar 区块为布局基础 |
| 构建 | `go tool mygo`（go.mod 中固定版本）+ pnpm | 用 `mygo.json` 而非 `mygo.config.ts`，不依赖 Bun |
| 本地存储 | 单个 `config.json` | 位于系统用户配置目录，不用数据库 |
| 密码存储 | 系统钥匙串（`zalando/go-keyring`） | 密码不落入 JSON；macOS 下调用系统 `security` 命令，同样无 cgo |
| SSH | `golang.org/x/crypto/ssh` + `pkg/sftp` | 每台服务器一条长连接，带 keepalive |
| 进程管理 | 服务器自带的 systemd | 负责后台运行、自启、重启、日志 |

整个项目不依赖 cgo，可以在 macOS 上直接交叉编译出 Windows 和 Linux 版本。首先适配 macOS。

## 3. 架构

```
本地电脑：Berth（MyGo 窗口）
            │ SSH / SFTP
   ┌────────┼────────┐
 服务器A   服务器B   服务器C     只需要 sshd + systemd
```

原则：**状态以服务器为准**。本地只保存"怎么连上服务器"，应用列表、版本、配置都从服务器读取。换电脑或丢失本地配置后，重新添加服务器即可恢复。

### 代码结构

```
core/                 不依赖桌面框架，可单独测试
  ssh/                连接池、host key 校验、命令执行、SFTP
  server/             环境检测、初始化、概况采集
  app/                应用增删、端口分配、unit 生成
  deploy/             上传、解压、切换、健康检查、回滚
  logs/               journalctl 流
  config/             config.json 读写、钥匙串
services/             MyGo 绑定层，只做转发和流式推送
frontend/
```

## 4. 服务器端约定

### 目录

```
~/senlinApps/<name>/
  releases/<时间戳>/     发布包解压后的内容
  current -> releases/<时间戳>
  shared/                持久文件，跨版本保留
  backups/               发版前对 shared/ 的备份
  app.json               应用配置
```

### 运行模式（按登录用户自动判定）

| 登录用户 | unit 位置 | 命令 | 开机自启前提 |
|---|---|---|---|
| root | `/etc/systemd/system/senlin-<name>.service` | `systemctl` | 无 |
| 普通用户 | `~/.config/systemd/user/senlin-<name>.service` | `systemctl --user` | 需开启 linger |

普通用户模式下，linger 未开启时用户级服务不会在开机时启动。添加服务器时检测，未开启则提供按钮执行 `sudo loginctl enable-linger <user>`（需要该用户有 sudo 权限），否则显示需要管理员执行的命令。

### unit 模板

```ini
[Unit]
Description=senlin app: {{name}}
After=network-online.target

[Service]
WorkingDirectory={{home}}/senlinApps/{{name}}/current
ExecStart={{home}}/senlinApps/{{name}}/current/{{entry}} {{args}}
EnvironmentFile=-{{home}}/senlinApps/{{name}}/shared/.env
Restart={{restart}}
RestartSec=3

[Install]
WantedBy={{target}}
```

说明：

- systemd 要求 `ExecStart` 为绝对路径，所以应用配置拆成"入口文件"和"启动参数"两项，不接受自由格式的命令行
- `{{target}}`：root 模式为 `multi-user.target`，用户模式为 `default.target`
- 开机自启 = `systemctl enable`，关闭 = `disable`，按应用单独开关
- `{{restart}}`：`on-failure`（默认）、`always`、`no`

### app.json

```json
{
  "name": "myapp",
  "entry": "myapp",
  "args": "serve --http=0.0.0.0:{{port}} --dir={{shared}}/pb_data",
  "port": 18001,
  "env": { "KEY": "value" },
  "shared": ["pb_data", "config.yaml"],
  "autostart": true,
  "restart": "on-failure",
  "health": { "type": "http", "path": "/api/health", "timeout": 10 },
  "backup": true,
  "keepReleases": 5,
  "current": "20261009-151800",
  "note": "官网后端 API，负责人 senlin"
}
```

`note` 是用户随时可改的备注（最多 2000 字），只改写 app.json，不影响 unit 和运行中的进程；在设置中保存其他字段时保留服务器上的备注。

`args` 可用变量：`{{port}}`、`{{shared}}`（shared 目录绝对路径）。

`shared` 列表中的路径在每次发版时软链进新的 release 目录，用于程序以相对路径读取配置或数据的情况。

## 5. 本地配置

`config.json`：

```json
{
  "servers": [
    {
      "id": "a1b2",
      "name": "阿里云-1",
      "host": "1.2.3.4",
      "port": 22,
      "user": "root",
      "auth": "password",
      "keyPath": "",
      "hostKey": "SHA256:...",
      "portRange": [18000, 18999]
    }
  ],
  "settings": { "theme": "system" }
}
```

- `auth`：`password` | `key` | `agent`
- 密码以 `berth/<server-id>` 为键存入系统钥匙串
- `hostKey`：首次连接时记录，之后不一致则拒绝连接并提示

## 6. 核心流程

### 添加服务器

1. 填写连接信息，测试连接
2. 展示 host key 指纹，确认后保存
3. 环境检测：systemd、运行模式、linger、CPU 架构、磁盘空间
4. 创建 `~/senlinApps/`
5. 扫描已有应用（读各目录下的 `app.json`）

### 服务器概况

一次 SSH 命令采集，页面可见时每 5 秒刷新，不保存历史。

| 项 | 来源 |
|---|---|
| 系统版本、内核、架构 | `/etc/os-release`、`uname -srm` |
| 运行时长、负载 | `/proc/uptime`、`/proc/loadavg` |
| CPU 核数、使用率 | `nproc`、两次读取 `/proc/stat` 的差值 |
| 内存、交换分区 | `/proc/meminfo`（已用 = 总量 − MemAvailable） |
| 磁盘 | `df -P`，重点显示 `senlinApps` 所在分区 |
| 各应用占用 | `systemctl show` 的 `MemoryCurrent`、`CPUUsageNSec` |
| 端口占用 | `/proc/net/{tcp,udp}{,6}` 的监听套接字；`/proc/<pid>/fd` 反查进程，`/proc/<pid>/cgroup` 判断是否属于 `senlin-*` 应用。不依赖 `ss`。root 可见全部进程，普通用户只能看到自己的进程名 |
| `senlinApps` 占用空间 | `du -sh`，按需手动刷新（目录大时较慢） |

磁盘剩余空间不足时，发版前给出警告。

### 新建应用

1. 选择预设（通用 / PocketBase），预设自动填充启动参数、持久路径、健康检查
2. 填写名称、入口文件、参数、端口（自动分配，可改，可留空）
3. 写入 `app.json`，生成 unit，`daemon-reload`

### 发版

1. 选择文件：单个可执行文件，或 zip / tar.gz
2. 校验入口文件的 ELF 架构与服务器是否一致
3. SFTP 上传到 `releases/<时间戳>/`，压缩包则解压，入口文件 `chmod +x`
4. 把 `shared` 列表中的路径软链进新 release
5. 停止服务
6. 备份 `shared/`（若开启）
7. 原子切换 `current` 软链
8. 启动服务
9. 健康检查
10. 失败：软链指回旧版本，恢复备份，启动旧版本，报告错误
11. 成功：清理超出 `keepReleases` 的旧版本

### 健康检查

| 类型 | 判定 | 适用 |
|---|---|---|
| `process`（默认） | 启动后 N 秒 systemd 仍为 active | 任何程序 |
| `tcp` | 服务器本地能连上端口 | 有监听端口的服务 |
| `http` | 服务器本地请求指定路径返回 2xx | Web 服务 |
| `none` | 不检查 | |

检查均在服务器本地回环进行，可以区分"程序没起来"和"安全组没放开端口"。

### 备份与恢复

备份下载到本地，一个 tar.gz，可以恢复到任意服务器（也用于迁移）：

```
manifest.json          应用名、来源服务器（含架构、home）、当前版本、备份时间、是否暂停
app.json               全部设置（含环境变量、备注）
shared/...             持久数据，不含由 app.json 生成的 .env
releases/<当前版本>/   正在运行的程序；可选包含全部历史版本
logs/journal.txt       可选，最近 N 天日志
```

- 服务器上 `tar -cz` 输出到 stdout，经 SSH 流式写到本地，服务器上不产生临时文件
- 可选"备份时暂停应用"：停止、打包、立即启动，保证数据库一致；使用数据库的应用默认开启
- 恢复前在本地校验整个包：路径不越界，符号链接不指向外部；程序架构必须与目标服务器一致
- 可改名、改端口（原端口被占用时自动建议空闲端口）；重建指向新服务器 shared/ 的软链，重新生成 .env 和 unit
- 同名应用已存在时须明确选择覆盖：先停止，原 shared/ 移到 backups/ 保留而不是删除
- 恢复后可启动并做健康检查

### 日志

- 实时：`journalctl -u senlin-<name> -f -o json`（用户模式加 `--user`），逐行通过 MyGo 的 channel 流式推到前端
- 历史：按时间范围和关键字查询
- 前端：虚拟滚动、搜索高亮、ANSI 颜色、暂停跟随

## 7. 界面

```
┌────────────┬──────────────────────────────────────┐
│ 服务器      │ 阿里云-1    1.2.3.4   ● 已连接        │
│ ● 阿里云-1  │ Ubuntu 24.04 · 2 核 · 运行 32 天      │
│ ○ 腾讯云-2  │ CPU 12%   内存 1.1/2G   磁盘 18/40G   │
│            ├──────────────────────────────────────┤
│ + 添加      │ 应用     端口    版本       状态  内存  │
│            │ myapp   18001   1009-1518  ● 运行 48M │
│            │ worker  -       1008-0930  ○ 停止  -  │
└────────────┴──────────────────────────────────────┘
```

应用详情页分四个标签：概览（状态、启停、打开访问）、发版（上传、历史版本、回滚）、日志、设置（参数、环境变量、自启、健康检查）。

## 8. 分期

### P0 骨架

- [x] MyGo 项目初始化，React + Tailwind + shadcn/ui
- [x] 主布局（服务器侧栏 + 内容区）
- [x] `core/config`：config.json 读写、钥匙串封装

### P1 服务器管理

- [x] `core/ssh`：三种认证、host key 校验、连接池、keepalive、断线重连
- [x] 添加、编辑、删除服务器，连接测试
- [x] 环境检测与运行模式判定
- [x] linger 检测与开启
- [x] 扫描服务器上已有应用
- [x] 服务器概况采集与展示
- [x] 端口占用：列出所有监听端口（对外/仅本机、进程、PID），识别属于哪个 Berth 应用，标出应用端口区间内被其他程序占用的端口；新建/编辑应用时即时提示端口冲突

### P2 应用与发版

- [x] 新建、删除应用，端口自动分配
- [x] unit 生成（root / 用户两种模式）
- [x] 预设模板：通用、PocketBase
- [x] 上传（带进度）、解压、ELF 架构校验
- [x] shared 软链、备份、原子切换
- [x] 四种健康检查
- [x] 失败自动回滚

### P3 运行管理

- [x] 启动、停止、重启
- [x] 开机自启开关、重启策略
- [x] 状态、当前版本、运行时长、内存与 CPU 占用显示
- [x] 实时日志与历史查询
- [x] 用 `IP:端口` 打开浏览器

### P4 增强

- [x] 手动回滚到任意历史版本
- [x] 环境变量编辑
- [x] 应用备注
- [x] 备份下载到本地，从备份恢复到任意服务器
- [x] 界面内编辑 `shared/` 下的配置文件
- [ ] SSH 隧道访问仅监听本地的端口
- [ ] 选择项目目录后自动交叉编译再发版
- [ ] 更多预设模板

P0–P3 完成后即可日常使用。

## 9. 风险与注意事项

| 项 | 说明 | 处理 |
|---|---|---|
| MyGo 非常新 | v0.3.x，单人维护，用户少，API 可能变动，遇到问题多半要自己读源码 | 逻辑全部放在 `core/`，壳层很薄，必要时换成 Wails v3 只需重写 `services/` |
| 安全组未放开端口 | 工具无法控制 | 健康检查通过但外网不通时给出明确提示 |
| 发版有数秒停机 | 单实例程序无法避免 | 接受 |
| `shared/` 体积大时备份慢 | 每次全量 | 可按应用关闭备份，只保留最近几份 |
| root 密码登录 | 国内云服务器常见，权限过大 | 支持，但界面上建议改用普通用户加密钥 |
| 纯 HTTP 访问 | 未备案无法使用域名与证书 | 敏感后台走 SSH 隧道（P4） |
| 压缩包解压 | 可能包含 `../` 路径 | 解压前校验路径，拒绝越界 |

## 10. 待确认

1. 端口默认区间 18000–18999 是否合适
