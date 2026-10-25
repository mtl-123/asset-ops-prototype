from pathlib import Path
path = Path("/mnt/g/asset-ops-prototype/asset-ops-prototype/agent-ui.js")
text = path.read_text(encoding="utf-8")
start = text.index("  async function showOnboard() {")
end = text.index("  async function openDetail(", start)
new_fn = r'''  async function showOnboard() {
    let info = null;
    let error = '';
    try {
      if (!window.QiyunAPI?.agentEnrollment) throw new Error('Agent 接入接口未就绪。');
      info = await window.QiyunAPI.agentEnrollment();
    } catch (failure) {
      error = failure.message || '无法读取接入信息。';
    }
    const server = ((info && info.server_url) || (window.location?.origin || 'http://127.0.0.1:8765')).replace(/\/+$/, '');
    const token = (info && info.token) || '<从服务端 data/agent.token 复制>';
    const downloadURL = info && info.download_url ? server + info.download_url : '';
    const localOnly = !!(info && info.local_only);
    const downloadStep = downloadURL
      ? `curl -fL '${downloadURL}' -o /tmp/qiyun-agent.zip
unzip -o /tmp/qiyun-agent.zip -d /usr/local/bin
chmod +x /usr/local/bin/qiyun-agent`
      : `# 把 agent/qiyun-agent 复制到目标主机
install -m 0755 qiyun-agent /usr/local/bin/qiyun-agent`;
    const installCommand = `${downloadStep}
sudo /usr/local/bin/qiyun-agent --install \\
  --server '${server}' \\
  --token '${token}' \\
  --interval 60 \\
  --stale-days 90 \\
  --network-interfaces 'eno1'`;
    const testCommand = `sudo /usr/local/bin/qiyun-agent --once --print
sudo systemctl is-enabled qiyun-agent
sudo systemctl is-active qiyun-agent
sudo journalctl -u qiyun-agent -n 50 --no-pager`;
    lastOnboardCommand = installCommand;
    const tokenLine = info && info.token
      ? `令牌：<code class="mono">${esc(info.token)}</code>`
      : `令牌：服务端 <code>data/agent.token</code>（当前登录不是管理员，无法在页面中读取）`;
    const warning = localOnly
      ? `<div class="notice amber" style="margin-top:12px">当前接入地址是 <strong>${esc(server)}</strong>，PVE 主机无法通过 127.0.0.1 访问它。请把 Go 服务部署到 PVE 可达的地址并配置 HTTPS，再把命令里的 server 换成实际地址。</div>`
      : '';
    const body = `<div class="notice">Agent 运行在 Linux / PVE 宿主机上，只读采集并上报；安装脚本会写配置、检查依赖并注册 systemd 开机自启。</div>
      ${error ? `<div class="notice amber" style="margin-top:10px">${esc(error)}</div>` : ''}
      <ol class="small" style="line-height:1.9;padding-left:20px">
        <li><strong>下载 Agent</strong>${downloadURL ? `：<a class="mono" href="${esc(downloadURL)}" download>qiyun-agent-linux-amd64.zip</a>` : '：使用项目中的 <code>agent/qiyun-agent</code>'}</li>
        <li><strong>确认物理网卡</strong>：<code>ip -br link</code>，把下面命令里的 <code>eno1</code> 换成实际上联口；不指定则自动选物理上联。不要选 <code>lo</code>/<code>veth</code>/<code>tap</code>/<code>vmbr</code>，除非你明确要统计桥接口。</li>
        <li><strong>登录令牌</strong>：${tokenLine}<br><span class="muted">令牌等同于 Agent 写入权限，勿发群聊/截图；安装后写入 0600 的 <code>/etc/qiyun-agent.toml</code>。</span></li>
        <li><strong>在 PVE 的 root shell 执行一条龙安装</strong>：
          <pre class="mono small" style="white-space:pre-wrap;overflow-wrap:anywhere;background:var(--canvas);padding:12px;border-radius:8px">${esc(installCommand)}</pre>
        </li>
        <li><strong>验证</strong>：
          <pre class="mono small" style="white-space:pre-wrap;overflow-wrap:anywhere;background:var(--canvas);padding:12px;border-radius:8px">${esc(testCommand)}</pre>
        </li>
        <li><strong>后续修改</strong>：编辑 <code>/etc/qiyun-agent.toml</code>（指标/审计/日志/网卡/interval），再 <code>sudo systemctl restart qiyun-agent</code>。已有配置默认保留；需要覆盖时加 <code>--force-config</code>。</li>
      </ol>
      ${warning}
      <p class="small muted">安装会检测并安装缺失的 <code>util-linux</code>/<code>curl</code>；用 <code>--check-deps</code> 只检查、<code>--install-deps</code> 显式安装。首次上报后本页自动出现节点。</p>`;
    A.modal('接入 Linux / PVE Agent', body, A.button('复制安装命令', 'agent-copy-command', true) + A.button('关闭', 'close-modal'));
  }

'''
text = text[:start] + new_fn + text[end:]
path.write_text(text, encoding="utf-8")
print("onboard patched", len(text))