(function () {
  const A = window.AssetDemo;
  if (!A || !A.pages) return;
  const esc = A.escape || (value => String(value == null ? '' : value));
  const state = {
    hosts: [],
    loaded: false,
    loading: false,
    error: '',
    auto: true,
    interval: 5000,
    filter: 'all',
    sort: 'freshness',
    query: '',
    page: 1,
    pageSize: 50,
    total: 0,
    pages: 0,
    summary: null,
    generatedAt: '',
    searchTimer: null,
    selected: new Set(),
    lastUpdated: 0,
    nextAt: 0,
    signature: '',
    history: {},
    timer: null
  };
  let lastOnboardCommand = '';
  let editingHostID = '';
  let deletingHostID = '';
  let bulkDeleteIDs = [];
  const PREFS_KEY = 'qiyun.agent.live.v1';
  const AUTO_CHOICES = [5000, 10000, 30000];

  try {
    const prefs = JSON.parse(localStorage.getItem(PREFS_KEY) || 'null');
    if (prefs) {
      if (typeof prefs.auto === 'boolean') state.auto = prefs.auto;
      if (AUTO_CHOICES.includes(Number(prefs.interval))) state.interval = Number(prefs.interval);
    }
  } catch { }

  function savePrefs() {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify({ auto: state.auto, interval: state.interval }));
    } catch { }
  }

  const number = value => Number(value) || 0;
  const clamp = value => Math.max(0, Math.min(100, number(value)));
  const pct = value => number(value).toFixed(1) + '%';
  const num = value => number(value).toLocaleString('zh-CN');
  const bytes = value => {
    const size = number(value);
    if (!size) return '0 B';
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
    let index = 0;
    let current = size;
    while (current >= 1024 && index < units.length - 1) { current /= 1024; index += 1; }
    return current.toFixed(current >= 10 ? 0 : 1) + ' ' + units[index];
  };
  const bps = value => (value == null || Number.isNaN(Number(value))) ? '—' : bytes(value) + '/s';
  const uptime = seconds => {
    const total = number(seconds);
    if (!total) return '—';
    const days = Math.floor(total / 86400);
    const hours = Math.floor((total % 86400) / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    if (days) return `${days} 天 ${hours} 小时`;
    if (hours) return `${hours} 小时 ${minutes} 分`;
    return `${minutes} 分`;
  };
  const parseTime = value => {
    const ms = Date.parse(value || '');
    return Number.isFinite(ms) ? ms : 0;
  };
  const dateTime = value => {
    const ms = parseTime(value);
    if (!ms) return '—';
    return new Date(ms).toLocaleString('zh-CN', { hour12: false });
  };
  const clock = ms => ms ? new Date(ms).toLocaleTimeString('zh-CN', { hour12: false }) : '—';
  const relative = ms => {
    if (!ms) return '—';
    const seconds = Math.max(0, Math.floor((Date.now() - ms) / 1000));
    if (seconds < 5) return '刚刚';
    if (seconds < 60) return `${seconds} 秒前`;
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return `${minutes} 分钟前`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `${hours} 小时前`;
    return `${Math.floor(hours / 24)} 天前`;
  };

  function freshness(host) {
    const source = host.received_at || host.collected_at;
    const ms = parseTime(source);
    if (!ms) return { key: 'pending', label: '待上报', tone: 'neutral', age: '未上报' };
    if (ms > Date.now() + 60000) return { key: 'unknown', label: '时间异常', tone: 'amber', age: '未来时间' };
    const seconds = Math.max(0, Math.floor((Date.now() - ms) / 1000));
    if (seconds <= 120) return { key: 'fresh', label: '数据新鲜', tone: 'green', age: relative(ms) };
    if (seconds <= 600) return { key: 'delayed', label: '数据延迟', tone: 'amber', age: relative(ms) };
    return { key: 'stale', label: '数据过期', tone: 'red', age: relative(ms) };
  }

  function tone(value, kind) {
    const v = number(value);
    const limits = kind === 'disk' ? [80, 90] : [70, 85];
    if (v >= limits[1]) return 'red';
    if (v >= limits[0]) return 'amber';
    return 'green';
  }

  function barRow(label, value, detail, kind) {
    const t = tone(value, kind);
    return `<div class="agent-bar-row">
      <div class="agent-bar-head"><span>${esc(label)}</span><strong class="text-${t}">${pct(value)}</strong></div>
      <div class="agent-bar-track"><span class="agent-bar-fill ${t}" style="width:${clamp(value)}%"></span></div>
      <div class="agent-bar-foot">${detail || ''}</div>
    </div>`;
  }

  function spark(values, kind) {
    const series = (Array.isArray(values) ? values : []).filter(value => Number.isFinite(Number(value)));
    if (series.length < 2) return `<span class="agent-spark-empty">等待更多采样</span>`;
    const width = 160, height = 38, pad = 3;
    const points = series.map((value, index) => {
      const x = pad + (index / (series.length - 1)) * (width - pad * 2);
      const y = height - pad - (clamp(value) / 100) * (height - pad * 2);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    });
    const area = `${pad},${height - pad} ${points.join(' ')} ${width - pad},${height - pad}`;
    return `<svg class="agent-spark ${kind || tone(series[series.length - 1])}" viewBox="0 0 ${width} ${height}" role="img" aria-label="会话内使用率趋势">
      <polygon points="${area}" fill="currentColor" opacity=".12"></polygon>
      <polyline points="${points.join(' ')}" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" stroke-linecap="round"></polyline>
    </svg>`;
  }

  function platformBadges(host) {
    const badges = [];
    if (host.pve_available) badges.push(A.badge(`PVE · ${number(host.pve_vms)} VM / ${number(host.pve_containers)} CT`, 'green'));
    if (host.docker_available) badges.push(A.badge(`Docker · ${number(host.docker_running)}/${number(host.docker_total)}`, 'blue'));
    if (host.k8s_available) badges.push(A.badge(`K8s · ${number(host.k8s_nodes)} 节点 / ${number(host.k8s_pods)} Pod`, 'purple'));
    if (!badges.length) badges.push(A.badge('仅宿主机', 'neutral'));
    return badges.join(' ');
  }

  function platformText(host) {
    const items = [];
    if (host.pve_available) items.push('PVE');
    if (host.docker_available) items.push('Docker');
    if (host.k8s_available) items.push('Kubernetes');
    return items.length ? items.join(' · ') : 'Linux 宿主机';
  }

  function localSummary() {
    const hosts = state.hosts;
    const count = hosts.length;
    const average = key => count ? hosts.reduce((sum, host) => sum + number(host[key]), 0) / count : 0;
    const maximum = key => count ? Math.max(...hosts.map(host => number(host[key]))) : 0;
    const sum = key => hosts.reduce((total, host) => total + number(host[key]), 0);
    return {
      local: true,
      hosts: state.total || count,
      fresh: hosts.filter(host => freshness(host).key === 'fresh').length,
      delayed: hosts.filter(host => freshness(host).key === 'delayed').length,
      stale: hosts.filter(host => ['stale', 'unknown'].includes(freshness(host).key)).length,
      cpu_avg: average('cpu_percent'),
      memory_avg: average('memory_percent'),
      disk_max: maximum('disk_percent'),
      pve_hosts: hosts.filter(host => host.pve_available).length,
      pve_vms: sum('pve_vms'),
      pve_containers: sum('pve_containers'),
      docker_hosts: hosts.filter(host => host.docker_available).length,
      docker_running: sum('docker_running'),
      docker_total: sum('docker_total'),
      k8s_hosts: hosts.filter(host => host.k8s_available).length,
      k8s_nodes: sum('k8s_nodes'),
      k8s_pods: sum('k8s_pods'),
      users_online: sum('users_online'),
      users_stale: sum('users_stale')
    };
  }

  function fleetSummary() {
    if (state.summary && typeof state.summary === 'object' && Object.prototype.hasOwnProperty.call(state.summary, 'hosts')) {
      return { ...state.summary, local: false };
    }
    return localSummary();
  }

  function recordHistory(hosts) {
    const now = Date.now();
    for (const host of hosts) {
      const id = host.host_id;
      const list = state.history[id] || [];
      const last = list[list.length - 1];
      const stamp = host.received_at || host.collected_at;
      if (!last || last.stamp !== stamp) {
        list.push({
          stamp,
          at: now,
          cpu: number(host.cpu_percent),
          memory: number(host.memory_percent),
          disk: number(host.disk_percent)
        });
      }
      state.history[id] = list.slice(-60);
    }
  }

  function heroMarkup() {
    return `<div class="agent-hero">
      <div>
        <div class="agent-eyebrow">INFRASTRUCTURE AGENTS · LIVE INVENTORY</div>
        <h2>Agent 采集</h2>
        <p>Linux / PVE 主机主动上报的只读清单。页面每 <strong>${state.interval / 1000} 秒</strong> 拉取最新一次上报；报告时间与数据新鲜度分开显示，不把旧数据伪装成实时值。</p>
      </div>
      <div class="agent-hero-rules">
        <span><i class="agent-dot green"></i> ≤ 2 分钟：新鲜</span>
        <span><i class="agent-dot amber"></i> 2–10 分钟：延迟</span>
        <span><i class="agent-dot red"></i> &gt; 10 分钟：过期</span>
      </div>
    </div>`;
  }

  function liveToolbar() {
    const intervals = AUTO_CHOICES.map(seconds => `<button class="agent-chip ${state.interval === seconds ? 'active' : ''}" data-action="agent-interval" data-seconds="${seconds}" aria-pressed="${state.interval === seconds}">${seconds / 1000}s</button>`).join('');
    return `<div class="agent-toolbar">
      <div class="agent-live">
        <span class="agent-pulse ${state.auto ? '' : 'paused'}"></span>
        <strong>${state.auto ? '实时刷新中' : '自动刷新已暂停'}</strong>
        <span id="agent-last">最后更新：${state.lastUpdated ? clock(state.lastUpdated) : '—'}</span>
        <span class="agent-countdown">下次：<b id="agent-next">${state.auto ? '—' : '已暂停'}</b></span>
        <span id="agent-loading" class="small muted">${state.loading ? '正在读取…' : ''}</span>
      </div>
      <div class="agent-controls">
        <button class="agent-chip ${state.auto ? 'active' : ''}" data-action="agent-auto" aria-pressed="${state.auto}">${state.auto ? '暂停刷新' : '开启刷新'}</button>
        ${intervals}
        <button class="agent-chip" data-action="agent-refresh">立即刷新</button>
        <button class="agent-chip agent-chip-primary" data-action="agent-onboard">＋ 接入 Agent</button>
      </div>
    </div>`;
  }

  function kpis() {
    const s = fleetSummary();
    const scope = s.local ? '当前页' : '当前筛选';
    const cards = [
      ['节点总数', num(s.hosts), `${scope} · ${num(s.fresh)} 新鲜 / ${num(s.delayed)} 延迟 / ${num(s.stale)} 过期或待上报`, s.stale ? 'amber' : 'green'],
      ['平均 CPU', pct(s.cpu_avg), '节点百分比算术平均，未按核数加权', tone(s.cpu_avg)],
      ['平均内存', pct(s.memory_avg), '节点百分比算术平均，未按容量加权', tone(s.memory_avg)],
      ['最高磁盘', pct(s.disk_max), '各节点最高挂载点取最大值', tone(s.disk_max, 'disk')],
      ['PVE 资源', `${num(s.pve_vms)} / ${num(s.pve_containers)}`, `${num(s.pve_hosts)} 个 PVE 节点 · VM / CT`, 'blue'],
      ['Docker 容器', `${num(s.docker_running)} / ${num(s.docker_total)}`, `${num(s.docker_hosts)} 个 Docker 节点 · 运行 / 总数`, 'blue'],
      ['Kubernetes', `${num(s.k8s_nodes)} / ${num(s.k8s_pods)}`, `${num(s.k8s_hosts)} 个 K8s 节点 · 节点 / Pod`, 'purple'],
      ['登录用户', num(s.users_online), `${num(s.users_stale)} 个长期未登录`, s.users_stale ? 'amber' : 'green']
    ];
    return `<div class="agent-kpis">${cards.map(([label, value, foot, kind]) => `<div class="agent-kpi agent-kpi-${kind}">
      <span>${esc(label)}</span><strong>${value}</strong><small>${esc(foot)}</small>
    </div>`).join('')}</div>`;
  }

  function resourcesPanel() {
    const s = fleetSummary();
    return `<section class="agent-panel">
      <header><div><h3>全局资源水位</h3><small>全量筛选结果，不是当前页样本</small></div><span class="agent-panel-tag">${num(s.hosts)} 台</span></header>
      <div class="agent-panel-body">
        ${barRow('CPU 平均', s.cpu_avg, '节点 CPU 使用率平均；未按核心数加权', 'cpu')}
        ${barRow('内存平均', s.memory_avg, '节点内存使用率平均；未按容量加权', 'memory')}
        ${barRow('磁盘最高', s.disk_max, '各节点最高挂载点使用率的最大值', 'disk')}
        <div class="agent-caveat">采集口径：Agent 每次上报一个瞬时点；CPU 为采样窗口使用率，内存为 used/total，磁盘按挂载点上报。聚合值用于横向巡检，不能当成精确容量规划数据。</div>
      </div>
    </section>`;
  }

  function freshnessPanel() {
    const s = fleetSummary();
    const total = Math.max(1, s.hosts);
    const segments = [
      ['fresh', '新鲜', s.fresh, 'green'],
      ['delayed', '延迟', s.delayed, 'amber'],
      ['stale', '过期/待上报', s.stale, 'red']
    ];
    return `<section class="agent-panel">
      <header><div><h3>数据新鲜度</h3><small>按服务端接收时间计算，避免主机时钟漂移误导</small></div></header>
      <div class="agent-panel-body">
        <div class="agent-freshness-bar">${segments.map(([, , count, kind]) => count ? `<span class="agent-segment ${kind}" style="width:${(count / total) * 100}%"></span>` : '').join('')}</div>
        <div class="agent-legend">${segments.map(([, label, count, kind]) => `<span><i class="agent-dot ${kind}"></i>${label}<b>${num(count)}</b><small>${Math.round(count / total * 100)}%</small></span>`).join('')}</div>
        <div class="agent-caveat">≤2 分钟新鲜，2–10 分钟延迟，超过 10 分钟或时间异常为过期。Agent 默认每 60 秒上报；网络中断时旧数据保留并标黄/标红，不补零。</div>
      </div>
    </section>`;
  }

  function platformsPanel() {
    const s = fleetSummary();
    const rows = [
      ['Proxmox VE', s.pve_hosts, `${num(s.pve_vms)} VM · ${num(s.pve_containers)} CT`, 'green'],
      ['Docker', s.docker_hosts, `${num(s.docker_running)} 运行 / ${num(s.docker_total)} 总容器`, 'blue'],
      ['Kubernetes', s.k8s_hosts, `${num(s.k8s_nodes)} 节点 · ${num(s.k8s_pods)} Pod`, 'purple'],
      ['登录用户', s.users_online, `${num(s.users_stale)} 个长期未登录`, s.users_stale ? 'amber' : 'green']
    ];
    return `<section class="agent-panel">
      <header><div><h3>平台覆盖</h3><small>只显示 Agent 实际探测到的平台</small></div></header>
      <div class="agent-panel-body agent-platform-table">${rows.map(([label, value, foot, kind]) => `<div class="agent-platform-row">
        <span class="agent-platform-name"><i class="agent-dot ${kind}"></i>${esc(label)}</span>
        <strong>${num(value)}</strong><small>${esc(foot)}</small>
      </div>`).join('')}</div>
    </section>`;
  }

  function compactPlatform(host) {
    const tags = [];
    if (host.pve_available) tags.push(`<span class="agent-mini-tag green">PVE ${num(host.pve_vms)}/${num(host.pve_containers)}</span>`);
    if (host.docker_available) tags.push(`<span class="agent-mini-tag blue">Docker ${num(host.docker_running)}/${num(host.docker_total)}</span>`);
    if (host.k8s_available) tags.push(`<span class="agent-mini-tag purple">K8s ${num(host.k8s_nodes)}/${num(host.k8s_pods)}</span>`);
    return tags.length ? tags.join(' ') : '<span class="small muted">宿主机</span>';
  }

  function miniBar(value, kind) {
    return `<div class="agent-mini-bar"><span class="agent-bar-fill ${tone(value, kind)}" style="width:${clamp(value)}%"></span></div>`;
  }

  function sortHeader(label, key) {
    const active = state.sort === key;
    return `<th scope="col"><button class="agent-th-sort ${active ? 'active' : ''}" data-action="agent-sort" data-sort="${key}" aria-label="按${label}排序">${label}${active ? ' ▾' : ''}</button></th>`;
  }

  function fleetTable() {
    if (!state.hosts.length) {
      return `<div class="agent-empty-filter"><strong>没有符合当前筛选条件的节点</strong><span>调整搜索、状态或平台筛选；也可以清空条件查看全部 Agent。</span><button class="agent-chip" data-action="agent-clear-filter">清空筛选</button></div>`;
    }
    const allSelected = state.hosts.length > 0 && state.hosts.every(host => state.selected.has(host.host_id));
    const rows = state.hosts.map(host => {
      const fresh = freshness(host);
      const title = host.display_name || host.hostname;
      const tags = Array.isArray(host.tags) && host.tags.length ? `<span class="agent-tag-line">${host.tags.map(tag => `<i>${esc(tag)}</i>`).join('')}</span>` : '';
      return `<tr>
        <td class="agent-select-col"><input type="checkbox" data-agent-id="${esc(host.host_id)}" aria-label="选择 ${esc(title)}" ${state.selected.has(host.host_id) ? 'checked' : ''}></td>
        <td><div class="agent-host-cell"><strong>${esc(title)}</strong><small class="mono">${host.display_name ? esc(host.hostname) + ' · ' : ''}${esc(host.host_id)}</small>${tags}</div></td>
        <td><div class="agent-status-cell"><i class="agent-dot ${fresh.tone}"></i><span>${esc(fresh.label)}</span><small>${esc(fresh.age)}</small></div></td>
        <td><div class="agent-os-cell">${esc(host.os_name || '未知系统')}<small>${esc(host.virtualization || 'physical')}${host.hypervisor_detail ? ' · ' + esc(host.hypervisor_detail) : ''}</small></div></td>
        <td><div class="agent-meter-cell"><span class="text-${tone(host.cpu_percent)}">${pct(host.cpu_percent)}</span>${miniBar(host.cpu_percent, 'cpu')}</div></td>
        <td><div class="agent-meter-cell"><span class="text-${tone(host.memory_percent)}">${pct(host.memory_percent)}</span>${miniBar(host.memory_percent, 'memory')}</div></td>
        <td><div class="agent-meter-cell"><span class="text-${tone(host.disk_percent, 'disk')}">${pct(host.disk_percent)}</span>${miniBar(host.disk_percent, 'disk')}</div></td>
        <td><div class="agent-platform-cell">${compactPlatform(host)}</div></td>
        <td><span class="agent-user-cell">${num(host.users_online)} 在线${number(host.users_stale) ? `<small>${num(host.users_stale)} 长期未登录</small>` : ''}</span></td>
        <td><div class="agent-time-cell"><span>${esc(fresh.age)}</span><small class="mono">${clock(parseTime(host.received_at || host.collected_at))}</small></div></td>
        <td><button class="op-btn op-btn-small" data-action="agent-detail" data-host-id="${esc(host.host_id)}">详情</button></td>
      </tr>`;
    }).join('');
    return `<div class="agent-table-wrap"><table class="agent-fleet-table">
      <thead><tr>
        <th class="agent-select-col"><input id="agent-select-all" type="checkbox" aria-label="选择当前页全部节点" ${allSelected ? 'checked' : ''}></th>
        ${sortHeader('主机', 'name')}
        <th scope="col">状态</th>
        <th scope="col">系统</th>
        ${sortHeader('CPU', 'cpu')}
        ${sortHeader('内存', 'memory')}
        ${sortHeader('磁盘', 'disk')}
        <th scope="col">平台</th>
        <th scope="col">用户</th>
        ${sortHeader('最后上报', 'age')}
        <th scope="col"></th>
      </tr></thead>
      <tbody>${rows}</tbody>
    </table></div>`;
  }

  function pageNumbers(page, pages) {
    if (pages <= 7) return Array.from({ length: pages }, (_, index) => index + 1);
    const result = [1];
    const start = Math.max(2, page - 1);
    const end = Math.min(pages - 1, page + 1);
    if (start > 2) result.push('…');
    for (let value = start; value <= end; value += 1) result.push(value);
    if (end < pages - 1) result.push('…');
    result.push(pages);
    return result;
  }

  function pagination() {
    const pages = state.pages || Math.max(1, Math.ceil(state.total / state.pageSize));
    const page = Math.min(state.page, pages || 1);
    const numbers = pageNumbers(page, pages);
    const sizes = [25, 50, 100, 200].map(size => `<option value="${size}" ${state.pageSize === size ? 'selected' : ''}>${size} / 页</option>`).join('');
    return `<div class="agent-pagination">
      <span class="small muted">共 <b>${num(state.total || state.hosts.length)}</b> 台 · 第 ${page} / ${pages || 1} 页</span>
      <div class="agent-pagination-controls">
        <select id="agent-page-size" aria-label="每页数量">${sizes}</select>
        <button class="agent-page-btn" data-action="agent-page" data-page="${Math.max(1, page - 1)}" ${page <= 1 ? 'disabled' : ''}>上一页</button>
        ${numbers.map(value => value === '…' ? '<span class="agent-page-ellipsis">…</span>' : `<button class="agent-page-btn ${value === page ? 'active' : ''}" data-action="agent-page" data-page="${value}" ${value === page ? 'aria-current="page"' : ''}>${value}</button>`).join('')}
        <button class="agent-page-btn" data-action="agent-page" data-page="${Math.min(pages || 1, page + 1)}" ${page >= pages ? 'disabled' : ''}>下一页</button>
      </div>
    </div>`;
  }

  function emptyMarkup() {
    return `${heroMarkup()}${liveToolbar()}${A.panel('还没有 Agent 上报',
      `<div class="notice amber">在 PVE 宿主机或 Linux 主机上运行 Agent，首次上报后这里会自动出现节点；页面会按当前刷新周期自动更新。</div>
       <div class="infra-kv" style="margin-top:14px">
         <span>平台地址</span><strong class="mono">${esc(window.location?.origin || 'http://127.0.0.1:8765')}</strong>
         <span>Agent 令牌</span><strong class="mono">服务端 data/agent.token</strong>
         <span>上报接口</span><strong class="mono">POST /api/v1/agent/report</strong>
       </div>
       <p class="small muted" style="margin-top:12px">在 PVE 宿主机的 root shell 中执行（示例）：</p>
       <pre class="mono small" style="white-space:pre-wrap;overflow-wrap:anywhere;background:var(--canvas);padding:12px;border-radius:8px">qiyun-agent --server ${esc(window.location?.origin || 'http://127.0.0.1:8765')} \\
  --token "$(cat /path/to/agent.token)" --interval 60</pre>
       <p class="small muted">常驻运行请参考 agent/deploy/qiyun-agent.service；Agent 只上报清单和连接建议，不保存 SSH/RDP 密码。</p>`,
      A.button('＋ 接入 Agent', 'agent-onboard', true))}`;
  }

  function loadingMarkup() {
    return `${heroMarkup()}${liveToolbar()}<div class="agent-panel"><div class="agent-panel-body"><div class="notice">正在读取 Agent 上报…</div></div></div>`;
  }

  function errorMarkup() {
    return `${heroMarkup()}${liveToolbar()}${A.panel('Agent 上报读取失败',
      `<div class="notice amber">${esc(state.error)}</div>
       <p class="small muted" style="margin-top:10px">确认 Go 服务可用、Agent 令牌正确，并允许浏览器访问 /api/v1/agent/hosts。</p>`,
      A.button('重试', 'agent-refresh', true))}`;
  }

  function listToolbar() {
    const filters = [
      ['all', '全部'], ['fresh', '新鲜'], ['delayed', '延迟'], ['stale', '过期'],
      ['pve', 'PVE'], ['docker', 'Docker'], ['k8s', 'K8s']
    ];
    const sorted = [
      ['freshness', '按新鲜度'], ['name', '按名称'], ['cpu', '按 CPU'],
      ['memory', '按内存'], ['disk', '按磁盘'], ['age', '按上报时间']
    ];
    return `<div class="agent-list-toolbar">
      <div class="agent-filter-row">${filters.map(([key, label]) => `<button class="agent-chip ${state.filter === key ? 'active' : ''}" data-action="agent-filter" data-filter="${key}" aria-pressed="${state.filter === key}">${label}</button>`).join('')}${state.query || state.filter !== 'all' ? '<button class="agent-chip" data-action="agent-clear-filter">清空</button>' : ''}</div>
      <div class="agent-list-tools">
        <input id="agent-search" type="search" value="${esc(state.query)}" placeholder="搜索主机名 / ID / 系统（服务端）" aria-label="搜索 Agent 节点">
        <select id="agent-sort" aria-label="排序">${sorted.map(([value, label]) => `<option value="${value}" ${state.sort === value ? 'selected' : ''}>${label}</option>`).join('')}</select>
        <span class="small muted">${state.loading ? '刷新中…' : '第 ' + state.page + ' 页'}</span>
      </div>
      <div class="agent-crud-actions">
        <button class="agent-chip agent-chip-primary" data-action="agent-create">＋ 登记 Agent</button>
        <button class="agent-chip" data-action="agent-edit-selected" ${state.selected.size === 1 ? '' : 'disabled'}>编辑选中</button>
        <button class="agent-chip agent-chip-danger" data-action="agent-bulk-delete" ${state.selected.size ? '' : 'disabled'}>删除选中${state.selected.size ? ` (${state.selected.size})` : ''}</button>
        <button class="agent-chip" data-action="agent-purge">清理过期</button>
      </div>
    </div>`;
  }

  function pageMarkup() {
    if (state.error && !state.hosts.length) return errorMarkup();
    if (!state.loaded && !state.hosts.length) return loadingMarkup();
    const total = state.total || state.hosts.length;
    const filteredEmpty = !state.hosts.length && (state.query || state.filter !== 'all');
    if (!state.hosts.length && !filteredEmpty && total === 0) return emptyMarkup();
    return `${heroMarkup()}${liveToolbar()}${state.error ? `<div class="notice amber">刷新失败，正在显示上一次成功读取的数据：${esc(state.error)}</div>` : ''}${kpis()}
      <div class="agent-fleet-layout">
        <section class="agent-panel agent-fleet-panel">
          <header><div><h3>节点清单</h3><small>服务端筛选 + 分页；1000 台也只渲染当前页</small></div><span class="agent-panel-tag">${num(total)} 台</span></header>
          <div class="agent-panel-body">
            ${listToolbar()}
            ${fleetTable()}
            ${pagination()}
          </div>
        </section>
        <aside class="agent-insights">${resourcesPanel()}${freshnessPanel()}${platformsPanel()}</aside>
      </div>
      <div class="agent-caveat agent-page-caveat">数据来源：Agent 主动上报的只读快照，服务端按 host_id 只保留最新一份；页面刷新读取最新快照。汇总数字来自服务端对全量筛选结果的聚合，不是当前页样本；详情中的原始 JSON 保留 Agent 上报的完整字段。</div>`;
  }

  function section(title, body, note) {
    return `<section class="agent-detail-section"><h3>${esc(title)}</h3>${body}${note ? `<p class="small muted">${note}</p>` : ''}</section>`;
  }

  function kv(rows) {
    return `<div class="agent-kv">${rows.map(([key, value]) => `<span>${esc(key)}</span><strong>${value == null || value === '' ? '—' : value}</strong>`).join('')}</div>`;
  }

  function boolBadge(value, yes, no) {
    return value ? A.badge(yes, 'green') : A.badge(no, 'neutral');
  }

  function userMarkup(users) {
    const sessions = Array.isArray(users.logged_in) ? users.logged_in : [];
    const stale = Array.isArray(users.stale) ? users.stale : [];
    const never = Array.isArray(users.never_logged_in) ? users.never_logged_in : [];
    return `<div class="agent-detail-stats">
        ${A.metric('当前登录', num(users.logged_in_count), `${num(users.session_count)} 个会话`, number(users.logged_in_count) ? 'green' : 'neutral')}
        ${A.metric('长期未登录', num(users.stale_count), `阈值 ${num(users.stale_days)} 天`, number(users.stale_count) ? 'amber' : 'green')}
        ${A.metric('本地用户', num(users.local_user_count), users.lastlog_available ? 'lastlog 可用' : 'lastlog 不可用', 'blue')}
      </div>
      ${sessions.length ? A.table(['在线用户', '终端', '来源', '登录时间'], sessions.map(item => [esc(item.user), esc(item.tty || '—'), esc(item.host || '—'), esc(item.login_time || '—')])) : '<p class="small muted">当前没有可读的登录会话。</p>'}
      ${stale.length ? A.table(['长期未登录', '最后登录', '未活跃'], stale.map(item => [esc(item.user), esc(item.last_login || '从未'), item.days_inactive == null ? '—' : `${num(item.days_inactive)} 天`])) : ''}
      ${never.length ? `<p class="small muted">从未登录：${never.map(esc).join('、')}</p>` : ''}`;
  }

  function pveGuestRows(items, kind) {
    return (Array.isArray(items) ? items : []).map(guest => [
      `<strong>${esc(guest.name || guest.vmid)}</strong><div class="small muted mono">${esc(guest.vmid)} · ${kind}${Array.isArray(guest.tags) && guest.tags.length ? ' · ' + guest.tags.map(esc).join(', ') : ''}</div>`,
      esc(guest.status || '—'),
      `${esc(guest.os_family || 'unknown')}${guest.ostype ? ' / ' + esc(guest.ostype) : ''}`,
      `${guest.cpu_cores == null ? '—' : esc(guest.cpu_cores)} vCPU<br>${guest.memory_bytes == null ? '—' : bytes(guest.memory_bytes)}`,
      guest.disk_bytes == null ? '—' : bytes(guest.disk_bytes),
      uptime(guest.uptime_seconds),
      esc((Array.isArray(guest.ip_addresses) ? guest.ip_addresses : []).join(', ') || '—'),
      guest.connection ? `<span class="mono">${esc(guest.connection.protocol)}://${esc(guest.connection.host)}:${number(guest.connection.port)}</span>` : '—'
    ]);
  }

  function pveMarkup(pve) {
    if (!pve || !pve.available) {
      return `<div class="notice">未检测到 Proxmox VE（/etc/pve 不存在），或 Agent 没有读取权限。${pve && pve.error ? '<br>' + esc(pve.error) : ''}</div>`;
    }
    const headers = ['名称', '状态', 'OS', '规格', '磁盘', '运行', 'IP', '连接建议'];
    const vms = pveGuestRows(pve.vms, 'qemu');
    const cts = pveGuestRows(pve.containers, 'lxc');
    return `<div class="agent-platform-summary">节点 ${esc(pve.node || '—')} · 版本 ${esc(pve.version || '未知')} · ${number((pve.vms || []).length)} VM / ${number((pve.containers || []).length)} CT</div>
      ${vms.length ? `<h4 class="agent-subtitle">虚拟机（qemu）</h4>${A.table(headers, vms)}` : '<p class="small muted">没有虚拟机。</p>'}
      ${cts.length ? `<h4 class="agent-subtitle">容器（lxc）</h4>${A.table(headers, cts)}` : '<p class="small muted">没有 LXC 容器。</p>'}`;
  }

  function dockerMarkup(docker) {
    if (!docker || !docker.available) {
      return `<div class="notice">未检测到 Docker daemon${docker && docker.error ? '：' + esc(docker.error) : '。'}</div>`;
    }
    const rows = (Array.isArray(docker.containers) ? docker.containers : []).map(item => [
      `<strong>${esc(item.name || item.id)}</strong><div class="small muted mono">${esc(String(item.id || '').slice(0, 12))}</div>`,
      esc(item.image || '—'),
      esc(item.state || '—'),
      esc(item.status || '—'),
      item.cpu_percent == null ? '—' : pct(item.cpu_percent),
      esc(item.memory_usage || '—') + (item.memory_percent == null ? '' : `<div class="small muted">${pct(item.memory_percent)}</div>`),
      esc(item.ports || '—')
    ]);
    return `<div class="agent-platform-summary">版本 ${esc(docker.version || '—')} · 镜像 ${docker.images == null ? '—' : num(docker.images)} · ${number(docker.containers_running)}/${number(docker.containers_total)} 运行</div>
      ${rows.length ? A.table(['容器', '镜像', '状态', '运行状态', 'CPU', '内存', '端口'], rows) : '<p class="small muted">没有容器。</p>'}`;
  }

  function k8sMarkup(k8s) {
    if (!k8s || !k8s.available) {
      return `<div class="notice">未检测到 Kubernetes 集群${k8s && k8s.error ? '：' + esc(k8s.error) : '。'}</div>`;
    }
    const nodes = (Array.isArray(k8s.nodes) ? k8s.nodes : []).map(node => [
      esc(node.name), node.ready ? A.badge('Ready', 'green') : A.badge('NotReady', 'red'),
      esc((Array.isArray(node.roles) ? node.roles : []).join(', ') || '—'), esc(node.kubelet_version || '—'),
      `${esc(node.os || '—')} / ${esc(node.arch || '—')}`,
      node.cpu_cores == null ? '—' : esc(node.cpu_cores),
      node.memory_bytes == null ? '—' : bytes(node.memory_bytes)
    ]);
    const bad = (Array.isArray(k8s.unhealthy_pods) ? k8s.unhealthy_pods : []).map(pod => [
      esc(pod.namespace), esc(pod.name), esc(pod.phase), esc(pod.node || '—'), num(pod.restarts), esc(pod.reason || '—')
    ]);
    return `<div class="agent-platform-summary">Context ${esc(k8s.context || '—')} · 版本 ${esc(k8s.version || '—')} · ${number(k8s.nodes_ready)}/${number(k8s.node_count)} 节点 Ready · ${number(k8s.pod_count)} Pod（运行 ${number(k8s.pods_running)} / Pending ${number(k8s.pods_pending)} / Failed ${number(k8s.pods_failed)}）</div>
      ${nodes.length ? A.table(['节点', '状态', '角色', 'kubelet', 'OS / 架构', 'CPU', '内存'], nodes) : ''}
      ${bad.length ? `<h4 class="agent-subtitle">异常 Pod</h4>${A.table(['Namespace', 'Pod', 'Phase', '节点', '重启', '原因'], bad)}` : '<p class="small muted">没有异常 Pod。</p>'}`;
  }

  function networkMarkup(network) {
    if (!network) return '<p class="small muted">该 Agent 版本未上报网络字段。</p>';
    const interfaces = Array.isArray(network.interfaces) ? network.interfaces : [];
    const rows = interfaces.map(item => [
      `<span class="mono">${esc(item.name)}</span>`,
      bps(item.received_bps), bps(item.transmitted_bps),
      bytes(item.received_bytes), bytes(item.transmitted_bytes),
      `${num(item.receive_errors)} / ${num(item.transmit_errors)}`,
      `${num(item.receive_dropped)} / ${num(item.transmit_dropped)}`
    ]);
    return `<div class="agent-network-summary">
        <div><span>累计接收</span><strong>${bytes(network.total_received_bytes)}</strong></div>
        <div><span>累计发送</span><strong>${bytes(network.total_transmitted_bytes)}</strong></div>
        <div><span>接收速率</span><strong>${bps(network.total_received_bps)}</strong></div>
        <div><span>发送速率</span><strong>${bps(network.total_transmitted_bps)}</strong></div>
        <div><span>采样间隔</span><strong>${network.sample_interval_seconds == null ? '—' : number(network.sample_interval_seconds).toFixed(1) + ' 秒'}</strong></div>
        <div><span>接口选择</span><strong>${esc(network.selection || '—')}</strong></div>
      </div>
      ${network.error ? `<div class="notice amber">${esc(network.error)}</div>` : ''}
      ${rows.length ? A.table(['接口', '接收速率', '发送速率', '累计接收', '累计发送', '收/发错误', '收/发丢包'], rows) : '<p class="small muted">没有网络接口数据。</p>'}
      <p class="small muted">速率是 Agent 在采样间隔内计算的 bytes/s；累计值是接口自启动以来的计数。虚拟接口（lo/veth/vmbr）会包含在内，不能直接把汇总值当作物理出口带宽。</p>`;
  }


  function metricsMarkup(metrics) {
    if (!metrics) return '<div class="notice">当前 Agent 未上报详细指标（metrics 关闭或旧版本）。</div>';
    const cpu = metrics.cpu || null;
    const memory = metrics.memory || null;
    const swap = metrics.swap || null;
    const proc = metrics.processes || null;
    const tcp = metrics.tcp || null;
    const p = value => value == null ? '—' : number(value).toFixed(2) + '%';
    const cpuParts = cpu
      ? [['user', cpu.user_percent], ['system', cpu.system_percent], ['iowait', cpu.iowait_percent], ['steal', cpu.steal_percent], ['idle', cpu.idle_percent]]
        .map(([label, value]) => `${label} ${p(value)}`).join(' · ')
      : '首次采样，等待下一周期';
    const diskRows = (metrics.disks || []).map(item => [
      `<span class="mono">${esc(item.device)}</span>`,
      item.read_iops == null ? '—' : number(item.read_iops).toFixed(1),
      item.write_iops == null ? '—' : number(item.write_iops).toFixed(1),
      item.read_bytes_per_second == null ? '—' : bytes(item.read_bytes_per_second) + '/s',
      item.write_bytes_per_second == null ? '—' : bytes(item.write_bytes_per_second) + '/s',
      item.await_ms == null ? '—' : number(item.await_ms).toFixed(2) + ' ms',
      item.aqu_sz == null ? '—' : number(item.aqu_sz).toFixed(2),
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
        ${A.metric('CPU 细分', cpu ? p(number(cpu.user_percent) + number(cpu.system_percent) + number(cpu.iowait_percent) + number(cpu.steal_percent)) : '—', cpuParts, 'blue')}
        ${A.metric('上下文切换', metrics.context_switches_per_second == null ? '—' : number(metrics.context_switches_per_second).toFixed(0) + '/s', '内核调度频率', 'green')}
        ${A.metric('内存可用', memory ? bytes(memory.available_bytes) : '—', memory ? `${p(memory.usage_percent)} 已用 · cache ${bytes(memory.cached_bytes)}` : '—', 'green')}
        ${A.metric('Swap', swap ? `${bytes(swap.used_bytes)} / ${bytes(swap.total_bytes)}` : '—', swap ? p(swap.usage_percent) : '—', swap && number(swap.used_bytes) > 0 ? 'amber' : 'green')}
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

  function detailMarkup(data) {
    const summary = data.summary || {};
    const payload = data.payload || {};
    const host = payload.host || {};
    const users = payload.users || {};
    const platforms = payload.platforms || {};
    const cpu = host.cpu || {};
    const memory = host.memory || {};
    const load = host.load || {};
    const network = host.network || {};
    const fresh = freshness({ received_at: summary.received_at || payload.received_at, collected_at: summary.collected_at || payload.collected_at });
    const disks = Array.isArray(host.disks) ? host.disks : [];
    const diskRows = disks.map(disk => [
      `<span class="mono">${esc(disk.mount)}</span>`,
      esc(disk.filesystem || '—'),
      bytes(disk.total_bytes),
      bytes(disk.used_bytes),
      bytes(disk.available_bytes),
      `<span class="text-${tone(disk.usage_percent, 'disk')}">${pct(disk.usage_percent)}</span>`
    ]);
    const boot = host.boot_time ? `${esc(host.boot_time)} · 已运行 ${uptime(host.uptime_seconds)}` : uptime(host.uptime_seconds);
    const displayTitle = summary.display_name || summary.hostname || host.hostname || '—';
    const tagList = Array.isArray(summary.tags) ? summary.tags : [];
    return `<div class="agent-detail-head">
        <div><strong>${esc(displayTitle)}</strong><div class="mono small">${esc(summary.hostname || host.hostname || '—')} · ${esc(summary.host_id || payload.host_id || '—')}</div>${tagList.length ? `<span class="agent-tag-line" style="margin-top:5px">${tagList.map(tag => `<i>${esc(tag)}</i>`).join('')}</span>` : ''}</div>
        <div class="agent-detail-fresh">${A.badge(fresh.label, fresh.tone)}<span class="small muted">${fresh.age}</span></div>
      </div>
      ${fresh.key === 'pending' ? '<div class="notice amber">该节点是登记占位，等待 Agent 首次上报；指标为空是正常的，不会伪造数据。Agent 使用同一 Host ID 上报后会自动填充。</div>' : ''}
      <div class="agent-detail-stats">
        ${A.metric('CPU', pct(cpu.usage_percent), `${number(cpu.logical_cores)} 逻辑核 / ${number(cpu.physical_cores)} 物理核`, tone(cpu.usage_percent))}
        ${A.metric('内存', pct(memory.usage_percent), `${bytes(memory.used_bytes)} / ${bytes(memory.total_bytes)}`, tone(memory.usage_percent))}
        ${A.metric('最高磁盘', pct(summary.disk_percent), `${disks.length} 个挂载点`, tone(summary.disk_percent, 'disk'))}
        ${A.metric('登录用户', num(users.logged_in_count), `${num(users.stale_count)} 长期未登录`, number(users.stale_count) ? 'amber' : 'green')}
      </div>
      ${section('系统与采集', kv([
        ['操作系统', esc(host.os_name || '—') + ' ' + esc(host.os_version || '')],
        ['内核 / 架构', `<span class="mono">${esc(host.kernel_version || '—')} / ${esc(host.arch || '—')}</span>`],
        ['虚拟化', esc(summary.virtualization || (host.virtualization && host.virtualization.is_virtual_machine ? 'vm' : 'physical')) + (summary.hypervisor_detail ? ' · ' + esc(summary.hypervisor_detail) : '')],
        ['启动 / 运行', boot],
        ['Agent 版本', esc(summary.agent_version || payload.agent_version || '—')],
        ['Schema', esc(payload.schema_version == null ? '—' : payload.schema_version)],
        ['采集时间', `<span class="mono">${dateTime(summary.collected_at || payload.collected_at)}</span> <span class="small muted">${relative(parseTime(summary.collected_at || payload.collected_at))}</span>`],
        ['接收时间', `<span class="mono">${dateTime(summary.received_at)}</span> <span class="small muted">${relative(parseTime(summary.received_at))}</span>`]
      ]))}
      ${section('运维元数据', kv([
        ['显示名', esc(summary.display_name || '—')],
        ['标签', tagList.length ? tagList.map(tag => `<span class="agent-mini-tag">${esc(tag)}</span>`).join(' ') : '—'],
        ['备注', esc(summary.note || '—')]
      ]), '显示名、标签和备注是平台侧元数据，不会被 Agent 上报覆盖。')}
      ${section('CPU / 内存 / 负载', `<div class="agent-detail-bars">
        ${barRow('CPU 使用率', cpu.usage_percent, `${esc(cpu.model || '型号未上报')} · ${number(cpu.logical_cores)} 逻辑核 / ${number(cpu.physical_cores)} 物理核`, 'cpu')}
        ${barRow('内存使用率', memory.usage_percent, `${bytes(memory.used_bytes)} 已用 / ${bytes(memory.total_bytes)} 总量 / ${bytes(memory.available_bytes)} 可用`, 'memory')}
      </div>
      <div class="agent-load-row"><div><span>Load 1m</span><strong>${load.one == null ? '—' : number(load.one).toFixed(2)}</strong></div><div><span>Load 5m</span><strong>${load.five == null ? '—' : number(load.five).toFixed(2)}</strong></div><div><span>Load 15m</span><strong>${load.fifteen == null ? '—' : number(load.fifteen).toFixed(2)}</strong></div></div>
      <p class="small muted">CPU 为采样窗口使用率；Load 是 Linux 运行队列/不可中断任务的平均值，需结合逻辑核数判断，不能单独当作 CPU 百分比。</p>`)}
      ${section('磁盘', diskRows.length ? A.table(['挂载点', '文件系统', '总量', '已用', '可用', '使用率'], diskRows) : '<p class="small muted">没有磁盘数据。</p>')}
      ${section('系统指标', metricsMarkup(host.metrics || {}))}
      ${section('网络', networkMarkup(network))}
      ${section('登录用户', userMarkup(users))}
      ${section('历史登录与失败尝试', auditMarkup(users))}
      ${section('日志路径', logsMarkup(payload.logs))}
      ${section('Proxmox VE', pveMarkup(platforms.pve))}
      ${section('Docker', dockerMarkup(platforms.docker))}
      ${section('Kubernetes', k8sMarkup(platforms.kubernetes))}
      <details class="agent-raw"><summary>原始 JSON（Agent 上报的完整字段）</summary><pre>${esc(JSON.stringify(payload, null, 2))}</pre></details>
      <p class="small muted agent-detail-note">服务端只保留每个 host_id 的最新一份上报；本页的“实时”指按刷新周期重新读取最新快照，不是历史数据库。审计与告警阈值不在本页伪造。</p>`;
  }

  function updateChrome() {
    const last = document.getElementById('agent-last');
    if (last) last.textContent = '最后更新：' + (state.lastUpdated ? clock(state.lastUpdated) : '—');
    const next = document.getElementById('agent-next');
    if (next) {
      if (!state.auto) next.textContent = '已暂停';
      else if (!state.nextAt) next.textContent = '—';
      else next.textContent = Math.max(0, Math.ceil((state.nextAt - Date.now()) / 1000)) + 's';
    }
    const loading = document.getElementById('agent-loading');
    if (loading) loading.textContent = state.loading ? '正在读取…' : '';
  }

  function rerender() {
    if (A.state.page !== 'agents') return;
    const active = document.activeElement;
    const activeId = active && active.id;
    const start = active && typeof active.selectionStart === 'number' ? active.selectionStart : null;
    const end = active && typeof active.selectionEnd === 'number' ? active.selectionEnd : null;
    A.render();
    if (activeId) {
      const element = document.getElementById(activeId);
      if (element && element.focus) {
        element.focus({ preventScroll: true });
        if (start != null && element.setSelectionRange) {
          try { element.setSelectionRange(start, end); } catch { }
        }
      }
    }
    updateChrome();
  }

  function queryParams() {
    const params = { page: state.page, page_size: state.pageSize, sort: state.sort };
    if (['fresh', 'delayed', 'stale'].includes(state.filter)) params.status = state.filter;
    else if (['pve', 'docker', 'k8s'].includes(state.filter)) params.platform = state.filter;
    if (state.query.trim()) params.q = state.query.trim();
    return params;
  }

  async function load(force) {
    if (state.loading) return;
    if (!force && !state.auto) return;
    state.loading = true;
    state.error = '';
    if (A.state.page === 'agents' && !state.loaded) rerender();
    try {
      if (!window.QiyunAPI?.agentHosts) throw new Error('Agent 接口未就绪。');
      const result = await window.QiyunAPI.agentHosts(queryParams());
      const items = Array.isArray(result?.items) ? result.items : [];
      const total = Number(result?.total);
      const pageSize = Number(result?.page_size);
      const pages = Number(result?.pages);
      state.hosts = items;
      state.total = Number.isFinite(total) ? total : items.length;
      state.page = Number(result?.page) || state.page;
      state.pageSize = Number.isFinite(pageSize) && pageSize > 0 ? pageSize : state.pageSize;
      state.pages = Number.isFinite(pages) && pages >= 0 ? pages : Math.ceil(state.total / state.pageSize);
      state.summary = result?.summary && typeof result.summary === 'object' ? result.summary : null;
      state.generatedAt = result?.generated_at || '';
      state.loaded = true;
      state.signature = JSON.stringify(items);
      state.lastUpdated = Date.now();
      recordHistory(items);
    } catch (error) {
      state.error = error.message || '无法读取 Agent 上报。';
      state.loaded = false;
    } finally {
      state.loading = false;
      state.nextAt = Date.now() + state.interval;
      if (A.state.page === 'agents') rerender();
    }
  }

  function startTimer() {
    if (state.timer) return;
    state.timer = setInterval(() => {
      updateChrome();
      if (!state.auto || document.hidden || A.state.page !== 'agents' || state.loading) return;
      if (!state.nextAt || Date.now() >= state.nextAt) load(false);
    }, 1000);
  }

  function integrationHelp() {
    return `<details class="notice" style="margin-bottom:12px;padding:12px 14px">
      <summary style="cursor:pointer;font-weight:600">接口与接入说明（点击展开）</summary>
      <div class="small" style="margin-top:10px;line-height:1.9;overflow-wrap:anywhere">
        <div>上报接口：<code>POST /api/v1/agent/report</code>（<code>Authorization: Bearer &lt;token&gt;</code>）</div>
        <div>列表接口：<code>GET /api/v1/agent/hosts</code> · 详情：<code>GET /api/v1/agent/hosts/{host_id}</code></div>
        <div>页面只读取最近一次上报；服务端按 host_id 覆盖保存，不做历史库。</div>
      </div>
    </details>`;
  }

  async function showOnboard() {
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
        <li><strong>网卡（可选）</strong>：默认自动选择物理上联（bond/team 优先）。需要手动指定时，用 <code>ip -br link</code> 查看接口，并在安装命令追加 <code>--network-interfaces 'eno1'</code>，或安装后编辑 <code>/etc/qiyun-agent.toml</code> 的 <code>network_interfaces</code>。不要选 <code>lo</code>/<code>veth</code>/<code>tap</code>/<code>vmbr</code>，除非明确要统计桥接口。</li>
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
      <p class="small muted">安装会检测必需依赖 <code>curl</code>；<code>last</code>/<code>lastb</code>/<code>lastlog</code> 是可选审计命令，缺失不会阻断安装。Ubuntu 26+/Debian 13 用 <code>wtmpdb</code> 提供 <code>last</code>、<code>lastlog2</code> 提供 <code>lastlog2</code>；<code>lastb</code> 可能已不存在。用 <code>--check-deps</code> 查看必需/可选状态。</p>`;
    A.modal('接入 Linux / PVE Agent', body, A.button('复制安装命令', 'agent-copy-command', true) + A.button('关闭', 'close-modal'));
  }

  function tagText(tags) {
    return Array.isArray(tags) ? tags.join(', ') : '';
  }

  function parseTags(value) {
    return String(value || '').split(/[,，\n]+/).map(item => item.trim()).filter(Boolean).slice(0, 20);
  }

  function formField(label, id, value, placeholder) {
    return `<label class="field"><span>${esc(label)}</span><input id="${esc(id)}" value="${esc(value || '')}" placeholder="${esc(placeholder || '')}" autocomplete="off"></label>`;
  }

  function formArea(label, id, value, placeholder) {
    return `<label class="field"><span>${esc(label)}</span><textarea id="${esc(id)}" rows="3" placeholder="${esc(placeholder || '')}">${esc(value || '')}</textarea></label>`;
  }

  function showCreateHost() {
    A.modal('登记 Agent 节点', `
      <div class="notice">登记只创建一条等待上报的节点记录，不会伪造 CPU/内存等指标。Agent 用同一 Host ID 首次上报后会自动填充。</div>
      <div class="form-grid">
        ${formField('Host ID（必填）', 'agent-new-id', '', '例如 5ce7f424c40d47f78d7901a58089f9ac')}
        ${formField('主机名（必填）', 'agent-new-hostname', '', '例如 pve-1')}
        ${formField('显示名（可选）', 'agent-new-display', '', '例如 生产 PVE-01')}
        ${formField('标签（逗号分隔）', 'agent-new-tags', '', '生产,PVE,机房A')}
      </div>
      ${formArea('备注', 'agent-new-note', '', '责任人或接入说明')}
      <div class="notice amber">如果填写的 Host ID 与 Agent 实际上报不一致，会多出一条永远待上报的记录；请从 Agent 安装输出的 host_id 复制。</div>`,
      A.button('保存登记', 'agent-create-save', true) + A.button('取消', 'close-modal'));
  }

  async function showEditHost(hostID) {
    let host = state.hosts.find(item => item.host_id === hostID);
    if (!host) {
      try {
        const data = await window.QiyunAPI.agentHost(hostID);
        host = data.summary || {};
      } catch (error) {
        A.toast('读取节点失败：' + error.message);
        return;
      }
    }
    editingHostID = hostID;
    A.modal(`编辑 Agent 元数据 · ${esc(host.display_name || host.hostname || hostID)}`, `
      <div class="notice">这里只改平台侧元数据，不会改 Agent 上报的 Host ID、主机名和指标。</div>
      <div class="form-grid">
        ${formField('Host ID（只读）', 'agent-edit-id', host.host_id || hostID)}
        ${formField('主机名（只读）', 'agent-edit-hostname', host.hostname || '')}
        ${formField('显示名', 'agent-edit-display', host.display_name || '')}
        ${formField('标签（逗号分隔）', 'agent-edit-tags', tagText(host.tags))}
      </div>
      ${formArea('备注', 'agent-edit-note', host.note || '')}`,
      A.button('保存', 'agent-edit-save', true) + A.button('取消', 'close-modal'));
    const idField = document.getElementById('agent-edit-id');
    const hostField = document.getElementById('agent-edit-hostname');
    if (idField) idField.readOnly = true;
    if (hostField) hostField.readOnly = true;
  }

  function showDeleteHost(hostID, name) {
    deletingHostID = hostID;
    A.modal('删除 Agent 节点', `
      <div class="notice amber">将永久删除 <strong>${esc(name || hostID)}</strong> 的节点记录和最近一次完整上报。删除后如果 Agent 仍在上报，会以同一 Host ID 重新出现。</div>
      <div class="mono small">${esc(hostID)}</div>`,
      A.button('确认删除', 'agent-delete-confirm', true) + A.button('取消', 'close-modal'));
  }

  function showBulkDelete() {
    bulkDeleteIDs = Array.from(state.selected);
    if (!bulkDeleteIDs.length) {
      A.toast('请先选择节点。');
      return;
    }
    A.modal('批量删除 Agent 节点', `
      <div class="notice amber">将永久删除选中的 <strong>${bulkDeleteIDs.length}</strong> 个节点。仍在上报的 Agent 会重新出现。</div>
      <div class="small mono" style="max-height:160px;overflow:auto">${bulkDeleteIDs.slice(0, 80).map(esc).join('<br>')}${bulkDeleteIDs.length > 80 ? '<br>…' : ''}</div>`,
      A.button('确认批量删除', 'agent-bulk-confirm', true) + A.button('取消', 'close-modal'));
  }

  function showPurge() {
    A.modal('清理 Agent 记录', `
      <div class="notice">“过期”按服务端接收时间计算：超过 10 分钟未上报。清理只删除平台记录，不卸载远端 Agent；仍在上报的节点会重新出现。</div>
      <div class="stack">
        ${A.button('清理所有过期节点（>10 分钟）', 'agent-purge-stale', true)}
        <div class="between" style="gap:10px">
          <label class="field" style="flex:1"><span>按天清理：最后上报早于</span><input id="agent-purge-days" type="number" min="1" max="3650" value="30"></label>
          ${A.button('按天清理', 'agent-purge-days')}
        </div>
        <p class="small muted">登记后一直未上报的节点没有接收时间，不会被“按天/过期”清理；请在表格中勾选后用“删除选中”。</p>
      </div>`,
      A.button('关闭', 'close-modal'));
  }

  async function openDetail(hostID) {
    if (!hostID) return;
    try {
      const data = await window.QiyunAPI.agentHost(hostID);
      const title = (data.summary && (data.summary.display_name || data.summary.hostname)) || hostID;
      A.modal(`Agent 节点 · ${esc(title)}`,
        `<div class="agent-detail">${detailMarkup(data)}</div>`,
        A.button('编辑元数据', 'agent-edit', false, `data-host-id="${esc(hostID)}"`) +
        A.button('删除节点', 'agent-delete', false, `data-host-id="${esc(hostID)}"`) +
        A.button('刷新详情', 'agent-detail-refresh', false, `data-host-id="${esc(hostID)}"`) +
        A.button('关闭', 'close-modal'));
    } catch (error) {
      A.toast('读取 Agent 详情失败：' + error.message);
    }
  }

  A.pages.agents = () => {
    if (!state.loaded && !state.loading && !state.error) {
      Promise.resolve().then(() => load(true));
    }
    updateChromeLater();
    return pageMarkup();
  };

  function updateChromeLater() {
    setTimeout(updateChrome, 0);
  }

  A.actions['agent-refresh'] = () => load(true);
  A.actions['agent-auto'] = () => {
    state.auto = !state.auto;
    state.nextAt = state.auto ? Date.now() + state.interval : 0;
    savePrefs();
    rerender();
    if (state.auto) load(true);
  };
  A.actions['agent-interval'] = button => {
    const seconds = Number(button.dataset.seconds) || 5000;
    if (!AUTO_CHOICES.includes(seconds)) return;
    state.interval = seconds;
    state.nextAt = Date.now() + seconds;
    savePrefs();
    rerender();
  };
  A.actions['agent-filter'] = button => {
    state.filter = button.dataset.filter || 'all';
    state.page = 1;
    load(true);
  };
  A.actions['agent-sort'] = button => {
    const sort = button.dataset.sort;
    if (!sort) return;
    state.sort = sort;
    state.page = 1;
    load(true);
  };
  A.actions['agent-page'] = button => {
    const page = Number(button.dataset.page);
    if (!Number.isFinite(page) || page < 1) return;
    state.page = page;
    load(true);
  };
  A.actions['agent-clear-filter'] = () => {
    state.query = '';
    state.filter = 'all';
    state.page = 1;
    if (state.searchTimer) clearTimeout(state.searchTimer);
    load(true);
  };
  A.actions['agent-onboard'] = () => showOnboard();
  A.actions['agent-copy-command'] = async () => {
    if (!lastOnboardCommand) return;
    try {
      await window.navigator.clipboard.writeText(lastOnboardCommand);
      A.toast('接入命令已复制。');
    } catch {
      A.toast('复制失败，请手动选择命令文本。');
    }
  };
  A.actions['agent-create'] = () => showCreateHost();
  A.actions['agent-create-save'] = async () => {
    const hostID = (document.getElementById('agent-new-id')?.value || '').trim();
    const hostname = (document.getElementById('agent-new-hostname')?.value || '').trim();
    if (!/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(hostID)) return A.toast('Host ID 格式不正确。');
    if (!hostname) return A.toast('主机名不能为空。');
    try {
      await window.QiyunAPI.agentCreateHost({
        host_id: hostID,
        hostname,
        display_name: (document.getElementById('agent-new-display')?.value || '').trim(),
        note: (document.getElementById('agent-new-note')?.value || '').trim(),
        tags: parseTags(document.getElementById('agent-new-tags')?.value)
      });
      A.closeModal();
      A.toast('节点已登记，等待 Agent 首次上报。');
      state.filter = 'all';
      state.query = '';
      state.page = 1;
      load(true);
    } catch (error) {
      A.toast('登记失败：' + error.message);
    }
  };
  A.actions['agent-edit'] = button => showEditHost(button.dataset.hostId);
  A.actions['agent-edit-selected'] = () => {
    if (state.selected.size !== 1) return A.toast('请选择一个节点。');
    showEditHost(Array.from(state.selected)[0]);
  };
  A.actions['agent-edit-save'] = async () => {
    if (!editingHostID) return;
    try {
      await window.QiyunAPI.agentUpdateHost(editingHostID, {
        display_name: (document.getElementById('agent-edit-display')?.value || '').trim(),
        note: (document.getElementById('agent-edit-note')?.value || '').trim(),
        tags: parseTags(document.getElementById('agent-edit-tags')?.value)
      });
      A.closeModal();
      A.toast('节点元数据已更新。');
      load(true);
    } catch (error) {
      A.toast('更新失败：' + error.message);
    }
  };
  A.actions['agent-delete'] = button => {
    const host = state.hosts.find(item => item.host_id === button.dataset.hostId);
    showDeleteHost(button.dataset.hostId, host?.display_name || host?.hostname || button.dataset.hostId);
  };
  A.actions['agent-delete-confirm'] = async () => {
    if (!deletingHostID) return;
    try {
      await window.QiyunAPI.agentDeleteHost(deletingHostID);
      state.selected.delete(deletingHostID);
      A.closeModal();
      A.toast('节点已删除。');
      load(true);
    } catch (error) {
      A.toast('删除失败：' + error.message);
    }
  };
  A.actions['agent-bulk-delete'] = () => showBulkDelete();
  A.actions['agent-bulk-confirm'] = async () => {
    if (!bulkDeleteIDs.length) return;
    try {
      const result = await window.QiyunAPI.agentBulkDeleteHosts(bulkDeleteIDs);
      state.selected.clear();
      A.closeModal();
      A.toast(`已删除 ${result.deleted || bulkDeleteIDs.length} 个节点。`);
      load(true);
    } catch (error) {
      A.toast('批量删除失败：' + error.message);
    }
  };
  A.actions['agent-purge'] = () => showPurge();
  A.actions['agent-purge-stale'] = async () => {
    try {
      const result = await window.QiyunAPI.agentPurgeHosts({ only_stale: true });
      state.selected.clear();
      A.closeModal();
      A.toast(`已清理 ${result.deleted || 0} 个过期节点。`);
      load(true);
    } catch (error) {
      A.toast('清理失败：' + error.message);
    }
  };
  A.actions['agent-purge-days'] = async () => {
    const days = Number(document.getElementById('agent-purge-days')?.value);
    if (!Number.isInteger(days) || days < 1 || days > 3650) return A.toast('天数必须在 1 到 3650 之间。');
    try {
      const result = await window.QiyunAPI.agentPurgeHosts({ older_than_days: days });
      state.selected.clear();
      A.closeModal();
      A.toast(`已清理 ${result.deleted || 0} 个超过 ${days} 天未上报的节点。`);
      load(true);
    } catch (error) {
      A.toast('清理失败：' + error.message);
    }
  };
  A.actions['agent-detail'] = button => openDetail(button.dataset.hostId);
  A.actions['agent-detail-refresh'] = button => openDetail(button.dataset.hostId);

  A.root.addEventListener('input', event => {
    if (event.target.id !== 'agent-search') return;
    state.query = event.target.value;
    state.page = 1;
    if (state.searchTimer) clearTimeout(state.searchTimer);
    state.searchTimer = setTimeout(() => {
      state.page = 1;
      load(true);
    }, 300);
  });
  A.root.addEventListener('change', event => {
    if (event.target.id === 'agent-sort') {
      state.sort = event.target.value;
      state.page = 1;
      load(true);
      return;
    }
    if (event.target.id === 'agent-page-size') {
      const size = Number(event.target.value);
      if ([25, 50, 100, 200].includes(size)) {
        state.pageSize = size;
        state.page = 1;
        load(true);
      }
      return;
    }
    if (event.target.id === 'agent-select-all') {
      if (event.target.checked) state.hosts.forEach(host => state.selected.add(host.host_id));
      else state.hosts.forEach(host => state.selected.delete(host.host_id));
      rerender();
      return;
    }
    if (event.target.matches('input[data-agent-id]')) {
      const id = event.target.dataset.agentId;
      if (event.target.checked) state.selected.add(id);
      else state.selected.delete(id);
      rerender();
    }
  });
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden && state.auto && A.state.page === 'agents' && (!state.nextAt || Date.now() >= state.nextAt)) load(false);
  });

  A.agentState = state;
  A.loadAgents = load;
  startTimer();
})();
