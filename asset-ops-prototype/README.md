# 栖云资产应用与运维

Go 服务统一提供静态前端、SQLite 资产数据、AES-GCM 凭据存储、SSH 终端/指标、Windows RDP，以及 Linux/PVE Agent 采集。前端由 `cmd/build` 把 CSS/JS 组装进 `index.html`，不需要运行时 CDN。

## 构建与运行

需要 Go 1.26 或兼容版本。

```bash
go run ./cmd/build
go test ./...
go run ./cmd/qiyun
```

默认地址 `http://127.0.0.1:8765/`，数据写入 `./data/qiyun.db`，主密钥为 `./data/master.key`（0600）。服务默认只监听 loopback；对局域网/中心部署必须同时启用登录和 HTTPS，详见 `IT-CHECKLIST.md`。

Windows 上可直接使用 `qiyun.exe`，或用 `start-qiyun.cmd`、桌面快捷方式启动。`start-qiyun.cmd` 会先检查 8765 是否被占用，不会杀掉已有服务。

## 源码开发即运行

开发时不要用安装包或 `qiyun.exe`，直接跑源码：

```powershell
# 本机单用户，默认 8770，数据在 data-dev
.\dev.ps1 -Mode local

# 中心多用户，首次启动会提示设置管理员密码
.\dev.ps1 -Mode central -Port 8770

# 另开终端跑连接器源码（监听 8766）
.\dev-connector.ps1 -Central http://127.0.0.1:8770
```

`dev.ps1` 会执行 `go run ./cmd/build` 从 `core.js` / `auth-ui.js` / `infra.js` / `terminal-*.js` 重新组装 `index.html`，然后 `go run ./cmd/qiyun` 启动服务。默认端口 **8770**，不会占用正式实例的 8765。

- 改前端：重跑 `go run ./cmd/build`（或重跑 `dev.ps1`），浏览器刷新；不要直接改生成的 `index.html`；
- 改 Go：`Ctrl+C` 停掉，重跑 `dev.ps1`；`go run` 会重新编译；
- 只重启 Go、不重建前端：`.\dev.ps1 -Mode local -Port 8770 -NoBuild`；
- 测试：`go test ./...`，前端用 `node <name>.test.cjs`（不要用 `node --test`，当前环境子进程受限）。

### 允许所有 IP 访问（局域网/开发）

```powershell
.\dev.ps1 -Mode central -AllowAllIPs -Port 8770
```

- 监听 `0.0.0.0:8770`，所有网卡可达；
- 自动强制登录（不能匿名开放到局域网）；首次启动提示设置管理员密码；
- 启动后会打印可用的局域网 URL；
- 其他电脑访问前，Windows 防火墙需放行端口（管理员 PowerShell）：
  ```powershell
  New-NetFirewallRule -DisplayName 'Qiyun Dev 8770' -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8770 -Profile Private
  ```
- 这是明文 HTTP，只适合可信局域网；公网/生产必须用 HTTPS 反向代理；
- 远程浏览器里的“连接远程桌面”仍不会打开那台电脑的 `mstsc`：远程电脑要装瘦连接器，或使用 Guacamole。不要删除 RDP 回环校验来“解决”这个问题。

如果要在正式 8765 上监听所有网卡，先停掉桌面启动器/`qiyun.exe`，再：

```powershell
.\dev.ps1 -Mode central -Port 8765 -AllowAllIPs
```

中心安装包也可以直接传 `-Listen '0.0.0.0:8765'`，但同样只建议在 HTTPS 反向代理后面或可信局域网使用。

## Windows 桌面安装（免命令行）

```powershell
.\build-qiyun.ps1 -Test
powershell -ExecutionPolicy Bypass -File .\install-qiyun.ps1
```

安装后桌面出现 **Qiyun Asset Ops**：服务未运行时自动启动、健康检查通过后打开浏览器；登录 Windows 时由启动文件夹中的后台快捷方式以 `-watch` 启动看门狗。卸载：

```powershell
powershell -ExecutionPolicy Bypass -File .\uninstall-qiyun.ps1
# 连同加密资产库删除：-RemoveData
```

新机器可复制 `dist\QiyunAssetOps.zip`，解压后双击 `setup.cmd`，安装到 `%LOCALAPPDATA%\QiyunAssetOps`。每台机器的 `data` 目录独立，不会自动共享资产；需要共享时使用中心资产服务或 Web RDP 网关。

## 本机 RDP（mstsc）

Windows 资产走本机 `mstsc`，不在浏览器里渲染：

```text
浏览器 → Go 服务（读取资产托管密码）→ PowerShell 启动器（临时凭据 + 签名 .rdp）→ 本机 mstsc
```

- 「主机与远程」→ 添加 Windows 资产，填 RDP 端口、账号、密码；
- 首次使用点「首次启用签名」，按提示允许证书和 UAC；之后启动不再出现发布者确认；
- 页面的「连接远程桌面」只表示本机 `mstsc` 已启动，不保证远端登录成功；
- 启动器约 30 秒后清理临时 `TERMSRV/<host>` 凭据和 `.rdp` 文件。

Windows 资产不扫描 SSH 指纹，也不能创建 SSH 终端；`POST /api/v1/terminal-sessions` 返回 `409 RDP_ASSET`。

## 浏览器 RDP / 连接器（可选）

除本机 `mstsc` 外，代码还包含两种中心化路径：

- `cmd/connector`：Windows 瘦连接器，中心页面签发一次性票据，连接器兑换后在**用户本机**启动 `mstsc`；
- `internal/guacamole`：配置 `QIYUN_GUACAMOLE_URL` 后由 Guacamole 提供浏览器内远程桌面。

这两条路径的部署/权限说明见 `IT-CHECKLIST.md` 与 `central.config.example.json`。普通单机使用不需要它们。

## Linux SSH、指标与传输

- Linux 资产使用 SSH 密码或私钥，首次连接需核验主机指纹；未核验资产不会自动信任。
- 终端是 WebSocket PTY；文件传输走独立 SFTP 分块 API，支持断点续传，不依赖浏览器插件。
- 后台每 60 秒通过 SSH 采集 `/proc`、`df` 等只读指标；`GET /api/v1/metrics` 查询，`QIYUN_METRICS_INTERVAL=0` 可关闭。
- 指标只覆盖 Linux；Windows 资源由下面的 Agent 或后续 WinRM 接入。

## Linux / PVE Agent 采集

Rust Agent 运行在 Linux/PVE 宿主机上，主动上报宿主机、登录用户、PVE 虚拟机/LXC、Docker、Kubernetes 清单。

### 采集内容

- 宿主机：CPU/内存/磁盘/网卡/负载/运行时长、OS/内核/架构、虚拟化类型；
- 用户：`who` 当前会话、在线人数、`lastlog` 最后登录、长期未登录/从未登录；
- PVE：QEMU/LXC 状态、ostype、CPU/内存/磁盘、QGA/`pct exec`/静态 IP、SSH/RDP 连接建议；
- Docker：容器/镜像数、状态、端口、`docker stats` CPU/内存；
- Kubernetes：context、节点就绪/角色/容量、Pod 数量与异常 Pod 原因。

### 构建

WSL/Docker 构建静态 musl 二进制：

```bash
cd asset-ops-prototype/agent
docker run --rm --network host -v "$PWD:/src" -w /src \
  rust:1.87-alpine /bin/sh /src/build/rust-alpine-build.sh
```

产物 `agent/qiyun-agent`（约 1.6 MB，`static-pie linked`）。脚本先跑 12 项单元测试，再构建依赖并只对最终二进制启用 `+crt-static`。

### 安装到 PVE

1. 复制二进制：

```bash
install -m 0755 qiyun-agent /usr/local/bin/qiyun-agent
```

2. 在平台服务端读取令牌：

```bash
cat data/agent.token
```

首次启动自动生成，权限 0600；也可用 `QIYUN_AGENT_TOKEN` 覆盖。

3. 创建 `/etc/qiyun-agent.env`：

```ini
QIYUN_SERVER=https://ops.example.com
QIYUN_AGENT_TOKEN=服务端 data/agent.token 的 64 位令牌
QIYUN_INTERVAL=60
```

4. systemd：

```bash
install -m 0644 deploy/qiyun-agent.service /etc/systemd/system/qiyun-agent.service
systemctl daemon-reload
systemctl enable --now qiyun-agent
journalctl -u qiyun-agent -f
```

5. 打开平台：`http://平台地址:8765/#/agents`，或左侧「基础设施 → Agent 采集」。页面顶部「＋ 接入 Agent」会给出下载地址、令牌和可复制的命令。

### Agent 实时仪表盘

Agent 页面是独立仪表盘，不是一张摘要表：

- **顶部实时控制**：最后更新时间、下次倒计时、5/10/30 秒周期、暂停/继续、立即刷新；页面隐藏时暂停轮询，回到前台自动补拉。
- **大规模布局**：面向 100–1000 台设计，使用紧凑可排序表格而不是一机一卡；服务端分页（25/50/100/200 每页），1000 台也只渲染当前页。
- **汇总指标**：节点数、CPU/内存平均值、最高磁盘、PVE VM/CT、Docker 运行容器、K8s 节点/Pod、登录用户；汇总来自服务端对**全量筛选结果**的聚合，不是当前页样本。
- **科学口径**：区分「Agent 采集时间」和「服务端接收时间」；≤2 分钟为新鲜、2–10 分钟为延迟、超过 10 分钟为过期；旧数据明确标黄/标红，不补零。
- **可视化**：资源水位条、新鲜度分段、平台覆盖、主机状态点、CPU/内存/磁盘迷你条。
- **完整详情**：系统/内核/架构/虚拟化/启动时间、CPU/内存/负载、所有磁盘、网络接口速率与累计量/错误/丢包、登录用户/长期未登录、PVE VM 与 LXC、Docker 容器、K8s 节点与异常 Pod，以及完整原始 JSON。
- **筛选/排序**：服务端搜索主机名/ID/系统，按新鲜度/平台筛选，按名称/CPU/内存/磁盘/上报时间排序，支持分页跳转。
- **记录生命周期**：可登记“等待上报”的占位节点、编辑显示名/标签/备注、单删、勾选批量删、清理所有过期记录，或按“超过 N 天未上报”清理。指标本身只由 Agent 上报，手工不可伪造。

数据来自 Agent 主动上报的**最新快照**；服务端按 `host_id` 覆盖保存，不做历史库。页面每 5 秒重新读取一次最新快照，主机的实际上报周期由 `qiyun-agent --interval` 决定（默认 60 秒）。

**过期不会自动删除。** “过期”只是数据新鲜度状态；记录会保留，直到运维执行删除/清理。原因：离线主机可能只是网络维护，自动删除会丢失资产上下文。页面提供三种处理：

1. 单节点删除；
2. 勾选当前页批量删除；
3. “清理过期”：删除所有接收时间超过 10 分钟的节点，或按“超过 N 天未上报”清理。

登记了但从未上报的占位节点没有接收时间，不会被“清理过期”删除，需要在表格中勾选后删除。删除后如果 Agent 仍在上报，会以同一 Host ID 重新出现；要永久下线，应在目标机停止/卸载 Agent。

### 平台接口

| 方法 | 路径 | 认证 | 用途 |
| --- | --- | --- | --- |
| POST | `/api/v1/agent/report` | `Authorization: Bearer <agent-token>` | Agent 上报，最大 4 MiB |
| GET | `/api/v1/agent/hosts` | 登录会话 / `asset.read` | 节点摘要分页 + 全量筛选汇总；`page/page_size/q/status/platform/sort` |
| POST | `/api/v1/agent/hosts` | `asset.write` | 登记等待上报的占位节点（host_id/hostname/显示名/标签/备注） |
| PATCH | `/api/v1/agent/hosts/{host_id}` | `asset.write` | 修改显示名/标签/备注（不改上报指标） |
| DELETE | `/api/v1/agent/hosts/{host_id}` | `asset.write` | 删除单个节点 |
| POST | `/api/v1/agent/hosts/bulk-delete` | `asset.write` | 批量删除 `{ids:[...]}` |
| POST | `/api/v1/agent/hosts/purge` | `asset.write` | `{only_stale:true}` 或 `{older_than_days:30}` 清理过期 |
| GET | `/api/v1/agent/hosts/{host_id}` | 登录会话 / `asset.read` | 摘要 + 完整 payload |
| GET | `/api/v1/agent/enrollment` | 管理员 | 返回接入地址、令牌、下载路径 |

示例：

```bash
TOKEN=$(cat data/agent.token)
curl -X POST http://127.0.0.1:8765/api/v1/agent/report \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  --data @agent/build/sample-report.json

curl 'http://127.0.0.1:8765/api/v1/agent/hosts?page=1&page_size=50&status=fresh&platform=pve&sort=cpu'
curl http://127.0.0.1:8765/api/v1/agent/hosts/demo-pve
```

Agent 自检与上报：

```bash
qiyun-agent --once --print
qiyun-agent --once --server http://平台地址:8765 --token "$TOKEN"
```

完整字段、PVE 解析和边界见 [agent/README.md](agent/README.md)。

### 网络前提

当前 Windows 实例只监听 `127.0.0.1:8765`，PVE 主机无法访问它。真实 PVE 上报必须把 Go 服务部署到 PVE 可达的服务器：

```bash
QIYUN_ADDR=0.0.0.0:8765 \
QIYUN_AUTH_MODE=on \
QIYUN_ADMIN_PASSWORD='强密码' \
QIYUN_AGENT_TOKEN='与 Agent 一致' \
qiyun.exe
```

并在前面配置 HTTPS 反向代理，Agent 使用 `QIYUN_SERVER=https://...`。不要直接把无 TLS 的令牌接口暴露到公网。

### 远程连接边界

Agent 只上报 VM/LXC 的 IP、OS 类型和建议协议（Windows→RDP 3389，Linux→SSH 22），**不保存凭据、不自动创建资产、不启停 VM、不执行命令**。实际远程会话仍在「主机与远程」中新增资产并填写凭据，走现有 SSH/RDP 链路。

## 集中部署与权限

中心模式支持本地用户、PBKDF2 密码、会话 Cookie、CSRF、admin/operator/auditor/viewer 角色和资产级 `can_connect`/`can_edit` 授权。安装步骤、HTTPS、防火墙和验收清单见 `IT-CHECKLIST.md`。

中心服务必须：

- `QIYUN_ADDR=0.0.0.0:8765`（或反向代理回环）；
- `QIYUN_AUTH_MODE=on` + 强 `QIYUN_ADMIN_PASSWORD`；
- 配置 HTTPS/反向代理、`QIYUN_ALLOWED_ORIGINS`、`QIYUN_ALLOWED_CIDRS`；
- 不要用普通 Windows 服务（Session 0）运行需要弹出 `mstsc` 的连接器；连接器按当前用户登录启动。

## 测试

```bash
go test ./...
node --test workflow.test.cjs monitoring-model.test.cjs terminal-workspace.test.cjs \
  terminal-gateway.test.cjs transfer-ui.test.cjs infra-hosts.test.cjs host-metrics.test.cjs \
  rdp-ui.test.cjs auth-ui.test.cjs agent-ui.test.cjs
```

当前：Go 全量通过；前端 **103 项**通过（含 Agent 页面 4 项）；Rust Agent 12 项通过，并完成 WSL“Rust Agent → Linux Go 服务 → 列表/详情”的 HTTP 201 端到端验证。

## 目录

- `cmd/qiyun`：HTTP/API/静态文件服务入口。
- `cmd/launcher`：Windows GUI 启动器/看门狗。
- `cmd/connector`：中心模式 Windows 瘦连接器。
- `cmd/build`：前端组装器。
- `internal/store`：SQLite 迁移、资产、凭据、用户/会话/授权、审计、Agent 最新上报。
- `internal/auth`：PBKDF2、会话、角色/权限、CSRF。
- `internal/secretbox`：AES-256-GCM 凭据加密。
- `internal/sshgateway`：网段限制、指纹校验、SSH PTY/WebSocket。
- `internal/rdp`：本机 `mstsc` 启动器、签名、临时凭据、审计。
- `internal/guacamole`：Guacamole REST 接入。
- `internal/agents`：Agent 上报模型/校验/摘要；`internal/store/agent_store.go` 持久化。
- `agent`：Rust Linux/PVE Agent、systemd、构建脚本。
- `agent-ui.js`：Agent 采集页面与详情弹窗。
- `install-*.ps1` / `package-*.ps1`：Windows 安装与便携包。
- `vendor`：固定前端依赖；`tools/dbinspect`：只读查看数据库。