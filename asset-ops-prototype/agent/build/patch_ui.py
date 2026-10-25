from pathlib import Path
path = Path("/mnt/g/asset-ops-prototype/asset-ops-prototype/agent-ui.js")
text = path.read_text(encoding="utf-8")
metrics = r'''
  function metricsMarkup(metrics) {
    if (!metrics) return '<div class="notice">当前 Agent 未上报详细指标（metrics 关闭或旧版本）。</div>';
    const cpu = metrics.cpu || null;
    const memory = metrics.memory || null;
    const swap = metrics.swap || null;
    const proc = metrics.processes || null;
    const tcp = metrics.tcp || null;
    const p = value => value == null ? '—' : num(value).toFixed(2) + '%';
    const cpuParts = cpu
      ? [['user', cpu.user_percent], ['system', cpu.system_percent], ['iowait', cpu.iowait_percent], ['steal', cpu.steal_percent], ['idle', cpu.idle_percent]]
        .map(([label, value]) => `${label} ${p(value)}`).join(' · ')
      : '首次采样，等待下一周期';
    const diskRows = (metrics.disks || []).map(item => [
      `<span class="mono">${esc(item.device)}</span>`,
      item.read_iops == null ? '—' : num(item.read_iops).toFixed(1),
      item.write_iops == null ? '—' : num(item.write_iops).toFixed(1),
      item.read_bytes_per_second == null ? '—' : bytes(item.read_bytes_per_second) + '/s',
      item.write_bytes_per_second == null ? '—' : bytes(item.write_bytes_per_second) + '/s',
      item.await_ms == null ? '—' : num(item.await_ms).toFixed(2) + ' ms',
      item.aqu_sz == null ? '—' : num(item.aqu_sz).toFixed(2),
      item.util_percent == null ? '—' : p(item.util_percent)
    ]);
    const inodeRows = (metrics.inodes || []).map(item => [
      `<span class="mono">${esc(item.mount)}</span>`, esc(item.filesystem || '—'),
      num(item.total), num(item.used), p(item.usage_percent)
    ]);
    const tcpRow = tcp ? [[
      num(tcp.established), num(tcp.time_wait), num(tcp.close_wait), num(tcp.listen),
      num(tcp.syn_sent), num(tcp.fin_wait1), num(tcp.fin_wait2), num(tcp.last_ack), num(tcp.total)
    ]] : [];
    return `<div class="agent-detail-stats">
        ${A.metric('CPU 细分', cpu ? p(num(cpu.user_percent) + num(cpu.system_percent) + num(cpu.iowait_percent) + num(cpu.steal_percent)) : '—', cpuParts, 'blue')}
        ${A.metric('上下文切换', metrics.context_switches_per_second == null ? '—' : num(metrics.context_switches_per_second).toFixed(0) + '/s', '内核调度频率', 'green')}
        ${A.metric('内存可用', memory ? bytes(memory.available_bytes) : '—', memory ? `${p(memory.usage_percent)} 已用 · cache ${bytes(memory.cached_bytes)}` : '—', 'green')}
        ${A.metric('Swap', swap ? `${bytes(swap.used_bytes)} / ${bytes(swap.total_bytes)}` : '—', swap ? p(swap.usage_percent) : '—', swap && num(swap.used_bytes) ? 'amber' : 'green')}
      </div>
      ${diskRows.length ? A.table(['设备', '读 IOPS', '写 IOPS', '读吞吐', '写吞吐', 'await', 'aqu-sz', 'util'], diskRows) : '<p class="small muted">没有磁盘 I/O 数据。</p>'}
      ${inodeRows.length ? A.table(['挂载点', '文件系统', 'Inode 总量', '已用', '使用率'], inodeRows) : ''}
      ${tcp ? A.table(['ESTABLISHED', 'TIME_WAIT', 'CLOSE_WAIT', 'LISTEN', 'SYN_SENT', 'FIN_WAIT1', 'FIN_WAIT2', 'LAST_ACK', '总计'], tcpRow) : ''}
      ${proc ? `<p class="small muted">进程 ${num(proc.total)} · 线程 ${num(proc.threads)} · 僵尸 ${num(proc.zombies)} · FD ${num(proc.fd_allocated)}/${num(proc.fd_max)}（${p(proc.fd_usage_percent)}）</p>` : ''}
      <p class="small muted">速率按实际采样间隔计算；首次上报没有上一周期，速率为 —。</p>`;
  }

  function auditMarkup(users) {
    const history = (Array.isArray(users.history) ? users.history : []).slice(0, 50);
    const failed = (Array.isArray(users.failed_logins) ? users.failed_logins : []).slice(0, 50);
    const historyTable = history.length
      ? A.table(['用户', '终端', '来源', '开始', '结束', '时长'], history.map(item => [
        esc(item.user), esc(item.tty || '—'), esc(item.host || '—'),
        esc(item.started || '—'), esc(item.ended || '—'), esc(item.duration || '—')
      ]))
      : `<p class="small muted">${users.history_available ? '没有历史登录记录。' : 'last 不可用：' + esc(users.history_error || '未安装 util-linux 或 /var/log/wtmp 不可读')}</p>`;
    const failedTable = failed.length
      ? A.table(['用户', '终端', '来源', '失败时间'], failed.map(item => [
        esc(item.user), esc(item.tty || '—'), esc(item.host || '—'), esc(item.time || '—')
      ]))
      : `<p class="small muted">${users.failed_available ? '没有失败登录记录。' : 'lastb 不可用：' + esc(users.failed_error || '需要 root 和 /var/log/btmp')}</p>`;
    return `<h4 class="agent-subtitle">历史登录（wtmp）</h4>${historyTable}<h4 class="agent-subtitle">失败登录（btmp）</h4>${failedTable}`;
  }

'''
if 'function metricsMarkup' not in text:
    text = text.replace('  function detailMarkup(data) {', metrics + '  function detailMarkup(data) {', 1)
old = "      ${section('网络', networkMarkup(network))}\n      ${section('登录用户', userMarkup(users))}"
new = "      ${section('系统指标', metricsMarkup(host.metrics || {}))}\n      ${section('网络', networkMarkup(network))}\n      ${section('登录用户', userMarkup(users))}\n      ${section('历史登录与失败尝试', auditMarkup(users))}"
if old in text:
    text = text.replace(old, new, 1)
path.write_text(text, encoding="utf-8")
print("patched", len(text))