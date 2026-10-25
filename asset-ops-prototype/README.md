# 栖云资产应用与运维

当前项目由 Go 服务统一提供静态前端、SQLite 资产数据、AES-GCM 凭据存储、SSH 主机指纹核验和 WebSocket PTY 网关。Linux 终端不再回退到本地模拟会话：网关、凭据、主机指纹或 SSH 连接任一环节失败时，连接会直接失败。

## 构建与启动

需要 Go 1.26 或兼容版本。项目不需要 Python、Node 包安装或运行时 CDN。

```bash
go run ./cmd/build
go test ./...
go run ./cmd/qiyun
```

默认地址是 `http://127.0.0.1:8765/#/terminal`，持久数据写入 `./data/qiyun.db`，凭据主密钥写入权限为 `0600` 的 `./data/master.key`。首次启动会自动生成主密钥；生产环境应通过受控 secret mount 提供并备份该文件。

服务默认只允许监听 loopback。需要从其他设备访问时，必须同时设置管理令牌：

```bash
QIYUN_ADDR=0.0.0.0:8765 \
QIYUN_ADMIN_TOKEN='replace-with-a-long-random-token' \
QIYUN_ALLOWED_ORIGINS='ops.example.com' \
QIYUN_ALLOWED_CIDRS='10.0.0.0/8,172.16.0.0/12,192.168.0.0/16' \
go run ./cmd/qiyun
```

对外部署必须在 Go 服务前配置 HTTPS 反向代理。浏览器管理令牌只保存在 `sessionStorage`；终端 WebSocket 使用 60 秒、单次消费的随机票据，不把长期令牌放入 WebSocket URL。

## 添加并连接 Linux 资产

入口有三处，都是同一张表单（写 Go 服务资产库、凭据加密、需核验指纹）：

- 「主机与远程」页右上角 **＋ 添加主机**（就地弹出表单，不跳页）
- 「资产台账」页 **＋ 登记资产 → 添加 Linux 主机（真实 SSH）**
- 「远程终端」页 Linux 资产标题右侧的 `+`

1. 打开“远程终端”，点击 Linux 资产标题右侧的 `+`。
2. 选择认证方式：
   - **密码**：填写 SSH 登录密码，可点击“显示”核对。
   - **SSH 私钥**：点击“从本地选择私钥文件”直接读取 `id_rsa` / `id_ed25519` / `.pem`，或粘贴私钥内容；私钥设置了口令时填写口令。`.ppk` 需先用 puttygen 转换为 OpenSSH 格式。
3. 保存时服务端立刻校验凭据格式，格式不对会直接提示原因，不会等到指纹批准后才失败。
4. 服务执行 SSH 握手扫描并显示服务器公钥的 SHA-256 指纹。
5. 通过云平台控制台、服务器控制台或已有 `known_hosts` 独立核对指纹，再点击批准。
6. 批准后自动进入诊断：逐项列出凭据读取、私钥解析、地址解析、TCP 连接、主机指纹、SSH 认证、会话执行的通过状态与失败原因。
7. 全部通过后即可从资产树打开终端。任何环节失败，点击资产行的 `◎` 可随时重新诊断。

诊断会给出私钥对应的公钥行，可复制后追加到目标机：

```bash
mkdir -p ~/.ssh
echo '<点击“复制公钥”得到的整行>' >> ~/.ssh/authorized_keys
chmod 700 ~/.ssh && chmod 600 ~/.ssh/authorized_keys
```

诊断也可通过链接直接打开：`http://127.0.0.1:8765/#/terminal?diag=<资产ID>`。凭据填错时无需删除资产，点击诊断面板里的“修改凭据”即可原地替换，已批准的主机指纹会保留。

服务端日志同时记录每一步失败原因（`ssh diagnose failed`、主机密钥扫描失败等），不含明文凭据。

### 网络可达性（务必先确认）

服务只能连上它自己所在网络能到达的地址：

- 文件里的 `PVE-190` 这类局域网地址，如果服务跑在 WSL 里，WSL2 默认 NAT 无法访问 `192.168.x.x`，会报 `no route to host`。
- 解决办法二选一：在 Windows 本机运行服务（见下），或在 `%UserProfile%\.wslconfig` 设置 `networkingMode=mirrored` 后重启 WSL。
- 先用 Windows 的 `Test-NetConnection <IP> -Port <SSH端口>` 确认端口通，再到资产行点击 `◎` 诊断。

在 Windows 本机运行（推荐，直接使用 Windows 网络栈）：

```powershell
go build -o qiyun.exe ./cmd/qiyun
.\qiyun.exe -addr 127.0.0.1:8765 -data .\data -web .
```

浏览器只提交不可猜测的资产 ID 和终端尺寸。服务端重新解析目标地址、凭据和已批准主机公钥；默认仅允许 RFC 1918 私网，未知密钥或密钥变化都会阻断连接。

终端使用本地固定版本的 xterm.js、FitAddon 和 trzsz.js。键盘输入和 PTY 输出使用 WebSocket 二进制帧，支持 Ctrl-C、vim、top 等交互程序；每个分屏都有独立 SSH 会话和 trzsz 过滤器。远端安装 `trzsz` 后，运行 `trz` 可选择或拖放文件/目录上传，运行 `tsz <file>` 可下载。

## 大文件传输（独立于终端）

终端工具栏的「文件传输」打开右侧抽屉，走独立的分块传输通道，不占用交互终端会话：

```text
浏览器（8 MiB 分块） → Go 传输服务 → SFTP → 远程文件
```

- **上传**：先写远端临时文件 `<name>.qiyun-<id>.part`（故意不用点开头的隐藏名，方便在远程 `ls` 里看到进度），长度校验通过后才原子改名到目标路径，中断不会留下半截正式文件。
- **断点续传**：任务记录服务端确认的字节偏移。中断（含失败）后重新选择同一文件、同一路径、同一大小，会从该偏移继续；服务重启会把进行中任务标为「已暂停」，仍可续传。
- **覆盖已有文件**：目标已存在需显式勾选覆盖。覆盖依赖 `posix-rename@openssh.com` 扩展（OpenSSH 默认提供）实现原子替换；服务端不支持该扩展时退化为「先删后改名」，并会在失败时明确报错而不是静默留在临时文件里。
- **下载**：支持 HTTP Range，浏览器用 File System Access API 流式写盘并记录断点；不支持流式写盘时退回浏览器直接下载，此时不记录断点。
- **不依赖远端 trz/tsz**：只需远端开启 SFTP（OpenSSH 默认开启）。
- **边界**：单块上限 8 MiB、单文件上限 4 TiB、同时最多 4 个任务；内存占用与文件大小无关，但尚未验证 TB 级长时间传输，也未实现服务端校验和比对。

接口：`POST/GET /api/v1/transfers`、`PUT /api/v1/transfers/{id}/chunk?offset=`、`GET /api/v1/transfers/{id}/content`（支持 `Range`）、`POST /api/v1/transfers/{id}/complete`、`DELETE /api/v1/transfers/{id}`。任务元数据存放在 `data/transfers/`，审计写入 `audit_events`。

### 传输慢时先量链路，别改代码

每次上传结束都会打一行账本，把耗时拆成「收浏览器请求体」和「写远端」两段：

```text
INFO transfer upload completed bytes=78643200 elapsed_ms=10025 mib_per_sec=7.4 chunks=10 body_ms=895 write_ms=8865
```

`write_ms` 占九成以上是常态——瓶颈在 SSH 链路，不在这段代码。要用同一把尺子量链路本身，跑内置测速工具（不需要登录远端 shell，凭据从 `data/` 解密）：

```bash
go run ./cmd/xferbench -asset <asset_id> -size 64
#   A raw ssh (cat > file)          : 裸 SSH 通道（相当于 scp）
#   B raw ssh (cat > file + sync)   : 同上，含落盘
#   C sftp concurrent/64/WriteAt    : 本服务当前使用的写法
#   D sftp concurrent/256/WriteAt   : 加大在途窗口，用来确认窗口不是瓶颈
#   F N parallel ssh streams        : 多流聚合；不涨说明链路已饱和
#   G raw ssh download              : 反方向，判断链路是否不对称
go run ./cmd/xferbench -asset <asset_id> -exec 'sha256sum /tmp/x'   # 顺手跑远端命令
```

判断方法：**C 与 A 持平** 说明协议层已经没有可挤的水分，只能换链路；**F 的聚合随并行数上涨** 才说明单流被窗口/时延限制，值得做并行分块。实测一条 100 Mbit 级 VPN 上 C 比 A 还略快，此时 75 MiB 用 8.6～10 秒就是该链路的物理下限。

## 主机指标采集（SSH 只读）

「主机与远程」页面的 CPU / 内存 / 磁盘数据不是演示值，来自服务端每 60 秒一次的 SSH 只读采样：

- 采样内容：`/proc/stat`（两次相隔 0.3 秒算 CPU 使用率）、`/proc/meminfo`、`/proc/loadavg`、`/proc/uptime`、`df -P`、`/etc/os-release`、`uname -r`、`nproc`。全部在一个 SSH exec 内完成，避免逐条命令的往返开销。
- 复用已有的资产凭据与主机指纹校验（与终端、文件传输同一套），不额外信任任何主机。
- 只对 `host_key_status = verified` 的 Linux 主机采样；未核验资产直接跳过，不产生噪音日志。
- 失败按指数退避（1×、2×、4× … 最长 8× 间隔）重试，避免对不可达主机每轮都拨号。
- 历史样本保存在 `data/metrics/<asset_id>.json`，默认留 288 点（1 小时 × 60 秒）。

命令行与接口：

```bash
# 默认 60 秒；设为 0 或 off 可完全关闭后台采样（仍可用接口手动采集）
qiyun.exe -addr 127.0.0.1:8765 -metrics-interval 60s
# 等价环境变量
QIYUN_METRICS_INTERVAL=30s
```

- `GET /api/v1/metrics`：所有 Linux 主机的最新状态（`ok` / `error` / `pending`），失败的带原因和下次重试倒计时。
- `POST /api/v1/metrics/{asset_id}/collect`：立即采样一次（页面上的“立即采集”）。
- `GET /api/v1/metrics/{asset_id}/history?limit=120`：历史点，用于趋势。

失败语义：请求本身成功但主机采不到，接口返回 `200` + `status: "error"`，页面显示具体原因（如 `dial tcp 192.168.1.190:50022: i/o timeout`），不会伪装成 0%。上次成功的快照会保留并标注“数据已过期”。

**边界**：只覆盖 Linux（依赖 `/proc` 与 `df`）；Windows 需要 WinRM 或 Agent，尚未接入。采集命令需要目标账号能读取 `/proc` 并执行 `df`、`awk`、`sed`；受限 shell（如 rbash）或非 POSIX 默认 shell 会失败并如实报错。

## 查看日志与诊断

服务把每个阶段写入标准错误（Windows 下即控制台窗口）：

```
INFO  http request method=POST path=/api/v1/terminal-sessions status=201 duration_ms=0
INFO  terminal websocket requested session_id=… origin=http://127.0.0.1:8765
INFO  ssh connected asset_id=… target=192.168.1.190:50022 user=meitaolin auth_type=password
INFO  terminal session open  session_id=… target=192.168.1.190:50022
INFO  terminal session closed session_id=… bytes_in=35 bytes_out=644 reason=…
WARN  ssh diagnose failed stage=authenticate target=… key_fingerprint=SHA256:…
```

- 每条 API 日志都带 `status`，4xx/5xx 用 `WARN` 输出；终端 WebSocket 握手失败、票据过期、SSH 连接失败、PTY 申请失败都会单独记录原因。
- 日志不记录密码、私钥或密文，只记录资产 ID、目标地址、用户名、公钥指纹和错误文本。
- 需要查看持久化状态（资产、终端会话、审计事件）时：

```bash
go run ./tools/dbinspect -db data/qiyun.db -limit 20
```

Windows 上数据库被服务占用，需先停止服务或在本机执行：

```powershell
go build -o dbinspect.exe ./tools/dbinspect
.\dbinspect.exe -db .\data\qiyun.db
```



- `cmd/qiyun`：HTTP/API/静态文件服务入口。
- `cmd/build`：Go 前端组装器。
- `internal/store`：SQLite 迁移、资产、凭据、会话和审计数据。
- `internal/secretbox`：AES-256-GCM 凭据加密，AAD 绑定凭据身份。
- `internal/sshgateway`：目标网段限制、指纹校验、SSH PTY 和 WebSocket 桥接。
- `terminal-gateway.js`：资产 API 与单次票据 WebSocket 客户端。
- `terminal-workspace.js`：保留原有资产树、标签、分屏和同步输入界面。
- `vendor`：固定版本前端依赖及许可证。
- `tools/dbinspect`：只读查看资产、终端会话和审计事件。

## 验证

```bash
go test ./...
node --test workflow.test.cjs monitoring-model.test.cjs terminal-workspace.test.cjs \
  terminal-gateway.test.cjs transfer-ui.test.cjs infra-hosts.test.cjs host-metrics.test.cjs
```

当前：Go 全量通过，前端 71 项通过。传输相关的回归覆盖了分块顺序（不重不漏）、失败任务续传、覆盖语义（含服务端不支持 `posix-rename` 的退化路径）、以及移除任务后清理远端临时文件。

资产、凭据、主机指纹和 Linux SSH 终端已经接入真实持久化链路。原有采购、盘点、财务、PVE、Windows RDP、监控和通知页面仍保留现有界面与交互，但尚未接入相应外部系统；服务不会把这些页面中的样例快照宣称为真实采集结果。它们需要分别配置 PVE、RDP 网关、SNMP/Agent、OIDC 和企业微信适配器后才能用于生产操作。
