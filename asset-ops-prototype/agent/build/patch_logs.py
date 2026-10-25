from pathlib import Path
path = Path("/mnt/g/asset-ops-prototype/asset-ops-prototype/agent-ui.js")
text = path.read_text(encoding="utf-8")
fn = r'''
  function logsMarkup(logs) {
    const rows = (Array.isArray(logs) ? logs : []).map(item => [
      `<span class="mono">${esc(item.path)}</span>`,
      item.exists ? A.badge('存在', 'green') : A.badge('缺失', 'red'),
      item.readable ? A.badge('可读', 'green') : A.badge('不可读', 'amber'),
      bytes(item.size_bytes),
      item.modified_at ? dateTime(item.modified_at) : '—',
      esc(item.error || '—')
    ]);
    return `${rows.length ? A.table(['路径', '存在', '可读', '大小', '最后修改', '错误'], rows) : '<p class="small muted">日志采集未启用或未配置路径。</p>'}
      <p class="small muted">这里检查日志路径/权限/轮转状态；全文采集建议使用 Promtail → Loki，当前 Agent 不把原始日志写入平台 SQLite。</p>`;
  }

'''
if 'function logsMarkup' not in text:
    text = text.replace('  function detailMarkup(data) {', fn + '  function detailMarkup(data) {', 1)
old = "      ${section('历史登录与失败尝试', auditMarkup(users))}"
new = old + "\n      ${section('日志路径', logsMarkup(payload.logs))}"
if old in text:
    text = text.replace(old, new, 1)
path.write_text(text, encoding="utf-8")
print("logs patched", len(text))