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
    if (response.status === 401 && !retried && typeof window.prompt === 'function') {
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
  function mapAsset(a) {
    return {
      id: a.id, assetCode: a.asset_code, name: a.name, type: 'Linux', os: a.os_name || 'Linux', ip: a.address,
      sshPort: a.ssh_port, owner: a.owner || '未指定', department: a.department || '未分部门', environment: a.environment || '未分环境',
      status: a.host_key_status === 'verified' ? (a.status === 'offline' ? '离线' : '在线') : '待核验',
      hostKeyStatus: a.host_key_status, hostKeyFingerprint: a.host_key_fingerprint || '', credentialId: a.credential_id,
      username: a.username || '', authType: a.auth_type || 'password',
      assetType: a.asset_type || 'linux_host', serial_number: a.serial_number || '', mac_address: a.mac_address || '',
      location: a.location || '', price_cents: a.price_cents || 0, purchased_at: a.purchased_at || '',
      warranty_until: a.warranty_until || '', supplier: a.supplier || '', purchase_order: a.purchase_order || '',
      updated_at: a.updated_at || '', created_at: a.created_at || ''
    };
  }
  async function refreshAssets() {
    const data = await request('/api/v1/assets'); assets = (data.items || []).map(mapAsset); listeners.forEach(fn => fn(getAssets())); return getAssets();
  }
  function getAssets() { return assets.map(a => ({ ...a })); }
  function onAssets(fn) { listeners.add(fn); return () => listeners.delete(fn); }
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
              if (message.type === 'ready') { setStatus('connected'); return; }
              if (message.type === 'status') { setStatus(message.state); return; }
              if (message.type === 'error') { options.onData?.(`\r\n[${message.code || 'SSH_ERROR'}] ${message.message || '远程连接失败'}\r\n`); queued = []; closed = true; setStatus('error', { code: message.code, message: message.message }); return; }
              if (message.type === 'exit') { options.onData?.(`\r\n远程会话已结束，退出码 ${message.exit_code ?? '未知'}。\r\n`); setStatus('offline'); return; }
              return;
            }
            if (event.data instanceof ArrayBuffer) {
              if (options.onServerOutput) options.onServerOutput(event.data);
              else options.onData?.(decoder.decode(new Uint8Array(event.data), { stream: true }));
            }
          };
          ws.onerror = () => { queued = []; closed = true; options.onData?.('\r\nSSH WebSocket 连接失败。\r\n'); setStatus('error'); };
          ws.onclose = () => { queued = []; const wasClosed = closed; closed = true; if (!wasClosed && currentStatus !== 'error') setStatus('offline'); };
        } catch (error) { queued = []; closed = true; options.onData?.(`\r\n[${error.code || 'SSH_CONNECT_FAILED'}] ${error.message}\r\n`); setStatus('error'); }
      })();
      function send(data) { if (closed) return; if (ws?.readyState === WebSocket.OPEN) ws.send(data); else queued.push(data); }
      return {
        write(data) { send(typeof data === 'string' ? encoder.encode(data) : data); },
        resize(cols, rows) { send(JSON.stringify({ type: 'resize', cols, rows })); },
        sendFiles(files) { options.onTransferUnavailable?.(files); },
        close() { closed = true; queued = []; if (ws && ws.readyState < WebSocket.CLOSING) ws.close(1000, 'client close'); }
      };
    }
  };
  window.LinuxTerminalGateway = gateway;
  window.QiyunAPI = {
    request, refreshAssets, getAssets, onAssets, mapAsset,
    createAsset: body => request('/api/v1/assets', { method: 'POST', body: JSON.stringify(body) }),
    getAsset: id => request('/api/v1/assets/' + encodeURIComponent(id)),
    updateAsset: (id, patch) => request('/api/v1/assets/' + encodeURIComponent(id), { method: 'PATCH', body: JSON.stringify(patch) }),
    deleteAsset: id => request('/api/v1/assets/' + encodeURIComponent(id), { method: 'DELETE' }),
    scanHostKey: id => request(`/api/v1/assets/${encodeURIComponent(id)}/host-key/scan`, { method: 'POST', body: '{}' }),
    approveHostKey: (id, scan) => request(`/api/v1/assets/${encodeURIComponent(id)}/host-key/approve`, { method: 'POST', body: JSON.stringify({ host_key: scan.host_key, fingerprint: scan.fingerprint }) }),
    testAsset: id => request(`/api/v1/assets/${encodeURIComponent(id)}/test`, { method: 'POST', body: '{}' }),
    diagnoseAsset: id => request(`/api/v1/assets/${encodeURIComponent(id)}/diagnose`, { method: 'POST', body: '{}' }),
    updateCredential: (id, body) => request(`/api/v1/assets/${encodeURIComponent(id)}/credential`, { method: 'PUT', body: JSON.stringify(body) }),
    setToken(value) { if (value) tokenStore.setItem('qiyun.admin-token', value); else tokenStore.removeItem('qiyun.admin-token'); }
  };
  refreshAssets().catch(error => console.warn('Asset API unavailable:', error.message));
})();
