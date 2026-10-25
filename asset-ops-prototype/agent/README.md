# 栖云 Linux 基础设施 Agent

`qiyun-agent` 运行在 Linux / Proxmox VE 宿主机上，采集宿主机、登录用户、PVE 虚拟机与 LXC、Docker、Kubernetes 清单，主动上报给栖云 Go 服务。平台只做清册、资源与连接建议，**不保存 SSH/RDP 密码，也不通过 Agent 执行远程命令**。

## 采集内容

| 范围 | 内容 |
| --- | --- |
| 宿主机 | 主机名、OS/内核/架构、虚拟化类型、启动时间、运行时长、CPU 型号/核心/使用率、内存、磁盘挂载/用量、网卡收发、负载 |
| 用户 | `who` 当前会话、在线用户数、`lastlog` 最后登录、超过阈值的长期未登录用户、从未登录用户 |
| PVE | `pvesh` / `qm list` / `pct list` 的 VM 与 LXC；`/etc/pve` 配置中的 ostype、CPU、内存、磁盘、标签；QGA / `pct exec` / 静态 `net0` 的 IP；SSH/RDP 连接建议 |
| Docker | 版本、容器总数/运行数/镜像数、容器状态/镜像/端口、`docker stats` 的 CPU/内存 |
| Kubernetes | `kubectl` 的 context、节点就绪/角色/容量、Pod 总数与运行/Pending/Failed、异常 Pod 原因 |

PVE 的 IP 依赖 QEMU Guest Agent（`agent: 1`）或运行中的 LXC；没有 IP 时只上报配置与状态，不猜测地址。`lastlog` 在部分容器/PVE LXC 中不可用，会明确标记。

## 构建

Rust Agent 是 Linux 目标；Windows 上通过 WSL Docker 构建静态 musl 二进制：

```bash
cd /mnt/g/asset-ops-prototype/asset-ops-prototype/agent
docker run --rm --network host \
  -v "$PWD:/src" -w /src \
  rust:1.87-alpine /bin/sh /src/build/rust-alpine-build.sh
```

产物：`agent/qiyun-agent`，`file` 显示 `static-pie linked`，约 2.0 MB。构建脚本会先跑 29 项单元测试，再构建依赖并只对最终二进制启用 `+crt-static`（全局启用会破坏 proc-macro）。

有原生 Rust 1.87+ 和 gcc 的 Linux 也可以：

```bash
cargo test --release
cargo build --release
```

## 安装到 PVE / Linux

### 一条命令安装（推荐）

以 root 执行下载的二进制；它会把自己安装到 `/usr/local/bin/qiyun-agent`，生成 `/etc/qiyun-agent.env` 和 `/etc/systemd/system/qiyun-agent.service`，并执行 `enable` + `restart`：

```bash
sudo ./qiyun-agent --install \
  --server https://ops.example.com \
  --token '服务端 data/agent.token 的 64 位令牌' \
  --interval 60 \
  --stale-days 90 \
  --network-interfaces eno1,bond0
```

- `--network-interfaces` 可省略；省略时自动选择物理上联（有 bond/team 时优先 bond，避免与 slave 重复计数）。
- 已有 `/etc/qiyun-agent.env` 时默认保留，加 `--force-config` 才覆盖。
- 之后修改 `/etc/qiyun-agent.env`，执行 `systemctl restart qiyun-agent` 生效。
- 确认：`systemctl is-enabled qiyun-agent` 应为 `enabled`；`systemctl is-active qiyun-agent` 应为 `active`。

### 手动安装

1. 复制二进制并设置权限：

```bash
install -m 0755 qiyun-agent /usr/local/bin/qiyun-agent
```

2. 从平台服务端读取 Agent 令牌（首次启动自动生成，权限 0600）：

```bash
cat /path/to/qiyun/data/agent.token
```

也可以由平台管理员设置 `QIYUN_AGENT_TOKEN`；Agent 与服务端必须一致。

3. 创建 `/etc/qiyun-agent.env`：

```ini
QIYUN_SERVER=https://ops.example.com
QIYUN_AGENT_TOKEN=replace-with-agent-token
QIYUN_INTERVAL=60
```

平台若只在本机回环监听，Agent 必须与平台同机；跨机上报需要让 Go 服务监听局域网/HTTPS，并启用登录（`QIYUN_AUTH_MODE=on`、`QIYUN_ADMIN_PASSWORD`）。生产建议在反向代理后使用 HTTPS。

4. 安装 systemd：

```bash
install -m 0644 deploy/qiyun-agent.service /etc/systemd/system/qiyun-agent.service
systemctl daemon-reload
systemctl enable --now qiyun-agent
journalctl -u qiyun-agent -f
```

5. 打开平台「基础设施 → Agent 采集」，应看到节点与资源；首次上报可能需要一个采集周期（默认 60 秒），也可在 PVE 上先执行 `qiyun-agent --once` 验证。

### 网卡统计与准确性

- 只统计 `QIYUN_NETWORK_INTERFACES` 指定的接口，或自动选择的物理上联；**不会把所有 lo/veth/vmbr 简单相加**。
- 计数器直接读 `/sys/class/net/<iface>/statistics/{rx_bytes,tx_bytes,rx_packets,tx_packets,rx_errors,tx_errors,rx_dropped,tx_dropped}`（内核 64 位累计值）。
- 速率由两次采样的单调时钟差值计算：`(当前计数 - 上次计数) / 实际间隔秒数`；计数器归零/接口重置时该次速率为空，不会产生错误的大数。
- `sample_interval_seconds` 是实际间隔；默认 `--interval 60` 时速率是 60 秒平均值，不是瞬时峰值。需要更细的速率时把 `QIYUN_INTERVAL` 调到 5–10 秒（PVE/Docker 采集也会更频繁）。
- 手动指定 `bond0` 时不要再同时指定它的 slave；手动指定 `vmbr0` 会统计桥接口，可能与物理口重复。
- 主机 Agent 的字节计数不能替代计费级流量：驱动/offload/VLAN 会改变字节语义，虚拟交换会重复计数；计费应使用交换机端口 SNMP `ifHCInOctets`/`ifHCOutOctets` 或 sFlow/NetFlow。

## 命令

```text
qiyun-agent --install --server URL --token T --network-interfaces eno1
qiyun-agent --once --print                 # 采集并打印 JSON，不上报
qiyun-agent --once --server URL --token T  # 采集并上报一次
qiyun-agent --server URL --token T --interval 60 --stale-days 90 --network-interfaces eno1
```

环境变量：`QIYUN_SERVER`、`QIYUN_AGENT_TOKEN`、`QIYUN_INTERVAL`。依赖系统 `curl` 完成 HTTP(S) 上报，避免引入 OpenSSL/C 工具链；PVE 默认自带 curl。

## 权限与安全

- 读取 `/etc/pve`、执行 `pvesh`/`qm`/`pct`、读取 `lastlog` 通常需要 root；建议 systemd 以 root 运行，并限制主机网络暴露面。
- Agent 只读采集，不接收平台下发的命令；删除/启停 VM 等“管控”操作不在本版本范围。
- 令牌是共享秘密，放在 0600 的 env 文件或 `data/agent.token`；不要写入命令行历史或日志。Agent 上报时把令牌放在临时 0600 curl 头文件中，避免出现在进程参数。
- 平台侧 `POST /api/v1/agent/report` 需要 Bearer 令牌；浏览器查询接口沿用登录/`asset.read` 权限。
- Agent 上报主机名、IP、容器/VM 清单、用户登录信息；这些属于基础设施元数据，部署前确认合规要求。

## 接口

- `POST /api/v1/agent/report`：Agent 上报，Bearer 令牌，最大 4 MiB，返回 201。
- `GET /api/v1/agent/hosts`：节点摘要列表（CPU/内存/磁盘、用户数、PVE/Docker/K8s 计数）。
- `GET /api/v1/agent/hosts/{host_id}`：摘要 + 完整原始 payload（VM/容器/Pod/磁盘/网卡/用户明细）。

存储只保留每个 `host_id` 的最新上报，`agent_hosts` 表同时保存摘要列和原始 JSON；新增平台字段不需要数据库迁移。

## 已知边界

- PVE 之外没有真实 PVE 环境时，`pve.available=false` 并带原因；单元测试用固定 JSON/命令输出覆盖解析。
- QEMU 没有 Guest Agent、LXC 没有运行中的 `ip` 命令时，IP 为空，连接建议为空。
- Kubernetes 需要可读的 kubeconfig（`KUBECONFIG`、`~/.kube/config` 或 k3s 默认路径）和 `kubectl`。
- `docker stats --no-stream` 在容器很多时会变慢，Agent 对命令设置了超时；失败只影响统计字段，不影响容器清单。
- 采集周期默认 60 秒；UI 读的是最近一次上报，不做历史趋势（后续可扩展 `agent_reports` 历史表）。
