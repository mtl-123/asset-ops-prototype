from pathlib import Path
path = Path("/mnt/g/asset-ops-prototype/asset-ops-prototype/agent-ui.js")
text = path.read_text(encoding="utf-8")
old = "      <p class=\"small muted\">安装会检测并安装缺失的 <code>util-linux</code>/<code>curl</code>；用 <code>--check-deps</code> 只检查、<code>--install-deps</code> 显式安装。首次上报后本页自动出现节点。</p>`;"
new = "      <p class=\"small muted\">安装会检测必需依赖 <code>curl</code>；<code>last</code>/<code>lastb</code>/<code>lastlog</code> 是可选审计命令，缺失不会阻断安装。Ubuntu 26+/Debian 13 用 <code>wtmpdb</code> 提供 <code>last</code>、<code>lastlog2</code> 提供 <code>lastlog2</code>；<code>lastb</code> 可能已不存在。用 <code>--check-deps</code> 查看必需/可选状态。</p>`;"
if old in text:
    text = text.replace(old, new, 1)
path.write_text(text, encoding="utf-8")
print("optional deps note patched", len(text))