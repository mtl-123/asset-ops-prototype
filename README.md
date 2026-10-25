# 栖云 · 资产应用与运维

以资产为入口的 IT 运维平台：Go 单体服务 + 原生前端，提供真实 SSH 远程终端、Linux 资产台账与主机指纹核验。

## 组成

| 目录 | 说明 |
| --- | --- |
| `asset-ops-prototype/` | 主应用：Go 服务、前端页面、测试与设计文档 |
| `tools/diagram-render/` | 图表生成工具（以 SVG 为权威源，替代原 Python + Pillow 脚本） |
| `物品资产-角色审批与财务对账图.*` | 资产角色与财务对账图（SVG/PNG/HTML） |
| `福利物资-角色审批关系图.*` | 福利物资审批角色关系图 |
| `福利物资审计思维导图.*` | 审批与审计逻辑思维导图 |
| `*需求初稿.md`、`*字段与规则.md` | 业务需求与字段规则 |

## 快速开始

需要 Go 1.26+，不需要 Python 或前端包管理器。

```bash
cd asset-ops-prototype
go run ./cmd/build      # 组装前端 index.html
go test ./...           # 后端测试
go run ./cmd/qiyun      # 启动服务
```

打开 <http://127.0.0.1:8765/#/terminal>。

Windows 上若要让服务访问局域网内生产主机，直接在 Windows 本机运行（WSL2 默认 NAT 到不了局域网）：

```powershell
go build -o qiyun.exe ./cmd/qiyun
.\qiyun.exe -addr 127.0.0.1:8765 -data .\data -web .
```

## 能力

- **资产增删改查**：`POST/GET/PATCH/DELETE /api/v1/assets`，支持按关键字、部门、环境、指纹状态过滤。
- **真实 SSH 终端**：WebSocket + PTY 二进制帧，xterm.js 渲染，多标签与横/纵/网格分屏，同步输入。
- **大文件传输**：独立于终端的 Go SFTP 分块通道，支持断点续传与覆盖，不依赖远端 `trz`/`tsz`。
- **主机指纹**：新增资产后扫描、人工核对 SHA-256、批准后固定；改 IP 或端口会自动作废指纹并要求重新核验；密钥变化返回 `HOST_KEY_MISMATCH`。
- **连接诊断**：凭据读取 → 私钥解析 → 地址解析 → TCP → 主机指纹 → SSH 认证 → 命令执行，逐项给出原因与建议，并显示私钥对应的公钥行便于比对 `authorized_keys`。
- **凭据安全**：AES-256-GCM 加密存储（AAD 绑定凭据身份），主密钥只落在服务端 `data/master.key`；浏览器只提交资产 ID 与终端尺寸。
- **审计**：资产变更、指纹扫描/批准、诊断、会话起止、关闭原因与字节数写入 `audit_events`。

## 安全默认

- 默认只监听 `127.0.0.1`；绑定非本机地址且未设置 `QIYUN_ADMIN_TOKEN` 时拒绝启动。
- 仅允许私网目标（`QIYUN_ALLOWED_CIDRS`，默认 `10/8`、`172.16/12`、`192.168/16`），阻断 loopback 与云 metadata 地址。
- 终端 WebSocket 使用 60 秒单次票据，长期令牌不进 URL。
- 连接失败一律失败收敛，不回退到演示会话。

## 不提交到仓库的内容

`.gitignore` 排除了 `asset-ops-prototype/data/`（主密钥与含加密凭据的 SQLite）、构建产物 `*.exe`，以及记录真实内网 IP、用户名、主机指纹的 `qa-evidence/`。

## 文档

- 应用说明与运维手册：[`asset-ops-prototype/README.md`](asset-ops-prototype/README.md)
- 原始设计：[`asset-ops-prototype/设计说明.md`](asset-ops-prototype/设计说明.md)
- 真实接入方案：[`asset-ops-prototype/平台扩展与真实接入设计.md`](asset-ops-prototype/平台扩展与真实接入设计.md)
- 监控优化背景：[`asset-ops-prototype/监控优化说明.md`](asset-ops-prototype/监控优化说明.md)
