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
node --test workflow.test.cjs monitoring-model.test.cjs terminal-workspace.test.cjs
```

资产、凭据、主机指纹和 Linux SSH 终端已经接入真实持久化链路。原有采购、盘点、财务、PVE、Windows RDP、监控和通知页面仍保留现有界面与交互，但尚未接入相应外部系统；服务不会把这些页面中的样例快照宣称为真实采集结果。它们需要分别配置 PVE、RDP 网关、SNMP/Agent、OIDC 和企业微信适配器后才能用于生产操作。
