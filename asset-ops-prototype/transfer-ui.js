(function () {
  'use strict';
  const A = window.AssetDemo;
  if (!A) return;

  // Matches the server-side MaxChunk exactly: every extra byte over the cap is
  // a rejected request, and every byte below it is another HTTP round trip plus
  // another task-state write. 8 MiB halves the round trips of the old 4 MiB.
  const CHUNK = 8 << 20;
  const token = () => { try { return window.sessionStorage.getItem('qiyun.admin-token') || ''; } catch { return ''; } };
  const auth = extra => ({ ...(token() ? { Authorization: 'Bearer ' + token() } : {}), ...(extra || {}) });

  async function api(path, options) {
    const response = await fetch(path, { ...(options || {}), headers: auth({ 'Content-Type': 'application/json', ...(options?.headers || {}) }) });
    const body = response.status === 204 ? null : await response.json().catch(() => null);
    if (!response.ok) {
      const error = new Error(body?.detail || `HTTP ${response.status}`);
      error.code = body?.code || 'HTTP_ERROR';
      error.status = response.status;
      throw error;
    }
    return body;
  }

  let drawer = null, assets = [], tasks = [], mode = 'upload', busy = false;
  const progress = new Map();
  const canceledTasks = new Set();

  function el(html) {
    const template = document.createElement('template');
    template.innerHTML = html.trim();
    return template.content.firstElementChild;
  }

  function fmtBytes(n) {
    if (!Number.isFinite(n) || n <= 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let value = n, unit = 0;
    while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit += 1; }
    return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
  }

  function statusLabel(task) {
    if (progress.has(task.id)) return '传输中';
    return { pending: '待开始', active: '传输中', paused: '已暂停（可续传）', completed: '已完成', failed: '失败', canceled: '已取消' }[task.status] || task.status;
  }

  // A rate window rather than a running average: the first seconds of a transfer
  // include connection setup, and a long window would keep under-reporting a
  // link that has since sped up.
  function noteProgress(id, offset, size) {
    const now = Date.now();
    const entry = progress.get(id) || { offset: 0, size, samples: [] };
    entry.offset = offset;
    entry.size = size;
    entry.samples = entry.samples || [];
    entry.samples.push({ t: now, offset });
    while (entry.samples.length > 2 && now - entry.samples[0].t > 5000) entry.samples.shift();
    const first = entry.samples[0], last = entry.samples[entry.samples.length - 1];
    const seconds = (last.t - first.t) / 1000;
    entry.rate = seconds > 0 ? Math.max(0, (last.offset - first.offset) / seconds) : 0;
    entry.eta = entry.rate > 0 ? (size - offset) / entry.rate : 0;
    progress.set(id, entry);
    return entry;
  }

  // Saying "还有 3 秒" is the difference between "很慢" and "正常" when the link
  // itself is the limit, which it usually is: an 80 Mbit/s VPN moves ~9.5 MB/s.
  function fmtEta(seconds) {
    if (!Number.isFinite(seconds) || seconds <= 0) return '';
    if (seconds < 1) return '不到 1 秒';
    if (seconds < 60) return Math.ceil(seconds) + ' 秒';
    const m = Math.floor(seconds / 60), s = Math.round(seconds % 60);
    return m + ' 分 ' + s + ' 秒';
  }

  function hostOptions(selectedId) {
    const usable = assets.filter(item => item.host_key_status === 'verified');
    if (!usable.length) return '<option value="">暂无已核验指纹的主机</option>';
    return usable.map(item => `<option value="${item.id}" ${item.id === selectedId ? 'selected' : ''}>${(item.name || item.address)} · ${item.address}</option>`).join('');
  }

  function taskRow(task) {
    const live = progress.get(task.id);
    const sent = live?.offset ?? task.offset;
    const percent = task.size > 0 ? Math.min(100, Math.round((sent / task.size) * 100)) : (task.status === 'completed' ? 100 : 0);
    const running = !!live;
    const direction = task.direction === 'upload' ? '上传' : '下载';
    const detail = task.error ? `<p class="transfer-error">${escapeHtml(task.error)}</p>` : '';
    // While an upload runs the bytes live in a staging file; without saying so,
    // checking the remote directory looks like the upload went nowhere.
    const staging = task.direction === 'upload' && task.status !== 'completed' && task.temp_path
      ? `<div class="transfer-meta"><span>远端临时文件</span><span class="mono">${escapeHtml(task.temp_path)}</span></div>`
      : '';
    const actions = running
      ? `<button type="button" data-op="cancel" data-id="${task.id}">停止</button>`
      : `${task.status === 'completed' || task.status === 'failed' ? `<button type="button" data-op="remove" data-id="${task.id}">移除</button>` : ''}${task.status === 'paused' || task.status === 'active' || task.status === 'pending' ? `<button type="button" data-op="cancel" data-id="${task.id}">取消</button>` : ''}${task.direction === 'download' && task.status !== 'completed' ? `<button type="button" data-op="resume-download" data-id="${task.id}">继续下载</button>` : ''}`;
    const hostLine = task.asset_id ? `<div class="transfer-meta"><span>目标主机</span><span>${escapeHtml(assetLabel(task.asset_id))}</span></div>` : '';
    return `<li class="transfer-task" data-state="${task.status}">
      <div class="transfer-task-head"><strong>${direction} ${escapeHtml(task.file_name)}</strong><span>${statusLabel(task)}</span></div>
      ${hostLine}
      <div class="transfer-bar" role="progressbar" aria-valuenow="${percent}" aria-valuemin="0" aria-valuemax="100"><i style="width:${percent}%"></i></div>
      <div class="transfer-meta"><span>${fmtBytes(sent)} / ${fmtBytes(task.size)} · ${percent}%</span><span class="mono">${escapeHtml(task.remote_path)}</span></div>
      ${live && live.rate ? `<div class="transfer-meta transfer-rate"><span>${fmtBytes(live.rate)}/s</span><span>${live.eta ? '剩余 ' + fmtEta(live.eta) : ''}</span></div>` : ''}
      ${staging}
      ${detail}
      <div class="transfer-task-actions">${actions}</div>
    </li>`;
  }

  function escapeHtml(value) {
    return String(value ?? '').replace(/[&<>"']/g, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
  }

  function build() {
    drawer = el(`<aside class="transfer-drawer" hidden aria-label="文件传输">
      <header><h3>文件传输</h3><button type="button" data-op="close" aria-label="关闭">×</button></header>
      <div class="transfer-body">
        <div class="transfer-seg" role="tablist">
          <button type="button" role="tab" data-op="mode" data-mode="upload" aria-selected="true">上传</button>
          <button type="button" role="tab" data-op="mode" data-mode="download" aria-selected="false">下载</button>
        </div>
        <label class="transfer-field"><span>目标主机</span><select data-field="asset"></select></label>
        <p class="transfer-target-note" data-target-note></p>
        <label class="transfer-field"><span>远端路径</span><input data-field="path" placeholder="/tmp/example.bin" spellcheck="false"></label>
        <div class="transfer-upload-only">
          <label class="transfer-field"><span>本地文件</span><input type="file" data-field="file"></label>
          <label class="transfer-check"><input type="checkbox" data-field="overwrite"><span>允许覆盖远端同名文件</span></label>
        </div>
        <button type="button" class="transfer-start" data-op="start">开始上传</button>
        <p class="transfer-hint" data-hint></p>
        <div class="transfer-list-head"><span>任务</span><button type="button" data-op="refresh">刷新</button></div>
        <ul class="transfer-list" data-list></ul>
      </div>
    </aside>`);
    document.body.appendChild(drawer);
    drawer.addEventListener('click', onClick);
    drawer.addEventListener('change', onChange);
    syncDrawerPlacement();
  }

  // A drawer inside <body> is invisible while the terminal stage is fullscreen,
  // so it follows the current fullscreen element instead of exiting fullscreen.
  function syncDrawerPlacement() {
    if (!drawer) return;
    const host = document.fullscreenElement && document.querySelector('.terminal-stage') === document.fullscreenElement
      ? document.fullscreenElement
      : document.body;
    if (drawer.parentElement !== host) host.appendChild(drawer);
  }
  document.addEventListener('fullscreenchange', syncDrawerPlacement);

  function query(selector) { return drawer?.querySelector(selector); }

  // 抽屉以前不认当前终端，默认选中资产列表第一台：在 A 机终端里点“文件传输”，
  // 实际发往了 B 机，报的却是“主机不可达”，看上去像 SSH 明明通了却不给传。
  function activeHostId() {
    const snap = A.terminalWorkspace?.snapshot?.();
    if (!snap) return '';
    const tabs = snap.tabs || [];
    const tab = tabs.find(item => item.id === snap.activeTabId) || tabs[0];
    if (!tab) return '';
    const pane = snap.panes?.[tab.activePaneId] || snap.panes?.[tab.paneIds?.[0]];
    return pane?.hostId || tab.hostId || '';
  }
  let pinnedHost = '';

  function assetLabel(id) {
    const asset = assets.find(item => item.id === id);
    if (!asset) return id;
    return (asset.name || asset.address) + ' · ' + asset.address + (asset.ssh_port && asset.ssh_port !== 22 ? ':' + asset.ssh_port : '');
  }

  function render() {
    if (!drawer) return;
    const assetSelect = query('[data-field="asset"]');
    const keep = pinnedHost || assetSelect.value;
    assetSelect.innerHTML = hostOptions(keep);
    const chose = assetSelect.value;
    const connected = activeHostId();
    const note = query('[data-target-note]');
    if (note) {
      note.textContent = !connected ? ''
        : chose === connected ? `与终端一致：${assetLabel(connected)}`
        : `注意：当前终端连接的是 ${assetLabel(connected)}，这里的操作会发往 ${chose ? assetLabel(chose) : '所选主机'}。`;
      note.dataset.mismatch = String(!!connected && chose !== connected);
    }
    query('.transfer-upload-only').hidden = mode !== 'upload';
    query('[data-op="start"]').textContent = mode === 'upload' ? '开始上传' : '开始下载';
    drawer.querySelectorAll('[data-op="mode"]').forEach(button => button.setAttribute('aria-selected', String(button.dataset.mode === mode)));
    query('[data-hint]').textContent = mode === 'upload'
      ? `分块上传，单块 ${fmtBytes(CHUNK)}，失败自动重试，实时速率与剩余时间见任务行。远端只出现 .part 临时文件，全部完成才改名为目标文件；中断后重新选择同一文件即可续传。速率长时间稳定在同一条水平线上，通常是链路带宽上限，不是程序在丢包。`
      : '下载使用浏览器流式写盘；不支持流式写入的浏览器会改为浏览器直接下载，且不记录断点。';
    query('[data-list]').innerHTML = tasks.length
      ? tasks.map(taskRow).join('')
      : '<li class="transfer-empty">暂无传输任务</li>';
  }

  // 任务列表来自服务端，刚建好的任务还没有行。不补进去的话，进度条要等整段
  // 传输结束才出现，看上去就像点完之后什么都没发生。
  function trackTask(task) {
    if (!task || !task.id) return;
    const index = tasks.findIndex(item => item.id === task.id);
    if (index >= 0) tasks[index] = { ...tasks[index], ...task };
    else tasks = [task, ...tasks];
  }

  async function refreshTasks() {
    try {
      const data = await api('/api/v1/transfers');
      tasks = data.items || [];
      render();
    } catch (error) {
      query('[data-list]') && (query('[data-list]').innerHTML = `<li class="transfer-empty">读取任务失败：${escapeHtml(error.message)}</li>`);
    }
  }

  async function open(hostId) {
    if (!drawer) build();
    drawer.hidden = false;
    syncDrawerPlacement();
    pinnedHost = hostId || activeHostId() || '';
    try {
      const data = await api('/api/v1/assets');
      assets = data.items || [];
    } catch (error) {
      A.toast?.('读取资产失败：' + error.message);
    }
    render();
    pinnedHost = '';
    await refreshTasks();
  }

  function close() { if (drawer) drawer.hidden = true; }

  A.actions['terminal-transfer'] = () => open(activeHostId());
  A.transferPanel = { open, close, refresh: refreshTasks };

  function onClick(event) {
    const button = event.target.closest('[data-op]');
    if (!button) return;
    const op = button.dataset.op;
    if (op === 'close') return close();
    if (op === 'mode') { mode = button.dataset.mode; return render(); }
    if (op === 'refresh') return refreshTasks();
    if (op === 'start') return mode === 'upload' ? startUpload() : startDownload();
    if (op === 'cancel') return cancelTask(button.dataset.id);
    if (op === 'remove') return removeTask(button.dataset.id);
    if (op === 'resume-download') return resumeDownloadById(button.dataset.id);
  }

  function onChange(event) {
    if (event.target.dataset.field === 'file' && event.target.files?.[0]) {
      // Prefill the remote path with the chosen file name so the common case
      // needs only one edit (the directory).
      const pathInput = query('[data-field="path"]');
      if (!pathInput.value.trim()) pathInput.value = '/tmp/' + event.target.files[0].name;
    }
  }

  async function cancelTask(id) {
    try {
      canceledTasks.add(id);
      if (activeDownload?.taskId === id) activeDownload.abort = true;
      await api(`/api/v1/transfers/${id}`, { method: 'DELETE' });
      progress.delete(id);
      await refreshTasks();
    } catch (error) { A.toast?.('取消失败：' + error.message); }
  }

  async function removeTask(id) {
    try {
      await api(`/api/v1/transfers/${id}`, { method: 'DELETE' });
      await refreshTasks();
    } catch (error) { A.toast?.('移除失败：' + error.message); }
  }

  function selectedAsset() {
    const id = query('[data-field="asset"]').value;
    if (!id) { A.toast?.('请先选择一台已核验指纹的主机。'); return null; }
    return id;
  }

  async function startUpload() {
    if (busy) return A.toast?.('已有上传正在进行。');
    const assetId = selectedAsset(); if (!assetId) return;
    const remotePath = query('[data-field="path"]').value.trim();
    const file = query('[data-field="file"]').files?.[0];
    if (!file) return A.toast?.('请选择要上传的本地文件。');
    if (!remotePath.startsWith('/') || remotePath.endsWith('/') || remotePath.split('/').includes('..')) {
      return A.toast?.('远端路径必须是绝对的文件路径，且不能包含 ..');
    }
    busy = true;
    query('[data-op="start"]').disabled = true;
    try {
      const existing = (await api('/api/v1/transfers?asset_id=' + encodeURIComponent(assetId))).items || [];
      // 失败的任务同样可以续传：服务端只在 offset 处接续，且会把临时文件回退到
      // 该偏移。之前把 failed 排除在外，于是提示说「可续传」却只能从头再来。
      let task = existing.find(item => item.direction === 'upload' && item.remote_path === remotePath && item.size === file.size
        && !['completed', 'canceled'].includes(item.status));
      if (!task) {
        task = await api('/api/v1/transfers', {
          method: 'POST',
          body: JSON.stringify({
            asset_id: assetId, direction: 'upload', remote_path: remotePath,
            file_name: file.name, size: file.size, overwrite: query('[data-field="overwrite"]').checked,
          }),
        });
      }
      let offset = task.offset || 0;
      if (offset > 0 && offset < task.size) {
        A.toast?.(`从 ${fmtBytes(offset)} 处继续上传。`);
      }
      noteProgress(task.id, offset, task.size);
      trackTask(task);
      activeTaskId = task.id;
      const startedAt = Date.now();
      render();
      // The next slice is read while the current one is in flight, so the file
      // read and the request never wait on each other. Ordering is unchanged:
      // exactly one chunk is outstanding and the server still owns the offset.
      let pending = offset < task.size ? sliceChunk(file, offset, task.size) : null;
      while (offset < task.size) {
        if (canceledTasks.has(task.id)) throw new Error('已取消');
        const buffer = await pending;
        const from = offset;
        const following = Math.min(from + CHUNK, task.size);
        pending = following < task.size ? sliceChunk(file, following, task.size) : null;
        const result = await sendChunk(task.id, buffer, from);
        if (result.resync !== undefined) {
          // The server confirmed a different offset (e.g. a chunk landed while a
          // response was lost); trust the server and re-slice from there.
          offset = result.resync;
          pending = offset < task.size ? sliceChunk(file, offset, task.size) : null;
        } else {
          offset = result.offset;
        }
        noteProgress(task.id, offset, task.size);
        render();
      }
      const elapsed = (Date.now() - startedAt) / 1000;
      // A transfer whose bytes all landed but whose publish failed comes back
      // with offset == size and never enters the loop above, so ask the server to
      // publish once more. This call is idempotent for an already-finished task.
      await api(`/api/v1/transfers/${task.id}/complete`, { method: 'POST' });
      progress.delete(task.id);
      await refreshTasks();
      const avg = elapsed > 0.5 ? ` · 平均 ${fmtBytes(task.size / elapsed)}/s` : '';
      A.toast?.(`上传完成：${remotePath}${avg}`);
    } catch (error) {
      progress.delete(activeTaskId);
      await refreshTasks().catch(() => {});
      A.toast?.('上传中断：' + error.message + '（任务已保留：重新选择同一文件再点开始，会从断点继续）');
    } finally {
      busy = false;
      query('[data-op="start"]').disabled = false;
    }
  }

  const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

  function sliceChunk(file, offset, total) {
    return file.slice(offset, Math.min(offset + CHUNK, total)).arrayBuffer();
  }

  // A single lost chunk should not abort a long upload: the server already
  // reconnects once, and this retries the request a couple more times before
  // surfacing the error. Offset mismatches resync instead of failing.
  async function sendChunk(taskId, buffer, offset) {
    let lastError = null;
    for (let attempt = 0; attempt < 3; attempt += 1) {
      if (canceledTasks.has(taskId)) throw new Error('已取消');
      try {
        const response = await fetch(`/api/v1/transfers/${taskId}/chunk?offset=${offset}`, {
          method: 'PUT',
          headers: auth({ 'Content-Type': 'application/octet-stream' }),
          body: buffer,
        });
        const body = await response.json().catch(() => null);
        if (response.ok) return { offset: body.offset };
        if (response.status === 409 && body?.code === 'OFFSET_MISMATCH') {
          const fresh = await api(`/api/v1/transfers/${taskId}`);
          return { resync: fresh.offset };
        }
        // 4xx other than a resync will not succeed on retry.
        if (response.status < 500 && response.status !== 429) throw new Error(body?.detail || `HTTP ${response.status}`);
        lastError = new Error(body?.detail || `HTTP ${response.status}`);
      } catch (error) {
        if (error.status && error.status >= 400 && error.status < 500) throw error;
        lastError = error;
      }
      await sleep(500 * (attempt + 1));
    }
    throw lastError || new Error('分块上传失败');
  }

  let activeTaskId = '';
  let activeDownload = null;

  async function startDownload() {
    if (busy) return A.toast?.('已有传输正在进行。');
    const assetId = selectedAsset(); if (!assetId) return;
    const remotePath = query('[data-field="path"]').value.trim();
    if (!remotePath.startsWith('/') || remotePath.endsWith('/')) return A.toast?.('远端路径必须是绝对的文件路径。');
    busy = true;
    query('[data-op="start"]').disabled = true;
    try {
      const task = await api('/api/v1/transfers', {
        method: 'POST',
        body: JSON.stringify({ asset_id: assetId, direction: 'download', remote_path: remotePath }),
      });
      await streamDownload(task);
    } catch (error) {
      A.toast?.('下载失败：' + error.message);
      await refreshTasks().catch(() => {});
    } finally {
      busy = false;
      query('[data-op="start"]').disabled = false;
    }
  }

  async function resumeDownloadById(id) {
    if (busy) return A.toast?.('已有传输正在进行。');
    try {
      const task = await api(`/api/v1/transfers/${id}`);
      busy = true;
      await streamDownload(task);
    } catch (error) {
      A.toast?.('继续下载失败：' + error.message);
    } finally {
      busy = false;
    }
  }

  async function streamDownload(task) {
    activeTaskId = task.id;
    trackTask(task);
    const start = task.offset || 0;
    if (!window.showSaveFilePicker) {
      // Without the File System Access API a large download cannot be streamed
      // to disk with a recorded offset, so hand it to the browser directly
      // instead of buffering it in memory.
      const url = `/api/v1/transfers/${task.id}/content${token() ? '?access_token=' + encodeURIComponent(token()) : ''}`;
      const link = document.createElement('a');
      link.href = url; link.download = task.file_name;
      document.body.appendChild(link); link.click(); link.remove();
      A.toast?.('当前浏览器不支持流式写盘，已交给浏览器直接下载；此方式不记录断点。');
      await refreshTasks();
      return;
    }
    let handle;
    if (start > 0 && window.showOpenFilePicker) {
      A.toast?.(`该任务已下载 ${fmtBytes(start)}，请选择同一个本地文件继续写入。`);
      const picked = await window.showOpenFilePicker({ multiple: false });
      handle = picked[0];
      const existing = await handle.getFile();
      if (existing.size < start) throw new Error('所选文件长度小于已确认断点，无法安全续传');
    } else {
      handle = await window.showSaveFilePicker({ suggestedName: task.file_name });
    }
    const writable = await handle.createWritable({ keepExistingData: start > 0 });
    const controller = new AbortController();
    activeDownload = { taskId: task.id, abort: false };
    let offset = start;
    // A download delivers many small buffers per second; re-rendering the whole
    // task list on every one of them costs more than the write itself.
    let lastPaint = 0;
    const paint = force => {
      const now = Date.now();
      if (!force && now - lastPaint < 250) return;
      lastPaint = now;
      render();
    };
    noteProgress(task.id, offset, task.size);
    paint(true);
    try {
      const response = await fetch(`/api/v1/transfers/${task.id}/content`, {
        headers: auth({ Range: `bytes=${offset}-` }),
        signal: controller.signal,
      });
      if (!response.ok && response.status !== 206) throw new Error(`HTTP ${response.status}`);
      const reader = response.body.getReader();
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        if (activeDownload?.abort) { controller.abort(); throw new Error('已取消'); }
        await writable.write({ type: 'write', position: offset, data: value });
        offset += value.byteLength;
        noteProgress(task.id, offset, task.size);
        paint(false);
      }
      await writable.close();
      if (offset !== task.size) throw new Error(`下载长度 ${offset} 与远端大小 ${task.size} 不一致`);
      await api(`/api/v1/transfers/${task.id}/complete`, { method: 'POST' });
      A.toast?.(`下载完成：${task.file_name}`);
    } catch (error) {
      try { await writable.abort(); } catch {}
      A.toast?.('下载中断：' + error.message + '（任务保留，可续传）');
    } finally {
      progress.delete(task.id);
      activeDownload = null;
      await refreshTasks().catch(() => {});
    }
  }
})();
