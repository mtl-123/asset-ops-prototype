from pathlib import Path
path = Path("/mnt/g/asset-ops-prototype/asset-ops-prototype/agent-ui.js")
text = path.read_text(encoding="utf-8")
old = "  --stale-days 90 \\\n  --network-interfaces 'eno1'"
new = "  --stale-days 90"
if old in text:
    text = text.replace(old, new, 1)
old_step = "<li><strong>确认物理网卡</strong>：<code>ip -br link</code>，把下面命令里的 <code>eno1</code> 换成实际上联口；不指定则自动选物理上联。不要选 <code>lo</code>/<code>veth</code>/<code>tap</code>/<code>vmbr</code>，除非你明确要统计桥接口。</li>"
new_step = "<li><strong>网卡（可选）</strong>：默认自动选择物理上联（bond/team 优先）。需要手动指定时，用 <code>ip -br link</code> 查看接口，并在安装命令追加 <code>--network-interfaces 'eno1'</code>，或安装后编辑 <code>/etc/qiyun-agent.toml</code> 的 <code>network_interfaces</code>。不要选 <code>lo</code>/<code>veth</code>/<code>tap</code>/<code>vmbr</code>，除非明确要统计桥接口。</li>"
if old_step in text:
    text = text.replace(old_step, new_step, 1)
path.write_text(text, encoding="utf-8")
print("onboard command adjusted", len(text))