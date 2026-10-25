# 栖云中心服务 · IT 部署清单

## 1. 架构

```text
用户浏览器 ──HTTPS──> 反向代理 ──HTTP 127.0.0.1:8765──> qiyun.exe（中心服务）
                                                        │
                                                        ├─ 资产/凭据/账号/权限/审计
                                                        └─ 连接器下载

Windows 用户电脑：qiyun-connector.exe（登录自启、只监听 127.0.0.1:8766）
        └─ 用一次性票据向中心换取 RDP 参数 → 本机 mstsc
```

中心服务不需要桌面会话，也不启动 mstsc；它负责登录、权限和票据。RDP 在用户电脑上由连接器启动。

## 2. 服务器要求

- Windows Server 2019/2022 或 Windows 10/11（建议独立服务器/虚拟机）；
- 2 vCPU、2 GB 内存、20 GB 磁盘起；
- 固定内网 IP，最好有 DNS 名称（例如 `ops.example.com`）；
- 管理员权限（安装计划任务和写 `C:\Program Files`、`C:\ProgramData`）；
- 能访问要管理的 Windows 资产 `3389`（如果走 Guacamole 还需要额外网关）；
- 防火墙只对外开放 **443**；`8765` 只允许本机反向代理访问。

## 3. 安装（IT 一次性）

1. 把 `QiyunCentral.zip` 复制到服务器，解压到临时目录（例如 `C:\Temp\QiyunCentral`）。
2. 右键开始菜单 → **终端(管理员)** / PowerShell(管理员)。
3. 进入解压目录，执行：

```powershell
cd C:\Temp\QiyunCentral
powershell -ExecutionPolicy Bypass -File .\install-central.ps1 `
  -PublicUrl 'https://ops.example.com' `
  -AdminUser 'admin'
```

脚本会提示输入初始管理员密码（至少 8 位）。也可以显式传 `-AdminPassword`，但不建议留在命令历史里。

安装脚本会：

- 复制程序到 `C:\Program Files\QiyunAssetOps`；
- 创建数据目录 `C:\ProgramData\QiyunAssetOps\data`；
- 写入 `central.config.json`（首次启动包含临时管理员密码）；
- 注册 **QiyunAssetOps** 计划任务（SYSTEM，开机启动，失败自动重启）；
- 启动服务并等待 `http://127.0.0.1:8765/healthz`；
- 初始化完成后从配置中删除明文管理员密码。

验证：

```powershell
Invoke-WebRequest http://127.0.0.1:8765/healthz
Get-ScheduledTask QiyunAssetOps
```

## 4. HTTPS 反向代理（必须）

中心服务只监听 `127.0.0.1:8765`，由反向代理提供 HTTPS。推荐 Caddy（自动证书）：

```caddyfile
ops.example.com {
    reverse_proxy 127.0.0.1:8765
}
```

Caddy 默认支持 WebSocket 和 `X-Forwarded-Proto`。

如果使用 IIS/Nginx：

- 反向代理到 `http://127.0.0.1:8765`；
- 保留 `Host`，设置 `X-Forwarded-Proto: https`；
- 允许 WebSocket Upgrade（终端路径 `/api/v1/terminal-sessions/*/ws`）；
- 上传上限至少 100 MB（大文件分块传输）。

DNS 把 `ops.example.com` 指向服务器；防火墙只放行 443。

## 5. 首次登录与权限

1. 浏览器打开 `https://ops.example.com`；
2. 用 `admin` + 安装时密码登录；
3. 右上角用户菜单 → **修改密码**（当前界面通过“用户管理 → 重置密码”完成）；
4. 用户管理 → 创建运维/审计/只读账号；
5. 资产台账 → 编辑 → 授权：给用户勾选可连接/可编辑；
6. 主机与远程 → 添加 Windows/Linux 资产；
7. 审计日志：管理员/审计角色可查看。

建议：

- 日常操作不要共用 `admin`；
- 离职/换岗先停用账号，再回收资产授权；
- 资产密码修改后，在资产编辑 → 修改凭据更新。

## 6. 连接器分发（普通用户）

1. 中心页面 Windows 资产 →「远程」→ 检测到未安装时显示下载；
2. 或直接访问 `https://ops.example.com/downloads/QiyunConnector.zip`；
3. 用户解压，双击 `install-connector.cmd`；
4. 如安装器提示，输入中心地址 `https://ops.example.com`；
5. 连接器安装到 `%LOCALAPPDATA%\QiyunConnector`，注册登录自启；
6. 回到中心页面点「重新检测」→「首次启用签名」→ 按提示允许 UAC；
7. 点「连接远程桌面」，本机 `mstsc` 打开。

连接器只监听 `127.0.0.1:8766`，不开放局域网；票据 90 秒、单次使用。

## 7. 备份与升级

备份（先停任务，复制数据目录）：

```powershell
Stop-ScheduledTask QiyunAssetOps
Copy-Item C:\ProgramData\QiyunAssetOps\data D:\backup\qiyun-data -Recurse
Start-ScheduledTask QiyunAssetOps
```

必须备份 `data\qiyun.db` 和 `data\master.key`；丢失 `master.key` 后已保存凭据无法解密。

升级：

```powershell
Stop-ScheduledTask QiyunAssetOps
# 替换 C:\Program Files\QiyunAssetOps 下的 qiyun.exe / index.html / vendor / assets
Start-ScheduledTask QiyunAssetOps
```

## 8. 卸载

```powershell
cd 'C:\Program Files\QiyunAssetOps'
powershell -ExecutionPolicy Bypass -File .\uninstall-central.ps1
# 连同资产库一起删除：
powershell -ExecutionPolicy Bypass -File .\uninstall-central.ps1 -RemoveData -RemoveFiles
```

不会删除反向代理、DNS、连接器签名证书/策略。

## 9. 验收清单

- [ ] `https://ops.example.com` 打开登录页，证书有效；
- [ ] `admin` 能登录，错误密码被拒绝；
- [ ] 创建运维账号，角色/资产授权生效；
- [ ] 运维账号只看到被授权资产；
- [ ] 连接器从中心下载、安装、登录自启；
- [ ] 首次签名 UAC 完成；
- [ ] 本机 `mstsc` 能连 Windows 资产；
- [ ] 审计日志能看到登录、资产、授权、RDP 事件；
- [ ] 服务器重启后计划任务自动拉起服务；
- [ ] `8765` 未对公网开放；
- [ ] `data\qiyun.db`、`master.key` 已纳入备份。

## 10. 注意

- 中心服务必须以 HTTPS 对外；明文 HTTP 会暴露登录密码、Cookie 和 RDP 票据兑换响应中的凭据。
- 不要用 Windows 服务（Session 0）替代连接器：`mstsc` 必须在用户桌面会话中启动。
- Guacamole 浏览器网关是可选组件；当前对 `10.0.0.4` 存在目标 RDP 安全协商兼容问题，不能作为唯一方案。默认先使用本机 `mstsc` 连接器。
- 连接器安装不需要管理员权限；首次签名会请求一次 UAC。

## 11. 常见问题

- **重装安装包会重置管理员密码吗？** 不会。用户表已有账号时，安装器传入的初始密码不会覆盖现有账号；请用现有管理员登录后在「用户管理」重置。
- **忘记唯一管理员密码怎么办？** 需要停服务并删除/恢复 `data\qiyun.db`（会丢失账号，不丢资产），或从备份恢复；没有命令行重置入口。
- **连接器让用户输入中心地址太麻烦？** 在打包机运行：
  ```powershell
  .\package-connector.ps1 -CentralUrl 'https://ops.example.com'
  .\package-central.ps1 -SkipBuild
  ```
  然后把新的 `downloads\QiyunConnector.zip` 放入中心，安装器会自动带上地址。
- **HTTPS 页面检测不到本机连接器？** 浏览器可能拦截 HTTPS→HTTP localhost 的 Mixed Content/Private Network Access。优先使用 Chrome/Edge；仍失败时需要自定义协议或本机 TLS，当前版本未实现。
- **服务日志在哪？** `C:\ProgramData\QiyunAssetOps\logs\qiyun.log` 和 `launcher.log`。
- **开机没有自动启动？** 检查计划任务 `QiyunAssetOps` 是否启用、上次运行结果；确认安装脚本以管理员运行。
