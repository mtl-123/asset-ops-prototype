(function () {
  'use strict';
  const A = window.AssetDemo;
  if (!A) return;
  const E = A.escape;
  const state = {
    query: '', groupBy: 'department', activeTabId: '', sequence: 0, paneSequence: 0,
    tabs: [], panes: {}
  };
  const api = window.QiyunAPI;
  const adapter = {
    available() { return !!(window.LinuxTerminalGateway && typeof window.LinuxTerminalGateway.connect === 'function'); },
    connect(pane, host) {
      if (!this.available()) return null;
      try {
        return window.LinuxTerminalGateway.connect({
          sessionId: pane.id, hostId: host.id, host,
          onData: data => appendOutput(pane.id, String(data || '')),
          onStatus: (status, detail) => updateStatus(pane.id, status, detail)
        });
      } catch (error) {
        appendOutput(pane.id, '\r\n[连接失败] ' + (error?.message || 'SSH 网关不可用'));
        updateStatus(pane.id, 'error');
        return null;
      }
    }
  };
  function linuxHosts() { return api?.getAssets?.() || []; }
  function getHost(id) { return linuxHosts().find(h => h.id === id); }
  const reachability = new Map();
  let diagnosticBusy = false;
  const fingerprintReady = host => host?.hostKeyStatus === 'verified';
  function hostHealth(id) { return reachability.get(id) || { state: 'unknown', label: '未检测', detail: '点击检查 SSH；指纹核验不代表连通' }; }
  function setHostHealth(id, state, label, detail = '') {
    reachability.set(id, { state, label, detail, checkedAt: new Date().toLocaleString() });
    // 只更新资产行，不触碰全屏 stage、分屏或 xterm。
    A.root.querySelectorAll('.terminal-asset-main').forEach(button => {
      if (button.dataset.host !== id) return;
      const health = hostHealth(id), row = button.closest('.terminal-asset-row');
      row.dataset.reachability = health.state;
      const text = row.querySelector('.terminal-health');
      if (text) { text.textContent = health.label; text.title = health.detail + ' · ' + health.checkedAt; }
    });
  }
  async function checkHost(id) {
    if (diagnosticBusy) { A.toast('已有 SSH 检查正在进行，请完成后再检查，避免重复认证。'); return null; }
    diagnosticBusy = true;
    setHostHealth(id, 'checking', '检测中', '正在执行一次 SSH 认证与命令探测');
    const pending = hostHealth(id);
    try {
      const report = await api.diagnoseAsset(id);
      if (hostHealth(id) === pending) {
        const failed = report.stages?.find(stage => stage.ok === false);
        if (report.ok === true) setHostHealth(id, 'reachable', 'SSH 连通', '诊断通过：认证与命令执行成功');
        else if (failed && ['tcp', 'authenticate', 'session', 'exec'].includes(failed.stage)) {
          setHostHealth(id, 'failed', failed.stage === 'tcp' ? 'SSH 不通（TCP 失败）' : 'SSH 连接失败', failed.detail || report.summary);
        } else setHostHealth(id, 'unknown', '未确认连通', report.summary || '检查未完成，请查看诊断详情');
      }
      return report;
    } catch (error) {
      if (hostHealth(id) === pending) setHostHealth(id, 'unknown', '检测失败（未确认）', error.message);
      throw error;
    } finally { diagnosticBusy = false; }
  }
  function activeTab() { return state.tabs.find(t => t.id === state.activeTabId) || null; }
  function getPane(id) { return state.panes[id] || null; }
  function makePane(hostId) {
    const host = getHost(hostId); if (!host) return null;
    const id = 'term-pane-' + (++state.paneSequence), connected = fingerprintReady(host);
    const pane = {
      id, hostId, status: connected ? 'connecting' : 'offline', transport: 'gateway',
      output: connected ? `栖云 Web Terminal\r\n正在通过资产身份连接 ${host.name} (${host.ip})...\r\n` : `无法连接 ${host.name} (${host.ip})\r\n资产当前状态：${host.status}`,
      handle: null, terminal: null, fitAddon: null, resizeObserver: null
    };
    state.panes[id] = pane;
    if (connected) {
      pane.handle = adapter.connect(pane, host);
      if (!pane.handle) {
        pane.status = 'error';
        pane.output += '\r\nSSH 网关未加载，连接已拒绝。\r\n';
      }
    }
    return pane;
  }
  const MAX_PANES = 4;
  const tabForPane = id => state.tabs.find(tab => tab.paneIds.includes(id));
  function canSplit(tab) {
    if (tab.paneIds.length < MAX_PANES) return true;
    A.toast('单个标签最多显示 4 个终端。'); return false;
  }
  function openHost(hostId, mode = 'tab') {
    const host = getHost(hostId);
    if (!host) return A.toast('只支持从资产树打开 Linux 主机。');
    if (mode === 'split' && activeTab()) {
      const tab = activeTab(); if (!canSplit(tab)) return;
      const pane = makePane(hostId); if (!pane) return;
      tab.paneIds.push(pane.id); tab.activePaneId = pane.id; tab.layout = tab.paneIds.length === 2 ? 'columns' : 'grid'; render(); return;
    }
    const existing = state.tabs.find(t => t.hostId === hostId && t.paneIds.length === 1);
    if (existing && mode !== 'new') { state.activeTabId = existing.id; render(); return; }
    const pane = makePane(hostId); if (!pane) return;
    const tab = { id: 'term-tab-' + (++state.sequence), hostId, title: host.name, paneIds: [pane.id], activePaneId: pane.id, layout: 'single', sync: false };
    state.tabs.push(tab); state.activeTabId = tab.id; render();
  }
  function closePaneHandle(pane) {
    if (!pane) return;
    delete state.panes[pane.id];
    if (!Object.values(state.panes).some(p => p.hostId === pane.hostId && p.status === 'connected')) {
      setHostHealth(pane.hostId, 'unknown', '连通待复查', '终端已主动关闭，不代表设备不通');
    }
    for (const cleanup of [() => pane.handle?.close?.(), () => pane.resizeObserver?.disconnect?.(), () => pane.terminal?.dispose?.()]) {
      try { cleanup(); } catch {}
    }
    pane.element?.closest('.terminal-pane')?.remove();
  }
  function closeTab(id) {
    const index = state.tabs.findIndex(t => t.id === id); if (index < 0) return;
    state.tabs[index].paneIds.forEach(pid => closePaneHandle(getPane(pid))); state.tabs.splice(index, 1);
    if (state.activeTabId === id) state.activeTabId = state.tabs[Math.max(0, index - 1)]?.id || state.tabs[0]?.id || '';
    render();
  }
  function splitActive(layout) {
    const tab = activeTab(); if (!tab) return A.toast('请先从资产树打开一台 Linux 主机。');
    if (!canSplit(tab)) return;
    const current = getPane(tab.activePaneId) || getPane(tab.paneIds[0]), pane = makePane(current.hostId);
    tab.paneIds.push(pane.id); tab.activePaneId = pane.id; tab.layout = ['rows', 'columns'].includes(layout) ? layout : 'grid'; render();
  }
  function closePane(id) {
    const tab = tabForPane(id);
    if (!tab || tab.paneIds.length === 1 || !tab.paneIds.includes(id)) return A.toast('标签内至少保留一个终端。');
    closePaneHandle(getPane(id)); tab.paneIds = tab.paneIds.filter(pid => pid !== id); tab.activePaneId = tab.paneIds[0];
    if (tab.paneIds.length === 1) tab.layout = 'single'; render();
  }
  const terminalDecoder = new TextDecoder();
  function writeTerminalOutput(id, data) {
    const pane = getPane(id); if (!pane) return;
    const bytes = typeof data === 'string' ? data : new Uint8Array(data);
    const text = typeof bytes === 'string' ? bytes : terminalDecoder.decode(bytes, { stream: true });
    pane.output = (pane.output + text).slice(-100000);
    pane.terminal?.write(bytes);
  }
  function appendOutput(id, text) {
    const pane = getPane(id); if (!pane) return;
    pane.output = (pane.output + text).slice(-100000);
    pane.terminal?.write(text);
  }
  function updateStatus(id, status, detail = {}) {
    const pane = getPane(id); if (!pane) return;
    if (!['connected', 'connecting', 'offline', 'error'].includes(status)) return;
    const next = status;
    if (next === 'connected') setHostHealth(pane.hostId, 'reachable', 'SSH 连通', '实际 SSH 会话已就绪');
    else if (next === 'error' && detail.code === 'SSH_CONNECT_FAILED') {
      setHostHealth(pane.hostId, 'failed', 'SSH 连接失败', detail.message || '连接或认证失败，不等同于网络断开');
    } else if (next === 'offline' || next === 'error') {
      setHostHealth(pane.hostId, 'unknown', '连通待复查', detail.message || '会话结束或网关异常，不代表设备不通');
    }
    if (pane.status !== next) inputRevision++;
    pane.status = next;
    // 状态通知不能替换全屏工作区或销毁 xterm 的选区。
    A.root.querySelectorAll(`[data-pane="${id}"] .terminal-status`).forEach(el => { el.className = 'terminal-status terminal-status-' + next; });
    state.tabs.filter(tab => tab.paneIds[0] === id).forEach(tab => {
      A.root.querySelectorAll(`[data-action="terminal-tab"][data-id="${tab.id}"] .terminal-status`).forEach(el => { el.className = 'terminal-status terminal-status-' + next; });
    });
  }
  function runCommand(paneId, command) {
    const tab = tabForPane(paneId), source = getPane(paneId); if (!tab || !source || source.status !== 'connected') return;
    tab.activePaneId = paneId;
    const targets = tab.sync ? tab.paneIds.map(getPane).filter(p => p?.status === 'connected') : [source];
    targets.forEach(pane => executeForPane(pane, command, pane.id === paneId)); render();
  }
  function executeForPane(pane, command, isSource) {
    const clean = String(command || '').trim(); if (!clean) return;
    appendOutput(pane.id, clean + '\r\n');
    if (!pane.handle?.write) {
      appendOutput(pane.id, '[SSH_NOT_CONNECTED] 当前分屏没有可用的真实 SSH 会话。\r\n');
      pane.status = 'error';
      return;
    }
    pane.handle.write(clean + '\r');
  }
  function groupLabel(host) { return state.groupBy === 'environment' ? host.environment || '未分环境' : host.department || '未分部门'; }
  function assetTree() {
    const status = api?.assetStatus?.() || { loaded: true, error: null };
    if (!status.loaded) {
      // 「没有登记主机」和「没读到主机」是两件事：混淆会让人重复添加同一台机器。
      const body = status.error
        ? `<p>资产清单载入失败</p><small>${E(status.error.message || '无法读取服务端资产')}${status.retrying ? ' · 正在自动重试' : ''}</small><button type="button" data-action="terminal-reload-assets">⟳ 立即重新载入</button>`
        : `<p>正在载入资产清单</p><small>正在从 Go 服务读取已登记主机</small>`;
      return `<aside class="terminal-assets"><div class="terminal-assets-head"><div><strong>Linux 资产</strong><small>${status.error ? '载入失败' : '载入中'}</small></div><button class="terminal-add-asset" data-action="terminal-add-asset" title="添加 Linux 资产" aria-label="添加 Linux 资产">＋</button></div><div class="terminal-empty-tree">${body}</div></aside>`;
    }
    const query = state.query.toLowerCase(), list = linuxHosts().filter(h => [h.name, h.id, h.ip, h.owner, groupLabel(h)].join(' ').toLowerCase().includes(query));
    const groups = list.reduce((all, host) => ((all[groupLabel(host)] ||= []).push(host), all), {});
    return `<aside class="terminal-assets"><div class="terminal-assets-head"><div><strong>Linux 资产</strong><small>${list.length} 台已登记</small></div><button class="terminal-add-asset" data-action="terminal-add-asset" title="添加 Linux 资产" aria-label="添加 Linux 资产">＋</button></div><label class="terminal-search"><span class="vab-icon vab-icon-search"></span><input id="terminal-search" value="${E(state.query)}" placeholder="搜索名称、IP、负责人"></label><div class="terminal-group-switch" role="group" aria-label="资产分组方式"><button data-action="terminal-group" data-id="department" aria-pressed="${state.groupBy === 'department'}">部门</button><button data-action="terminal-group" data-id="environment" aria-pressed="${state.groupBy === 'environment'}">环境</button></div><p class="terminal-hint">灰：未检测/待复查；绿：SSH 连通；红：SSH 连接失败。点击每台设备的“◎”检查（会尝试认证），不自动检测。</p><div class="terminal-tree">${Object.entries(groups).map(([group, hosts]) => `<section><div class="terminal-tree-group"><span>⌄</span><strong>${E(group)}</strong><small>${hosts.length}</small></div>${hosts.map(host => `<div class="terminal-asset-row" data-reachability="${hostHealth(host.id).state}"><button class="terminal-asset-main" data-action="terminal-open" data-host="${E(host.id)}" ${!fingerprintReady(host) ? 'aria-disabled="true"' : ''}><span class="terminal-host-icon"><span class="vab-icon vab-icon-server"></span></span><span><strong>${E(host.name)}</strong><small>${E(host.ip)} · <span class="terminal-health" role="status" title="${E(hostHealth(host.id).detail)}${hostHealth(host.id).checkedAt ? ' · ' + E(hostHealth(host.id).checkedAt) : ''}">${E(hostHealth(host.id).label)}</span>${fingerprintReady(host) ? '' : ' · 指纹待核验'}</small></span></button>${fingerprintReady(host) ? `<button class="terminal-asset-split" data-action="terminal-open-new" data-host="${E(host.id)}" title="新建独立终端（独立 SSH）" aria-label="新建 ${E(host.name)} 独立终端">＋</button><button class="terminal-asset-split" data-action="terminal-open-split" data-host="${E(host.id)}" title="在当前标签分屏打开" aria-label="在当前标签分屏打开 ${E(host.name)}">⊞</button>` : `<button class="terminal-asset-split" data-action="terminal-verify" data-host="${E(host.id)}" title="扫描并核验 SSH 指纹" aria-label="核验 ${E(host.name)} 的 SSH 指纹">✓</button>`}<button class="terminal-asset-diag" data-action="terminal-diagnose" data-host="${E(host.id)}" title="检查 SSH（单次认证，不自动重试）" aria-label="诊断 ${E(host.name)} 的连接">◎</button><button class="terminal-asset-edit" data-action="terminal-edit-host" data-host="${E(host.id)}" title="编辑资产" aria-label="编辑 ${E(host.name)}">✎</button><button class="terminal-asset-delete" data-action="terminal-delete-asset" data-host="${E(host.id)}" title="删除资产" aria-label="删除 ${E(host.name)}">×</button></div>`).join('')}</section>`).join('') || (linuxHosts().length === 0 ? `<div class="terminal-empty-tree"><p>还没有登记 Linux 主机</p><small>凭据加密保存在服务端，核验指纹后可直接打开终端</small><button type="button" data-action="terminal-add-asset">＋ 添加真实主机</button></div>` : '<div class="empty-state">没有匹配的 Linux 资产</div>')}</div></aside>`;
  }
  function paneMarkup(pane, tab) {
    const host = getHost(pane.hostId), active = tab.activePaneId === pane.id;
    return `<section class="terminal-pane ${active ? 'active' : ''}" data-pane="${pane.id}" data-action="terminal-focus-pane" data-id="${pane.id}"><header><div><span class="terminal-status terminal-status-${pane.status}"></span><strong>${E(host?.name || pane.hostId)}</strong><small>${E(host?.ip || '')}</small></div><div><span class="terminal-mode">SSH · ${E(host?.hostKeyFingerprint || '指纹待回读')}</span>${tab.paneIds.length > 1 ? `<button data-action="terminal-close-pane" data-id="${pane.id}" aria-label="关闭此分屏" title="关闭此分屏">×</button>` : ''}</div></header><div class="terminal-xterm" data-terminal-xterm="${pane.id}"></div></section>`;
  }
  function workspace() {
    const tab = activeTab();
    if (!tab) return `<div class="terminal-empty"><div class="terminal-empty-icon">›_</div><h2>选择一台 Linux 资产开始连接</h2><p>从左侧资产树点击主机，系统将按资产身份自动建立 SSH 会话。</p><div><span>文件传输面板</span><span>多标签</span><span>分屏同步</span><span>SSH 诊断</span></div></div>`;
    return `<div class="terminal-session-tabs" role="tablist">${state.tabs.map(t => { const host = getHost(t.hostId); return `<button role="tab" aria-selected="${t.id === state.activeTabId}" data-action="terminal-tab" data-id="${t.id}"><span class="terminal-status terminal-status-${getPane(t.paneIds[0])?.status || 'offline'}"></span><strong>${E(t.title)}</strong><small>${E(host?.ip || '')}</small><span class="terminal-tab-close" data-action="terminal-close-tab" data-id="${t.id}" aria-label="关闭标签">×</span></button>`; }).join('')}</div><div class="terminal-toolbar"><div class="terminal-layouts"><button data-action="terminal-split" data-layout="columns" title="左右分屏" aria-label="左右分屏">◫</button><button data-action="terminal-split" data-layout="rows" title="上下分屏" aria-label="上下分屏">⬒</button><button data-action="terminal-split" data-layout="grid" title="网格分屏" aria-label="网格分屏">⊞</button></div><label class="terminal-sync"><input type="checkbox" data-action="terminal-sync" ${tab.sync ? 'checked' : ''}><span>同步操作</span></label><div class="terminal-tools"><button data-action="terminal-open-new" data-host="${E(getPane(tab.activePaneId)?.hostId || tab.hostId)}" title="为当前主机新建标签和独立 SSH 会话">新建独立终端</button><button data-action="terminal-copy" title="复制活动终端选区（Ctrl+Shift+C）">复制</button><button data-action="terminal-paste" title="粘贴到活动终端（Ctrl+Shift+V）">粘贴</button><button data-action="terminal-transfer" title="大文件分块传输，支持断点续传">文件传输</button><button data-action="terminal-fullscreen" aria-pressed="false" title="终端工作区全屏">全屏</button></div><div class="terminal-workspace-status"><span>${tab.paneIds.length} 个会话</span><span>传输任务按分屏隔离</span></div></div><div class="terminal-panes layout-${tab.layout}" style="--pane-count:${tab.paneIds.length}">${tab.paneIds.map(pid => paneMarkup(getPane(pid), tab)).join('')}</div>`;
  }
  let formAuth = 'password';
  const AUTH_LABELS = { password: '密码', private_key: 'SSH 私钥' };
  function authSwitch() {
    return `<div class="terminal-auth-switch" role="group" aria-label="认证方式">${Object.entries(AUTH_LABELS).map(([id, label]) => `<button type="button" data-action="terminal-auth-type" data-id="${id}" aria-pressed="${formAuth === id}">${label}</button>`).join('')}</div>`;
  }
  function assetForm() {
    formAuth = 'password';
    A.modal('添加 Linux 资产', `<form id="terminal-asset-form"><div class="form-grid">
      <div class="field"><label for="ta-code">资产编码 *</label><input id="ta-code" required maxlength="60" placeholder="例如 SRV-PROD-01"></div>
      <div class="field"><label for="ta-name">资产名称 *</label><input id="ta-name" required maxlength="80" placeholder="例如 生产应用服务器"></div>
      <div class="field"><label for="ta-address">IP 或主机名 *</label><input id="ta-address" required maxlength="255" placeholder="10.20.30.21"></div>
      <div class="field"><label for="ta-port">SSH 端口 *</label><input id="ta-port" type="number" min="1" max="65535" value="22" required></div>
      <div class="field"><label for="ta-department">部门</label><input id="ta-department" maxlength="60"></div>
      <div class="field"><label for="ta-environment">环境</label><select id="ta-environment"><option value="production">生产环境</option><option value="staging">预发布环境</option><option value="testing">测试环境</option><option value="development">开发环境</option></select></div>
      <div class="field"><label for="ta-owner">负责人</label><input id="ta-owner" maxlength="60"></div>
      <div class="field"><label for="ta-os">操作系统</label><input id="ta-os" maxlength="80" value="Linux"></div>
      <div class="field"><label for="ta-username">SSH 用户名 *</label><input id="ta-username" required maxlength="64" autocomplete="username" placeholder="例如 root 或 ops"></div>
      <div class="field"><label>认证方式 *</label>${authSwitch()}</div>
    </div><div data-auth-block="password" ${formAuth === 'password' ? '' : 'hidden'}><div class="field"><label for="ta-password">SSH 密码 *</label><div class="terminal-secret-row"><input id="ta-password" type="password" autocomplete="current-password" placeholder="输入该账号的 SSH 登录密码"><button type="button" class="terminal-reveal" data-action="terminal-toggle-password" data-target="ta-password" aria-label="显示或隐藏密码">显示</button></div></div></div><div data-auth-block="private_key" hidden><div style="height:14px"></div><div class="field"><label for="ta-keyfile">从本地选择私钥文件</label><div class="terminal-file-row"><input id="ta-keyfile" type="file" accept=".pem,.key,.ppk,.txt,id_rsa,id_ed25519,text/plain"><span id="ta-key-status" class="terminal-key-status">支持 OpenSSH / PEM，.ppk 需先转换</span></div><p class="terminal-hint">只读取你选择的文件内容填入下方文本框，不做其他上传。</p></div><div class="field"><label for="ta-secret">或直接粘贴私钥内容 *</label><textarea id="ta-secret" rows="5" spellcheck="false" autocomplete="off" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"></textarea></div><div class="field"><label for="ta-passphrase">私钥口令（私钥已加密时必填）</label><input id="ta-passphrase" type="password" autocomplete="new-password"></div></div><div id="terminal-asset-error" class="text-red small" role="alert"></div></form><div class="notice">凭据只提交给同源 Go 服务，并以 AES-GCM 加密保存。保存后会先扫描 SSH 主机指纹，核验通过才可连接；连接失败可在资产行点击“◎”查看逐步诊断原因。</div>`, A.button('取消', 'close-modal') + A.button('保存并扫描指纹', 'terminal-save-asset', true));
  }
  function credentialForm(asset) {
    formAuth = asset.authType || 'password';
    A.modal(`修改凭据 · ${asset.name}`, `<form id="terminal-credential-form"><input type="hidden" id="tc-asset" value="${E(asset.id)}"><div class="notice amber">修改凭据不会影响已经批准的主机指纹，保存后可直接重新诊断连接。</div><div class="form-grid" style="margin-top:14px"><div class="field"><label for="tc-username">SSH 用户名 *</label><input id="tc-username" required maxlength="64" value="${E(asset.username || '')}" autocomplete="username"></div><div class="field"><label>认证方式 *</label>${authSwitch()}</div></div><div data-auth-block="password" ${formAuth === 'password' ? '' : 'hidden'}><div class="field"><label for="tc-password">新的 SSH 密码 *</label><div class="terminal-secret-row"><input id="tc-password" type="password" autocomplete="new-password"><button type="button" class="terminal-reveal" data-action="terminal-toggle-password" data-target="tc-password" aria-label="显示或隐藏密码">显示</button></div></div></div><div data-auth-block="private_key" ${formAuth === 'private_key' ? '' : 'hidden'}><div class="field"><label for="tc-keyfile">从本地选择私钥文件</label><div class="terminal-file-row"><input id="tc-keyfile" type="file" accept=".pem,.key,.ppk,.txt,id_rsa,id_ed25519,text/plain"><span id="tc-key-status" class="terminal-key-status">支持 OpenSSH / PEM</span></div></div><div class="field"><label for="tc-secret">粘贴新的私钥内容 *</label><textarea id="tc-secret" rows="5" spellcheck="false" autocomplete="off"></textarea></div><div class="field"><label for="tc-passphrase">私钥口令</label><input id="tc-passphrase" type="password" autocomplete="new-password"></div></div><div id="terminal-credential-error" class="text-red small" role="alert"></div></form>`, A.button('取消', 'close-modal') + A.button('保存并诊断', 'terminal-save-credential', true));
  }
  const formValue = id => A.root.querySelector('#' + id)?.value?.trim() || '';
  function detectPrivateKey(text) {
    const value = String(text || '').replace(/\r\n/g, '\n').trim();
    if (!value) return { ok: false, message: '尚未选择或粘贴私钥' };
    if (value.includes('PuTTY-User-Key-File')) return { ok: false, message: '这是 PuTTY .ppk 私钥，请先转换为 OpenSSH 格式' };
    if (/^(ssh-rsa|ssh-ed25519|ecdsa-sha2-)/.test(value)) return { ok: false, message: '这是公钥，请选择不带 .pub 的私钥文件' };
    const match = value.match(/-----BEGIN ([A-Z0-9 ]*PRIVATE KEY)-----/);
    if (!match) return { ok: false, message: '未识别到 PRIVATE KEY 头，内容可能不是私钥' };
    const encrypted = value.includes('ENCRYPTED') || /Proc-Type:\s*4,\s*ENCRYPTED/.test(value);
    return { ok: true, message: `已识别 ${match[1]}${encrypted ? '，已加密：必须填写私钥口令' : '，未加密'}` };
  }
  async function loadKeyFile(input) {
    const file = input.files?.[0]; if (!file) return;
    const status = A.root.querySelector('#' + input.id.replace('keyfile', 'key-status'));
    try {
      const text = await file.text();
      const textarea = A.root.querySelector('#' + input.id.replace('keyfile', 'secret'));
      if (textarea) textarea.value = text;
      const detected = detectPrivateKey(text);
      if (status) { status.textContent = `${file.name}（${Math.max(1, Math.round(file.size / 1024))} KB）· ${detected.message}`; status.dataset.state = detected.ok ? 'ok' : 'error'; }
    } catch (error) {
      if (status) { status.textContent = '读取失败：' + (error.message || error); status.dataset.state = 'error'; }
    }
  }
  function credentialPayload(prefix) {
    return {
      username: formValue(prefix + '-username'),
      auth_type: formAuth,
      secret: formAuth === 'password' ? (A.root.querySelector('#' + prefix + '-password')?.value || '') : (A.root.querySelector('#' + prefix + '-secret')?.value || ''),
      passphrase: A.root.querySelector('#' + prefix + '-passphrase')?.value || ''
    };
  }
  function setFormError(formID, message, tone = 'error') {
    const out = A.root.querySelector(`#${formID}-error`); if (!out) return;
    out.textContent = message; out.dataset.tone = tone;
  }
  async function saveAsset() {
    const form = A.root.querySelector('#terminal-asset-form'); if (!form?.reportValidity()) return;
    const cred = credentialPayload('ta');
    if (!cred.username) return setFormError('terminal-asset', '请填写 SSH 用户名');
    if (formAuth === 'private_key') {
      const detected = detectPrivateKey(cred.secret);
      if (!detected.ok) return setFormError('terminal-asset', detected.message);
    }
    setFormError('terminal-asset', '正在保存资产并扫描 SSH 指纹...', 'info');
    let asset;
    try {
      asset = await api.createAsset({ asset_code: formValue('ta-code'), name: formValue('ta-name'), address: formValue('ta-address'), ssh_port: Number(formValue('ta-port')), department: formValue('ta-department'), environment: formValue('ta-environment'), owner: formValue('ta-owner'), os_name: formValue('ta-os'), ...cred });
      await api.refreshAssets();
    } catch (error) { return setFormError('terminal-asset', error.message); }
    try {
      const scan = await api.scanHostKey(asset.id); fingerprintModal(asset, scan);
    } catch (error) { setFormError('terminal-asset', `资产已保存，但 SSH 指纹扫描失败：${error.message}${error.hint ? `（建议：${error.hint}）` : ''}`); }
  }
  async function saveCredential() {
    const form = A.root.querySelector('#terminal-credential-form'); if (!form?.reportValidity()) return;
    const id = formValue('tc-asset'), cred = credentialPayload('tc');
    if (!cred.username) return setFormError('terminal-credential', '请填写 SSH 用户名');
    if (formAuth === 'private_key') { const detected = detectPrivateKey(cred.secret); if (!detected.ok) return setFormError('terminal-credential', detected.message); }
    if (formAuth === 'password' && !cred.secret.trim()) return setFormError('terminal-credential', '请填写新的 SSH 密码');
    setFormError('terminal-credential', '正在保存并诊断连接...', 'info');
    try {
      await api.updateCredential(id, cred); await api.refreshAssets();
      const d = await checkHost(id); A.closeModal(); render(); diagnosticModal(d);
    } catch (error) { setFormError('terminal-credential', error.message); }
  }
  function fingerprintModal(asset, scan) {
    A.modal('确认 SSH 主机指纹', `<div class="notice amber">请通过服务器控制台、云平台或现有 known_hosts 独立核对下面的 SHA-256 指纹。未确认前系统不会建立 SSH 会话。</div><div class="infra-kv" style="margin-top:18px"><span>资产</span><strong>${E(asset.name)}</strong><span>实际目标</span><strong class="mono">${E(scan.target)}</strong><span>主机指纹</span><strong class="mono">${E(scan.fingerprint)}</strong><span>公钥</span><strong class="mono terminal-host-key">${E(scan.host_key)}</strong></div>`, A.button('稍后确认', 'close-modal') + A.button('指纹一致，批准并诊断', 'terminal-approve-host-key', true, `data-id="${E(asset.id)}" data-key="${E(scan.host_key)}" data-fingerprint="${E(scan.fingerprint)}"`));
  }
  function diagnosticModal(d, options = {}) {
    if (!d) return;
    const stages = (d.stages || []).map(stage => `<li class="terminal-diag-step ${stage.ok ? 'ok' : 'fail'}"><span class="terminal-diag-mark">${stage.ok ? '✓' : '✕'}</span><div><strong>${E(stage.label)}</strong><p>${E(stage.detail)}</p>${stage.hint ? `<p class="terminal-diag-hint">建议：${E(stage.hint)}</p>` : ''}</div></li>`).join('');
    const keyBlock = d.authorized_key ? `<div class="terminal-diag-key"><span>私钥对应的公钥（与目标机 ~/.ssh/authorized_keys 逐字比对，或直接追加）</span><code class="mono" id="terminal-diag-key-line">${E(d.authorized_key)}</code><div class="terminal-diag-actions"><button type="button" data-action="terminal-copy-key" data-id="${E(d.asset_id || '')}" >复制公钥</button><span>目标机执行：mkdir -p ~/.ssh &amp;&amp; echo '${'&lt;复制的公钥&gt;'} ' &gt;&gt; ~/.ssh/authorized_keys &amp;&amp; chmod 600 ~/.ssh/authorized_keys</span></div></div>` : '';
    const summary = d.ok ? `<div class="notice">全部步骤通过：${E(d.username)}@${E(d.target || '')} 可以建立交互终端。</div>` : `<div class="notice amber">${E(d.summary || '连接未通过，请按下方步骤修正。')}</div>`;
    const buttons = options.assetID && !d.ok ? A.button('修改凭据', 'terminal-edit-credential', true, `data-id="${E(options.assetID)}"`) + A.button('关闭', 'close-modal') : A.button('关闭', 'close-modal');
    A.modal(`SSH 连接诊断 · ${d.asset_name || d.username || ''}`, `${summary}<div class="terminal-diag-meta"><span>目标</span><strong class="mono">${E(d.target || '未解析')}</strong><span>用户</span><strong class="mono">${E(d.username || '')}</strong><span>认证方式</span><strong>${E(AUTH_LABELS[d.auth_type] || d.auth_type || '')}</strong>${d.key_fingerprint ? `<span>公钥指纹</span><strong class="mono">${E(d.key_fingerprint)}</strong>` : ''}</div><ol class="terminal-diag-list">${stages}</ol>${keyBlock}`, buttons);
  }
  async function diagnoseAsset(id) {
    try { diagnosticModal(await checkHost(id), { assetID: id }); }
    catch (error) { A.toast('诊断失败：' + error.message); }
  }
  async function copyPublicKey() {
    const line = A.root.querySelector('#terminal-diag-key-line')?.textContent?.trim();
    if (!line) return A.toast('没有可复制的公钥。');
    try { await navigator.clipboard.writeText(line); A.toast('公钥已复制，可粘贴到目标机的 ~/.ssh/authorized_keys。'); }
    catch { A.toast('浏览器拒绝剪贴板访问，请手动选中公钥文本复制。'); }
  }
  async function deleteAsset(id) {
    const asset = api.getAssets().find(item => item.id === id); if (!asset) return;
    if (!window.confirm?.(`确认删除资产“${asset.name}”？关联终端会话将立即关闭。`)) return;
    try {
      for (const tab of [...state.tabs]) if (tab.hostId === id || tab.paneIds.some(paneID => getPane(paneID)?.hostId === id)) closeTab(tab.id);
      await api.deleteAsset(id); await api.refreshAssets(); A.toast('资产已删除。');
    } catch (error) { A.toast('删除失败：' + error.message); }
  }
  async function verifyHost(id) {
    const asset = api.getAssets().find(item => item.id === id); if (!asset) return A.toast('资产不存在或已删除。');
    try { const scan = await api.scanHostKey(id); fingerprintModal({ id, name: asset.name }, scan); }
    catch (error) { A.toast(`SSH 指纹扫描失败：${error.message}${error.hint ? `（建议：${error.hint}）` : ''}`); }
  }
  async function approveHostKey(el) {
    const id = el.dataset.id;
    try {
      await api.approveHostKey(id, { host_key: el.dataset.key, fingerprint: el.dataset.fingerprint });
    } catch (error) { return A.toast('指纹批准失败：' + error.message); }
    let report;
    try { report = await checkHost(id); if (!report) return; }
    catch (error) { return A.toast('已批准指纹，但诊断请求失败：' + error.message); }
    await api.refreshAssets(); render();
    // Surface every step, not just "failed", so the operator can see which link broke.
    diagnosticModal(report, { assetID: id });
    if (report.ok) A.toast('SSH 指纹已批准，认证与命令执行均已通过。');
  }
  function switchAuthType(el) {
    formAuth = el.dataset.id === 'private_key' ? 'private_key' : 'password';
    A.root.querySelectorAll('[data-auth-block]').forEach(block => { block.hidden = block.dataset.authBlock !== formAuth; });
    A.root.querySelectorAll('[data-action="terminal-auth-type"]').forEach(button => button.setAttribute('aria-pressed', String(button.dataset.id === formAuth)));
  }
  function togglePassword(el) {
    const input = A.root.querySelector('#' + el.dataset.target); if (!input) return;
    const reveal = input.type === 'password';
    input.type = reveal ? 'text' : 'password';
    el.textContent = reveal ? '隐藏' : '显示';
    el.setAttribute('aria-pressed', String(reveal));
  }

  let inputRevision = 0, pasteDispatch = false;
  const clipboardHint = /Mac|iPhone|iPad/.test(window.navigator?.platform || '') ? 'Command+C / Command+V' : 'Ctrl+Insert / Shift+Insert';
  function clipboardContext() {
    const tab = activeTab(), pane = getPane(tab?.activePaneId);
    if (!pane?.terminal || pane.status !== 'connected') { A.toast('当前终端未连接，不能粘贴。'); return null; }
    const targets = (tab.sync ? tab.paneIds.map(getPane) : [pane]).filter(p => p?.status === 'connected');
    return { tab, pane, sync: tab.sync, revision: inputRevision, targets: targets.map(p => ({ pane: p, terminal: p.terminal, handle: p.handle })) };
  }
  function validClipboardContext(context) {
    return A.state.page === 'terminal' && context.revision === inputRevision && activeTab() === context.tab && context.tab.activePaneId === context.pane.id && context.tab.sync === context.sync && context.targets.every(t => getPane(t.pane.id) === t.pane && t.pane.status === 'connected' && t.pane.terminal === t.terminal && t.pane.handle === t.handle);
  }
  async function copySelection() {
    const text = getPane(activeTab()?.activePaneId)?.terminal?.getSelection?.();
    if (!text) return A.toast('当前活动终端没有选区，请先选中文本。');
    try { await window.navigator.clipboard.writeText(text); A.toast('终端选区已复制。'); }
    catch { A.toast(`无法访问剪贴板，请使用原生快捷键 ${clipboardHint}，或浏览器复制菜单。`); }
  }
  function confirmPaste(message) {
    const doc = window.document, stage = A.root.querySelector('.terminal-stage');
    if (!doc?.fullscreenElement || !stage) return window.confirm(message);
    // 原生确认框可能改变浏览器全屏状态；在全屏树内使用模态对话框。
    return new Promise(resolve => {
      const dialog = doc.createElement('dialog');
      const description = doc.createElement('p'); description.textContent = message;
      const cancel = doc.createElement('button'); cancel.textContent = '取消';
      const accept = doc.createElement('button'); accept.textContent = '确认粘贴';
      dialog.setAttribute('aria-label', '确认终端粘贴');
      dialog.append(description, cancel, accept); stage.appendChild(dialog);
      const finish = value => { dialog.close?.(); dialog.remove(); resolve(value); };
      cancel.addEventListener('click', () => finish(false));
      accept.addEventListener('click', () => finish(true));
      dialog.addEventListener('cancel', event => { event.preventDefault(); finish(false); });
      // 不让 core 的 Escape 处理把焦点移到全屏树外。
      dialog.addEventListener('keydown', event => event.stopPropagation());
      try { dialog.showModal(); cancel.focus(); } catch { finish(false); }
    });
  }
  function pasteText(context, text, confirmed = false) {
    if (!validClipboardContext(context)) return A.toast('会话或输入状态已变化，已取消粘贴，请重试。');
    if (!text) return A.toast('剪贴板没有文本。');
    // 按 JSON 转义后的 UTF-8 大小预留协议和 bracketed paste 开销。
    if (new TextEncoder().encode(JSON.stringify(text)).length > 48 * 1024) return A.toast('粘贴内容过大（上限 48 KiB，含编码开销），请分段粘贴或使用文件传输。');
    if (!confirmed && (/[\r\n]/.test(text) || context.sync)) {
      const result = confirmPaste(`即将向 ${context.targets.length} 个会话粘贴${/[\r\n]/.test(text) ? '多行' : ''}文本，可能执行命令。是否继续？`);
      if (result && typeof result.then === 'function') return result.then(ok => { if (ok) return pasteText(context, text, true); });
      if (!result) return;
    }
    if (!validClipboardContext(context)) return A.toast('会话状态已变化，已取消粘贴。');
    inputRevision++;
    pasteDispatch = true;
    try { context.targets.forEach(t => t.terminal.paste(text)); }
    finally { pasteDispatch = false; }
    context.pane.terminal.focus();
  }
  async function pasteClipboard() {
    const context = clipboardContext(); if (!context) return;
    let text;
    try { text = await window.navigator.clipboard.readText(); }
    catch { return A.toast(`无法读取剪贴板，请聚焦终端后使用原生快捷键 ${clipboardHint}，或浏览器粘贴菜单。`); }
    return pasteText(context, text);
  }
  function fullscreenChanged() {
    const stage = A.root.querySelector('.terminal-stage');
    const enabled = !!stage && window.document?.fullscreenElement === stage;
    const button = A.root.querySelector('[data-action="terminal-fullscreen"]');
    if (button) { button.textContent = enabled ? '退出全屏' : '全屏'; button.setAttribute('aria-pressed', String(enabled)); button.title = enabled ? '退出全屏（Esc）' : '终端工作区全屏'; }
    requestAnimationFrame(() => activeTab()?.paneIds.forEach(id => fitPane(getPane(id))));
  }
  async function toggleFullscreen() {
    const stage = A.root.querySelector('.terminal-stage'), doc = window.document;
    if (!stage?.requestFullscreen || !doc?.exitFullscreen || doc.fullscreenEnabled === false) return A.toast('浏览器不支持工作区全屏，请使用浏览器全屏功能。');
    try {
      if (doc.fullscreenElement === stage) await doc.exitFullscreen();
      else if (!doc.fullscreenElement) await stage.requestFullscreen();
      else return A.toast('请先退出其他区域的全屏。');
    } catch { A.toast('全屏操作失败，请重试或使用浏览器全屏功能。'); }
    fullscreenChanged();
  }
  window.document?.addEventListener('fullscreenchange', fullscreenChanged);
  window.document?.addEventListener('fullscreenerror', () => A.toast('浏览器拒绝了全屏请求。'));

  function sendTerminalInput(sourceID, data, binary) {
    const tab = tabForPane(sourceID), source = getPane(sourceID); if (!tab || !source || source.status !== 'connected') return;
    if (!pasteDispatch) { inputRevision++; tab.activePaneId = sourceID; }
    const targets = tab.sync && !pasteDispatch ? tab.paneIds.map(getPane).filter(Boolean) : [source];
    for (const pane of targets) {
      if (pane.status !== 'connected') continue;
      // Raw PTY bytes straight to the shell; file transfer is a separate SFTP API.
      pane.handle?.write?.(data);
    }
  }
  function fitPane(pane) {
    if (!pane?.element?.isConnected || getPane(pane.id) !== pane || A.state.page !== 'terminal' || !activeTab()?.paneIds.includes(pane.id)) return;
    if (!pane.element.clientWidth || !pane.element.clientHeight) return;
    try { pane.fitAddon?.fit(); } catch {}
  }
  function mountTerminals() {
    const tab = activeTab(); if (!tab || !window.Terminal) return;
    for (const id of tab.paneIds) {
      const pane = getPane(id), host = getHost(pane?.hostId), element = A.root.querySelector?.(`[data-terminal-xterm="${id}"]`);
      if (!pane || !element || pane.terminal) continue;
      pane.element = element;
      // 黑底浅灰字的类 MobaXterm 风格，并非官方主题复刻。
      const terminal = pane.terminal = new window.Terminal({ cursorBlink: true, convertEol: false, scrollback: 5000, fontSize: 12, lineHeight: 1.25, letterSpacing: 0, fontFamily: 'Cascadia Mono, Consolas, monospace', theme: {
        background: '#000000', foreground: '#d0d0d0', cursor: '#d0d0d0', cursorAccent: '#000000', selectionBackground: '#444444',
        black: '#000000', red: '#aa0000', green: '#00aa00', yellow: '#aa5500', blue: '#0000aa', magenta: '#aa00aa', cyan: '#00aaaa', white: '#aaaaaa',
        brightBlack: '#555555', brightRed: '#ff5555', brightGreen: '#55ff55', brightYellow: '#ffff55', brightBlue: '#5555ff', brightMagenta: '#ff55ff', brightCyan: '#55ffff', brightWhite: '#ffffff'
      } });
      if (window.FitAddon?.FitAddon) { pane.fitAddon = new window.FitAddon.FitAddon(); terminal.loadAddon(pane.fitAddon); }
      terminal.open(element); element.dataset.mounted = 'true'; if (pane.output) terminal.write(pane.output);
      terminal.onData(data => { if (pane.terminal === terminal) sendTerminalInput(id, data, false); }); terminal.onBinary(data => { if (pane.terminal === terminal) sendTerminalInput(id, data, true); });
      terminal.attachCustomKeyEventHandler(event => {
        const key = event.key.toLowerCase();
        if (!event.ctrlKey || !event.shiftKey || event.altKey || event.metaKey || !['c', 'v'].includes(key)) return true;
        if (event.type === 'keydown') {
          event.preventDefault();
          if (!event.repeat) { focusPane(id); key === 'c' ? copySelection() : pasteClipboard(); }
        }
        return false;
      });
      element.addEventListener('focusin', () => focusPane(id));
      element.addEventListener('paste', event => {
        event.preventDefault(); event.stopImmediatePropagation(); focusPane(id);
        const context = clipboardContext();
        if (context) pasteText(context, event.clipboardData?.getData('text/plain') || '');
      }, true);
      terminal.onResize(size => { pane.handle?.resize?.(size.cols, size.rows); });
      requestAnimationFrame(() => { fitPane(pane); if (activeTab() === tab && tab.activePaneId === id && element.isConnected) terminal.focus(); });
      if (window.ResizeObserver) { pane.resizeObserver = new window.ResizeObserver(() => fitPane(pane)); pane.resizeObserver.observe(element); }
      if (!fingerprintReady(host)) terminal.options.disableStdin = true;
    }
  }

  let deepLinked = '';
  function maybeDeepLink() {
    if (typeof location === 'undefined') return;
    const query = String(location.hash || '').split('?')[1];
    if (!query) return;
    const params = new URLSearchParams(query), id = params.get('diag');
    if (!id || id === deepLinked) return;
    deepLinked = id;
    setTimeout(() => diagnoseAsset(id), 50);
  }
  function page() { setTimeout(() => { mountTerminals(); maybeDeepLink(); }, 0); return `<div class="terminal-shell">${assetTree()}<main class="terminal-stage">${workspace()}</main></div><div class="terminal-footnote"><span class="status-dot"></span>${adapter.available() ? '真实模式 · 资产来自 Go 服务，终端通过 SSH WebSocket 网关连接。' : 'SSH 网关未加载，所有远程连接均被拒绝。'}</div>`; }
  let shell = null, footnote = null;
  function renderPage(container) {
    if (!shell) {
      container.innerHTML = page();
      shell = container.querySelector('.terminal-shell');
      footnote = container.querySelector('.terminal-footnote');
    } else if (shell.parentNode !== container) {
      container.replaceChildren(shell, footnote);
    }
    reconcile();
  }
  function reconcile() {
    if (!shell) return;
    const stage = shell.querySelector('.terminal-stage');
    const template = window.document.createElement('template');
    template.innerHTML = workspace();
    // 仅替换无终端实例的控件；stage 及已存在的 pane 从不移除再插入。
    shell.querySelector('.terminal-assets').outerHTML = assetTree();
    for (const selector of ['.terminal-session-tabs', '.terminal-toolbar', '.terminal-empty']) {
      const previous = stage.querySelector(selector), next = template.content.querySelector(selector);
      if (previous && next) previous.replaceWith(next);
      else if (previous) previous.remove();
      else if (next) stage.insertBefore(next, stage.querySelector('.terminal-panes'));
    }
    stage.querySelectorAll('[data-terminal-tab]').forEach(group => {
      if (!state.tabs.some(tab => tab.id === group.dataset.terminalTab)) group.remove();
    });
    for (const tab of state.tabs) {
      let group = stage.querySelector(`[data-terminal-tab="${tab.id}"]`);
      if (!group) {
        group = window.document.createElement('div'); group.dataset.terminalTab = tab.id; stage.appendChild(group);
      }
      group.className = 'terminal-panes layout-' + tab.layout;
      group.style.setProperty('--pane-count', tab.paneIds.length);
      group.hidden = tab.id !== state.activeTabId;
      for (const id of tab.paneIds) {
        const pane = getPane(id);
        let node = group.querySelector(`[data-pane="${id}"]`);
        if (!node) { group.insertAdjacentHTML('beforeend', paneMarkup(pane, tab)); node = group.lastElementChild; }
        node.classList.toggle('active', tab.activePaneId === id);
        const close = node.querySelector('[data-action="terminal-close-pane"]');
        if (close) close.hidden = tab.paneIds.length === 1;
        else if (tab.paneIds.length > 1) node.querySelector('header>div:last-child').insertAdjacentHTML('beforeend', `<button data-action="terminal-close-pane" data-id="${id}" aria-label="关闭此分屏" title="关闭此分屏">×</button>`);
      }
    }
    // 初始 HTML 中的占位分屏交由上面的按标签容器替代，尚未挂载 xterm。
    stage.querySelectorAll('.terminal-panes:not([data-terminal-tab])').forEach(node => node.remove());
    mountTerminals(); fullscreenChanged();
  }
  function render() {
    inputRevision++;
    if (A.state.page === 'terminal') {
      if (shell?.isConnected) reconcile(); else A.render();
    }
  }
  function focusPane(id) {
    const tab = activeTab(); if (!tab?.paneIds.includes(id)) return;
    if (tab.activePaneId !== id) inputRevision++;
    tab.activePaneId = id;
    A.root.querySelectorAll?.('.terminal-pane').forEach(el => el.classList.toggle('active', el.dataset.pane === id));
  }
  A.pages.terminal = page;
  A.meta.terminal = ['Linux 终端', 'WEB TERMINAL', '以资产为入口，统一管理多主机 SSH 会话；文件传输走 Go SFTP 接口。'];
  const navIndex = A.nav.findIndex(n => n[1] === 'hosts');
  if (navIndex >= 0 && !A.nav.some(n => n[1] === 'terminal')) A.nav.splice(navIndex + 1, 0, ['', 'terminal', 'Linux 终端', 'monitor', '']);
  A.actions['infra-remote'] = button => {
    const host = (A.infrastructure?.getHosts?.() || []).find(h => h.id === button.dataset.host);
    if (host?.type === 'Linux') {
      const registered = linuxHosts().find(item => item.ip === host.ip);
      A.closeModal(); A.navigate('terminal');
      if (registered) openHost(registered.id); else A.toast('该主机尚未登记到真实资产库，请先在 Linux 终端中添加并核验指纹。');
      return;
    }
    A.toast('未配置真实 Windows RDP 网关，连接已拒绝。');
  };
  Object.assign(A.actions, {
    'terminal-copy': copySelection,
    'terminal-paste': pasteClipboard,
    'terminal-fullscreen': toggleFullscreen,
    'terminal-add-asset': assetForm,
    'terminal-reload-assets': async () => {
      try { await api.refreshAssets(); A.toast('资产清单已重新载入。'); }
      catch (error) { A.toast('仍无法载入资产清单：' + (error.message || '未知错误')); }
      render();
    },
    'terminal-save-asset': saveAsset,
    'terminal-auth-type': switchAuthType,
    'terminal-toggle-password': togglePassword,
    'terminal-approve-host-key': approveHostKey,
    'terminal-diagnose': el => diagnoseAsset(el.dataset.host),
    'terminal-edit-host': el => A.assetEditor ? A.assetEditor(el.dataset.host) : A.toast('资产编辑入口未加载。'),
    'terminal-copy-key': copyPublicKey,
    'terminal-edit-credential': el => {
      const asset = api.getAssets().find(item => item.id === el.dataset.id) || {};
      credentialForm({ id: el.dataset.id, name: el.dataset.name || asset.name || '', username: asset.username, authType: asset.authType });
    },
    'terminal-save-credential': saveCredential,
    'terminal-verify': el => verifyHost(el.dataset.host),
    'terminal-delete-asset': el => deleteAsset(el.dataset.host),
    'terminal-open': el => { if (!fingerprintReady(getHost(el.dataset.host))) return A.toast('资产尚未通过 SSH 指纹核验，或当前不可连接。'); openHost(el.dataset.host); },
    'terminal-open-new': el => { if (!fingerprintReady(getHost(el.dataset.host))) return A.toast('资产尚未通过 SSH 指纹核验，或当前不可连接。'); openHost(el.dataset.host, 'new'); },
    'terminal-open-split': el => { if (!fingerprintReady(getHost(el.dataset.host))) return A.toast('资产尚未通过 SSH 指纹核验，不能加入分屏。'); openHost(el.dataset.host, 'split'); },
    'terminal-tab': el => { state.activeTabId = el.dataset.id; render(); }, 'terminal-close-tab': el => closeTab(el.dataset.id),
    'terminal-split': el => splitActive(el.dataset.layout), 'terminal-close-pane': el => closePane(el.dataset.id),
    'terminal-focus-pane': el => focusPane(el.dataset.id),
    'terminal-group': el => { state.groupBy = el.dataset.id; render(); }, 'terminal-sync': el => { const tab = activeTab(); if (tab) { inputRevision++; tab.sync = !!el.checked; } }
  });
  if (A.root?.addEventListener) {
    A.root.addEventListener('change', async event => {
      const input = event.target;
      if (input?.type === 'file' && /keyfile$/.test(input.id || '')) await loadKeyFile(input);
      if (input?.id === 'ta-secret' || input?.id === 'tc-secret') { /* typed content is validated on submit */ }
    });
    A.root.addEventListener('input', event => {
      if (event.target.id === 'terminal-search') { state.query = event.target.value; render(); const input = A.root.querySelector('#terminal-search'); input?.focus({ preventScroll: true }); input?.setSelectionRange(state.query.length, state.query.length); }
      if (event.target.id === 'ta-secret' || event.target.id === 'tc-secret') {
        const status = A.root.querySelector('#' + event.target.id.replace('secret', 'key-status'));
        if (status && event.target.value.trim()) { const detected = detectPrivateKey(event.target.value); status.textContent = detected.message; status.dataset.state = detected.ok ? 'ok' : 'error'; }
      }
    });
  }
  api?.onAssets?.(() => render());
  // 载入失败同样要落到界面上，否则用户只看到一棵空树。
  api?.onAssetsError?.(() => render());
  A.terminalWorkspace = {
    openHost, runCommand, renderPage,
    snapshot: () => ({ activeTabId: state.activeTabId, tabs: state.tabs.map(t => ({ ...t, paneIds: [...t.paneIds] })), panes: Object.fromEntries(Object.entries(state.panes).map(([id, p]) => [id, { ...p, handle: undefined, terminal: undefined, fitAddon: undefined, resizeObserver: undefined, element: undefined }])) })
  };
})();
