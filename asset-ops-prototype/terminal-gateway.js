(function () {
  'use strict';
  const encoder = new TextEncoder();
  const decoder = new TextDecoder();
  const listeners = new Set();
  let assets = [];
  const tokenStore = window.sessionStorage;
  const token = () => tokenStore.getItem('qiyun.admin-token') || '';
  const headers = extra => ({ 'Content-Type': 'application/json', ...(token() ? { Authorization: 'Bearer ' + token() } : {}), ...(extra || {}) });
  async function request(path, options = {}, retried = false) {
    const response = await fetch(path, { ...options, headers: headers(options.headers) });
    const body = response.status === 204 ? null : await response.json().catch(() => null);
    if (response.status === 401 && !retried && !window.QiyunAuth?.state?.authEnabled && typeof window.prompt === 'function') {
      const value = window.prompt('请输入栖云管理令牌');
      if (value) { tokenStore.setItem('qiyun.admin-token', value); return request(path, options, true); }
    }
    if (!response.ok) {
      const error = new Error(body?.detail || `HTTP ${response.status}`);
      error.code = body?.code || 'HTTP_ERROR'; error.status = response.status;
      error.stage = body?.stage || ''; error.hint = body?.hint || '';
      throw error;
    }
    return body;
  }
  // Windows hosts are managed with RDP, not SSH. Keep the classification in one
  // place so the asset table, remote dialog and terminal tree agree.
  function isWindowsAsset(a) {
    return String(a?.os_family || '').toLowerCase() === 'windows' ||
      String(a?.asset_type || '').toLowerCase() === 'windows_host' ||
      /windows/i.test(String(a?.os_name || ''));
  }
  function mapAsset(a) {
    const windows = isWindowsAsset(a);
    return {
      id: a.id, assetCode: a.asset_code, name: a.name, type: windows ? 'Windows' : 'Linux',
      os: a.os_name || (windows ? 'Windows' : 'Linux'), ip: a.address,
      sshPort: a.ssh_port, rdpPort: a.rdp_port || 3389, osFamily: windows ? 'windows' : (a.os_family || 'linux'),
      owner: a.owner || '未指定', department: a.department || '未分部门', environment: a.environment || '未分环境',
      status: windows ? '待连接' : (a.host_key_status === 'verified' ? (a.status === 'offline' ? '离线' : '在线') : '待核验'),
      hostKeyStatus: a.host_key_status, hostKeyFingerprint: a.host_key_fingerprint || '', credentialId: a.credential_id,
      username: a.username || '', authType: a.auth_type || 'password',
      assetType: a.asset_type || (windows ? 'windows_host' : 'linux_host'), serial_number: a.serial_number || '', mac_address: a.mac_address || '',
      location: a.location || '', price_cents: a.price_cents || 0, purchased_at: a.purchased_at || '',
      warranty_until: a.warranty_until || '', supplier: a.supplier || '', purchase_order: a.purchase_order || '',
      updated_at: a.updated_at || '', created_at: a.created_at || ''
    };
  }
  // 资产清单的一次性拉取曾经在失败时只写一条 console.warn：界面会把“没拉到”
  // 当成“台账为空”，用户看到的是资产消失、没有入口，而服务端数据其实完好。
  let assetsLoaded = false, assetsError = null, retryTimer = 0, retryDelay = 2000, retryToken = 0;
  const errorListeners = new Set();
  function scheduleAssetRetry() {
    const token = ++retryToken;
    retryTimer = setTimeout(() => {
      // 令牌变化说明期间已成功载入，这次排队直接作废，避免无谓的重复请求。
      if (token !== retryToken) return;
      retryTimer = 0;
      refreshAssets().catch(() => {});
    }, retryDelay);
    retryDelay = Math.min(retryDelay * 2, 30000);
  }
  async function refreshAssets() {
    let data;
    try {
      data = await request('/api/v1/assets');
    } catch (error) {
      assetsError = error;
      errorListeners.forEach(fn => { try { fn(error); } catch {} });
      // 只在从未成功载入时自动重试；已有清单时交给用户手动刷新，避免打扰。
      if (!assetsLoaded) scheduleAssetRetry();
      throw error;
    }
    assets = (data.items || []).map(mapAsset);
    assetsLoaded = true; assetsError = null; retryDelay = 2000;
    retryToken++; retryTimer = 0;
    listeners.forEach(fn => fn(getAssets()));
    return getAssets();
  }
  function getAssets() { return assets.map(a => ({ ...a })); }
  function assetStatus() { return { loaded: assetsLoaded, error: assetsError, retrying: !!retryTimer }; }
  function onAssets(fn) { listeners.add(fn); return () => listeners.delete(fn); }
  function onAssetsError(fn) { errorListeners.add(fn); return () => errorListeners.delete(fn); }
  function websocketURL(path, ticket) {
    const url = new URL(path, location.href); url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    url.searchParams.set('ticket', ticket); return url.href;
  }
  const gateway = {
    connect(options) {
      let ws = null, closed = false, queued = [], currentStatus = 'connecting';
      const setStatus = (value, detail) => { currentStatus = value; options.onStatus?.(value, detail); };
      setStatus('connecting');
      (async () => {
        try {
          const session = await request('/api/v1/terminal-sessions', { method: 'POST', body: JSON.stringify({ asset_id: options.hostId, cols: options.cols || 120, rows: options.rows || 32 }) });
          if (closed) return;
          ws = new WebSocket(websocketURL(session.ws_url, session.ws_ticket)); ws.binaryType = 'arraybuffer';
          ws.onopen = () => { queued.splice(0).forEach(data => ws.send(data)); };
          ws.onmessage = event => {
            if (typeof event.data === 'string') {
              let message; try { message = JSON.parse(event.data); } catch { options.onData?.(event.data); return; }
              if (message.type === 'ready') {
                options.onData?.('\r\n[传输限制] 下载要求支持 File System Access API 的浏览器及 HTTPS/localhost；不支持时拒绝下载，不使用整文件 Blob。会话最长 8 小时；未承诺 1000G 稳定性或断点续传。\r\n');
                setStatus('connected'); return;
              }
              if (message.type === 'status') { setStatus(message.state); return; }
              if (message.type === 'error') { options.onData?.(`\r\n[${message.code || 'SSH_ERROR'}] ${message.message || '远程连接失败'}\r\n`); queued = []; closed = true; setStatus('error', { code: message.code, message: message.message }); return; }
              if (message.type === 'exit') { options.onData?.(`\r\n远程会话已结束，退出码 ${message.exit_code ?? '未知'}。\r\n`); setStatus('offline'); return; }
              return;
            }
            if (event.data instanceof ArrayBuffer) options.onData?.(decoder.decode(new Uint8Array(event.data), { stream: true }));
          };
          ws.onerror = () => { queued = []; closed = true; options.onData?.('\r\nSSH WebSocket 连接失败。\r\n'); setStatus('error'); };
          ws.onclose = () => { queued = []; const wasClosed = closed; closed = true; if (!wasClosed && currentStatus !== 'error') setStatus('offline'); };
        } catch (error) { queued = []; closed = true; options.onData?.(`\r\n[${error.code || 'SSH_CONNECT_FAILED'}] ${error.message}\r\n`); setStatus('error'); }
      })();
      function send(data) {
        if (closed) return;
        const size = typeof data === 'string' ? encoder.encode(data).byteLength : data.byteLength;
        const pending = queued.reduce((n, value) => n + (typeof value === 'string' ? encoder.encode(value).byteLength : value.byteLength), 0);
        if ((ws?.bufferedAmount || 0) + pending + size > 32 * 1024 * 1024) {
          closed = true; queued = []; ws?.close(1000, 'send buffer limit');
          options.onData?.('\r\n[传输中止] 网络发送积压超过 32 MiB，已关闭连接以限制内存；请检查网络后重连。\r\n');
          setStatus('error'); return;
        }
        if (ws?.readyState === WebSocket.OPEN) ws.send(data); else queued.push(data);
      }
      return {
        write(data) {
          const bytes = typeof data === 'string' ? encoder.encode(data) : data instanceof ArrayBuffer ? new Uint8Array(data) : data;
          for (let offset = 0; offset < bytes.byteLength; offset += 65536) send(bytes.slice(offset, offset + 65536));
        },
        resize(cols, rows) { send(JSON.stringify({ type: 'resize', cols, rows })); },
        close() { closed = true; queued = []; if (ws && ws.readyState < WebSocket.CLOSING) ws.close(1000, 'client close'); }
      };
    }
  };
  let connectorBase = 'http://127.0.0.1:8766';
  const connectorHeaders = { 'X-Qiyun-Connector': '1' };
  async function connectorRequest(path, options = {}, timeout = 20000) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeout);
    try {
      const response = await fetch(connectorBase + path, {
        ...options,
        cache: 'no-store',
        signal: controller.signal,
        headers: { ...connectorHeaders, ...(options.headers || {}) }
      });
      const body = await response.json().catch(() => null);
      if (!response.ok) {
        const error = new Error(body?.detail || `连接器返回 ${response.status}`);
        error.code = body?.code || 'CONNECTOR_ERROR';
        error.status = response.status;
        throw error;
      }
      return body;
    } finally {
      clearTimeout(timer);
    }
  }
  async function connectorHealth() {
    try {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), 4000);
      try {
        const response = await fetch(connectorBase + '/healthz', {
          cache: 'no-store', signal: controller.signal, headers: connectorHeaders
        });
        if (!response.ok) return null;
        return await response.json();
      } finally {
        clearTimeout(timer);
      }
    } catch {
      return null;
    }
  }
  window.LinuxTerminalGateway = gateway;
  window.QiyunAPI = {
    request, refreshAssets, getAssets, assetStatus, onAssets, onAssetsError, mapAsset, isWindowsAsset,
    createAsset: body => request('/api/v1/assets', { method: 'POST', body: JSON.stringify(body) }),
    getAsset: id => request('/api/v1/assets/' + encodeURIComponent(id)),
    updateAsset: (id, patch) => request('/api/v1/assets/' + encodeURIComponent(id), { method: 'PATCH', body: JSON.stringify(patch) }),
    deleteAsset: id => request('/api/v1/assets/' + encodeURIComponent(id), { method: 'DELETE' }),
    scanHostKey: id => request(`/api/v1/assets/${encodeURIComponent(id)}/host-key/scan`, { method: 'POST', body: '{}' }),
    approveHostKey: (id, scan) => request(`/api/v1/assets/${encodeURIComponent(id)}/host-key/approve`, { method: 'POST', body: JSON.stringify({ host_key: scan.host_key, fingerprint: scan.fingerprint }) }),
    testAsset: id => request(`/api/v1/assets/${encodeURIComponent(id)}/test`, { method: 'POST', body: '{}' }),
    diagnoseAsset: id => request(`/api/v1/assets/${encodeURIComponent(id)}/diagnose`, { method: 'POST', body: '{}' }),
    updateCredential: (id, body) => request(`/api/v1/assets/${encodeURIComponent(id)}/credential`, { method: 'PUT', body: JSON.stringify(body) }),
    // Agent inventory (Linux/PVE/Docker/Kubernetes) is read-only in the UI.
    agentHosts: params => {
      const query = new URLSearchParams();
      Object.entries(params || {}).forEach(([key, value]) => {
        if (value !== undefined && value !== null && String(value) !== '') query.set(key, String(value));
      });
      const suffix = query.toString() ? '?' + query.toString() : '';
      return request('/api/v1/agent/hosts' + suffix, { cache: 'no-store' });
    },
    agentHost: id => request('/api/v1/agent/hosts/' + encodeURIComponent(id), { cache: 'no-store' }),
    agentCreateHost: body => request('/api/v1/agent/hosts', { method: 'POST', body: JSON.stringify(body) }),
    agentUpdateHost: (id, body) => request('/api/v1/agent/hosts/' + encodeURIComponent(id), { method: 'PATCH', body: JSON.stringify(body) }),
    agentDeleteHost: id => request('/api/v1/agent/hosts/' + encodeURIComponent(id), { method: 'DELETE' }),
    agentBulkDeleteHosts: ids => request('/api/v1/agent/hosts/bulk-delete', { method: 'POST', body: JSON.stringify({ ids }) }),
    agentPurgeHosts: body => request('/api/v1/agent/hosts/purge', { method: 'POST', body: JSON.stringify(body) }),
    agentEnrollment: () => request('/api/v1/agent/enrollment', { cache: 'no-store' }),
    // RDP: the browser never sends a password. The Go service resolves the
    // asset's stored credential and hands it to the local mstsc launcher.
    rdpCapabilities: () => request('/api/v1/rdp/capabilities'),
    rdpSetupSigning: () => request('/api/v1/rdp/signing', { method: 'POST', body: '{}' }),
    rdpLaunch: (id, body) => request(`/api/v1/assets/${encodeURIComponent(id)}/rdp/launch`, { method: 'POST', body: JSON.stringify(body || {}) }),
    rdpTicket: (id, body) => request(`/api/v1/assets/${encodeURIComponent(id)}/rdp/ticket`, { method: 'POST', body: JSON.stringify(body || {}) }),
    webRdp: (id, body) => request(`/api/v1/assets/${encodeURIComponent(id)}/web-rdp`, { method: 'POST', body: JSON.stringify(body || {}) }),
    rdpConnectorInfo: () => request('/api/v1/rdp/connector'),
    connectorHealth,
    connectorSigning: () => connectorRequest('/signing', { method: 'POST' }, 150000),
    connectorLaunch: ticket => connectorRequest('/launch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ticket })
    }, 45000),
    setConnectorBase(value) { if (value) connectorBase = String(value).replace(/\/+$/, ''); },
    setToken(value) { if (value) tokenStore.setItem('qiyun.admin-token', value); else tokenStore.removeItem('qiyun.admin-token'); }
  };
  // 先宣告数据层就绪，再发起首次拉取：早于本文件执行的模块（如 infra.js）靠这个事件补订阅。
  if (typeof window.dispatchEvent === 'function' && typeof Event === 'function') window.dispatchEvent(new Event('qiyun:api-ready'));
  refreshAssets().catch(error => console.warn('Asset API unavailable, retrying:', error.message));
})();
