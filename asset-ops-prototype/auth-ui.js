// Central authentication UI: login overlay, CSRF-aware fetch, user
// administration and per-asset grants. Kept separate from core.js so the local
// single-user mode still starts without a login screen.
(() => {
  const state = { me: null, permissions: [], csrf: '', authEnabled: false, loginShown: false };
  const rawFetch = window.fetch.bind(window);
  const roles = { admin: '管理员', operator: '运维', auditor: '审计', viewer: '只读' };

  function hasPermission(code) {
    return state.me?.role === 'admin' || state.permissions.includes(code);
  }
  function roleLabel(role) { return roles[role] || role; }
  function esc(value) { return AssetDemo.escape(value); }

  function injectStyles() {
    if (document.getElementById('qiyun-auth-style')) return;
    const style = document.createElement('style');
    style.id = 'qiyun-auth-style';
    style.textContent = `
      #qiyun-login{position:fixed;inset:0;z-index:9999;background:rgba(18,32,38,.72);display:grid;place-items:center;padding:24px}
      #qiyun-login[hidden]{display:none}
      .qiyun-login-card{width:min(380px,100%);background:var(--surface,#fff);border:1px solid var(--line,#e1e8ec);border-radius:14px;padding:28px;box-shadow:0 24px 80px #0004;color:var(--ink,#203037)}
      .qiyun-login-card h1{font-size:19px;margin:0 0 4px}
      .qiyun-login-card p{font-size:12px;color:var(--sub,#74828b);margin:0 0 20px}
      .qiyun-login-card label{display:block;font-size:12px;color:var(--sub,#74828b);margin:12px 0 5px}
      .qiyun-login-card input{width:100%;box-sizing:border-box;padding:10px 12px;border:1px solid var(--line,#dbe4e8);border-radius:7px;font:inherit;background:var(--surface,#fff);color:inherit}
      .qiyun-login-card button{width:100%;margin-top:18px;padding:10px;border:0;border-radius:7px;background:#07836c;color:#fff;font:inherit;cursor:pointer}
      .qiyun-login-card button:disabled{opacity:.6;cursor:default}
      .qiyun-login-error{min-height:18px;margin-top:10px;color:#cf5759;font-size:12px}
      .qiyun-user-chip{display:flex;align-items:center;gap:8px;font-size:12px;white-space:nowrap}
      .qiyun-user-name{font-weight:600}
      .qiyun-user-role{font-size:11px;color:var(--sub,#74828b);border:1px solid var(--line,#dbe4e8);border-radius:4px;padding:1px 5px}
      .qiyun-admin-form{margin-bottom:18px}
      .qiyun-admin-form .row{gap:8px}
    `;
    document.head.appendChild(style);
  }

  function showLogin(message = '') {
    injectStyles();
    let overlay = document.getElementById('qiyun-login');
    if (!overlay) {
      overlay = document.createElement('div');
      overlay.id = 'qiyun-login';
      overlay.innerHTML = `<form class="qiyun-login-card" id="qiyun-login-form">
        <h1>栖云 · 资产应用与运维</h1>
        <p>请使用管理员分配的账号登录</p>
        <label for="qiyun-username">用户名</label>
        <input id="qiyun-username" name="username" autocomplete="username" required autofocus>
        <label for="qiyun-password">密码</label>
        <input id="qiyun-password" name="password" type="password" autocomplete="current-password" required>
        <div id="qiyun-login-error" class="qiyun-login-error" role="alert"></div>
        <button type="submit" id="qiyun-login-submit">登录</button>
      </form>`;
      document.body.appendChild(overlay);
      overlay.querySelector('#qiyun-login-form').addEventListener('submit', async event => {
        event.preventDefault();
        const button = overlay.querySelector('#qiyun-login-submit');
        const error = overlay.querySelector('#qiyun-login-error');
        const username = overlay.querySelector('#qiyun-username').value.trim();
        const password = overlay.querySelector('#qiyun-password').value;
        error.textContent = '';
        button.disabled = true;
        button.textContent = '正在登录…';
        try {
          const response = await rawFetch('/api/v1/auth/login', {
            method: 'POST', credentials: 'same-origin',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ username, password }),
          });
          const body = await response.json().catch(() => ({}));
          if (!response.ok) throw new Error(body.detail || '用户名或密码错误');
          window.location.reload();
        } catch (err) {
          error.textContent = err.message || '登录失败';
          button.disabled = false;
          button.textContent = '登录';
        }
      });
    }
    overlay.hidden = false;
    state.loginShown = true;
    if (message) overlay.querySelector('#qiyun-login-error').textContent = message;
    setTimeout(() => overlay.querySelector('#qiyun-username')?.focus(), 30);
  }

  function hideLogin() {
    const overlay = document.getElementById('qiyun-login');
    if (overlay) overlay.hidden = true;
    state.loginShown = false;
  }

  async function ensureSession() {
    const response = await rawFetch('/api/v1/auth/me', { credentials: 'same-origin' });
    if (response.status === 401) return null;
    if (!response.ok) throw new Error('认证服务返回 ' + response.status);
    const data = await response.json();
    state.me = data.user || null;
    state.permissions = Array.isArray(data.permissions) ? data.permissions : [];
    state.csrf = data.csrf_token || '';
    state.authEnabled = !!data.auth_enabled;
    return data;
  }

  // Add the CSRF header to same-origin API writes and surface expired sessions.
  window.fetch = function (input, init = {}) {
    const url = typeof input === 'string' ? input : (input?.url || '');
    const method = (init.method || input?.method || 'GET').toUpperCase();
    let nextInit = init;
    if (url.startsWith('/api/') && state.csrf && !['GET', 'HEAD', 'OPTIONS'].includes(method)) {
      const headers = new Headers(init.headers || input?.headers || {});
      headers.set('X-CSRF-Token', state.csrf);
      nextInit = { ...init, headers };
    }
    return rawFetch(input, nextInit).then(response => {
      if (response.status === 401 && !url.includes('/api/v1/auth/')) showLogin('登录已过期，请重新登录');
      return response;
    });
  };

  async function api(url, options = {}) {
    const response = await window.fetch(url, { credentials: 'same-origin', ...options });
    const text = await response.text();
    let body = null;
    try { body = text ? JSON.parse(text) : null; } catch { body = { detail: text }; }
    if (!response.ok) {
      if (response.status === 401) showLogin('登录已过期，请重新登录');
      throw new Error(body?.detail || body?.error || ('请求失败（' + response.status + '）'));
    }
    return body;
  }

  async function logout() {
    try { await api('/api/v1/auth/logout', { method: 'POST' }); } catch { /* ignore */ }
    window.location.reload();
  }

  function installUserMenu() {
    const top = document.querySelector('#asset-app .top-actions');
    if (!top || document.getElementById('qiyun-user-chip')) return;
    injectStyles();
    const chip = document.createElement('div');
    chip.id = 'qiyun-user-chip';
    chip.className = 'qiyun-user-chip';
    const name = esc(state.me?.display_name || state.me?.username || '本机用户');
    chip.innerHTML = `<span class="qiyun-user-name">${name}</span><span class="qiyun-user-role">${roleLabel(state.me?.role)}</span>`;
    if (state.authEnabled && hasPermission('user.manage')) {
      chip.insertAdjacentHTML('beforeend', AssetDemo.button('用户管理', 'qiyun-user-admin'));
    }
    if (state.authEnabled && hasPermission('audit.read')) {
      chip.insertAdjacentHTML('beforeend', AssetDemo.button('审计日志', 'qiyun-audit'));
    }
    if (state.authEnabled) {
      chip.insertAdjacentHTML('beforeend', AssetDemo.button('退出', 'qiyun-logout'));
    }
    top.appendChild(chip);
  }

  async function openUserAdmin() {
    if (!hasPermission('user.manage')) return AssetDemo.toast('当前账号没有用户管理权限。');
    try {
      const [users, permissions] = await Promise.all([api('/api/v1/users'), api('/api/v1/permissions')]);
      const rows = (users.items || []).map(user => {
        const status = user.status === 'active' ? AssetDemo.badge('启用', 'green') : AssetDemo.badge('停用', 'neutral');
        const last = user.last_login_at ? String(user.last_login_at).slice(0, 19).replace('T', ' ') : '—';
        const role = `<select data-qiyun-role="${esc(user.id)}">${Object.entries(roles).map(([value, label]) => `<option value="${value}" ${user.role === value ? 'selected' : ''}>${label}</option>`).join('')}</select>`;
        return [esc(user.username), esc(user.display_name || ''), role, status, last,
          `<button type="button" class="op-btn op-btn-small" data-qiyun-reset="${esc(user.id)}">重置密码</button> ` +
          `<button type="button" class="op-btn op-btn-small" data-qiyun-disable="${esc(user.id)}" ${user.status !== 'active' ? 'disabled' : ''}>停用</button>`];
      });
      const permissionText = (permissions.items || []).map(item => `${item.code}：${item.description}`).join('；');
      const html = `
        <form id="qiyun-new-user" class="qiyun-admin-form">
          <div class="notice">角色：管理员拥有全部权限；运维可连接被授权资产；审计可查看全部资产和审计；只读只能查看被授权资产。</div>
          <div class="form-grid">
            <div class="field"><label for="qiyun-new-username">用户名 *</label><input id="qiyun-new-username" maxlength="64" required></div>
            <div class="field"><label for="qiyun-new-display">显示名称</label><input id="qiyun-new-display" maxlength="80"></div>
            <div class="field"><label for="qiyun-new-password">初始密码 *</label><input id="qiyun-new-password" type="password" minlength="8" required autocomplete="new-password"></div>
            <div class="field"><label for="qiyun-new-role">角色</label><select id="qiyun-new-role">${Object.entries(roles).map(([value, label]) => `<option value="${value}">${label}</option>`).join('')}</select></div>
          </div>
          <div class="row" style="margin-top:12px"><button type="submit" class="op-btn op-btn-primary">创建用户</button></div>
        </form>
        <div id="qiyun-user-error" class="text-red small" role="alert" style="min-height:18px"></div>
        ${AssetDemo.table(['用户名', '显示名', '角色', '状态', '最近登录', '操作'], rows)}
        <div class="notice">权限目录：${esc(permissionText)}</div>`;
      AssetDemo.modal('用户与权限', html, AssetDemo.button('关闭', 'close-modal'));
      const layer = document.getElementById('modal-layer');
      layer.querySelector('#qiyun-new-user').addEventListener('submit', async event => {
        event.preventDefault();
        const error = layer.querySelector('#qiyun-user-error');
        error.textContent = '';
        try {
          await api('/api/v1/users', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              username: layer.querySelector('#qiyun-new-username').value.trim(),
              display_name: layer.querySelector('#qiyun-new-display').value.trim(),
              password: layer.querySelector('#qiyun-new-password').value,
              role: layer.querySelector('#qiyun-new-role').value,
            }),
          });
          AssetDemo.toast('用户已创建。');
          await openUserAdmin();
        } catch (err) { error.textContent = err.message; }
      });
      layer.querySelectorAll('[data-qiyun-role]').forEach(select => {
        select.addEventListener('change', async () => {
          try {
            await api('/api/v1/users/' + encodeURIComponent(select.dataset.qiyunRole), {
              method: 'PATCH', headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ role: select.value }),
            });
            AssetDemo.toast('角色已更新。');
            await openUserAdmin();
          } catch (err) { AssetDemo.toast('更新失败：' + err.message); }
        });
      });
      layer.querySelectorAll('[data-qiyun-reset]').forEach(button => {
        button.addEventListener('click', async () => {
          const password = window.prompt('输入新密码（至少 8 位）');
          if (!password) return;
          try {
            await api('/api/v1/users/' + encodeURIComponent(button.dataset.qiyunReset) + '/password', {
              method: 'PUT', headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ password }),
            });
            AssetDemo.toast('密码已重置，该用户现有会话已失效。');
          } catch (err) { AssetDemo.toast('重置失败：' + err.message); }
        });
      });
      layer.querySelectorAll('[data-qiyun-disable]').forEach(button => {
        button.addEventListener('click', async () => {
          if (!window.confirm('停用该用户并立即注销其会话？')) return;
          try {
            await api('/api/v1/users/' + encodeURIComponent(button.dataset.qiyunDisable), { method: 'DELETE' });
            AssetDemo.toast('用户已停用。');
            await openUserAdmin();
          } catch (err) { AssetDemo.toast('停用失败：' + err.message); }
        });
      });
    } catch (err) {
      AssetDemo.toast('读取用户失败：' + err.message);
    }
  }

  async function openAssetGrants(assetId) {
    if (!hasPermission('user.manage')) return AssetDemo.toast('当前账号没有授权管理权限。');
    try {
      const [users, grants] = await Promise.all([
        api('/api/v1/users'),
        api('/api/v1/assets/' + encodeURIComponent(assetId) + '/grants'),
      ]);
      const grantMap = new Map((grants.items || []).map(grant => [grant.user_id, grant]));
      const rows = (users.items || []).filter(user => user.role !== 'admin').map(user => {
        const grant = grantMap.get(user.id) || {};
        return [esc(user.username), esc(user.display_name || ''), roleLabel(user.role),
          `<input type="checkbox" data-qiyun-grant="${esc(user.id)}" data-kind="connect" ${grant.can_connect ? 'checked' : ''}>`,
          `<input type="checkbox" data-qiyun-grant="${esc(user.id)}" data-kind="edit" ${grant.can_edit ? 'checked' : ''}>`];
      });
      AssetDemo.modal('资产授权', `<div class="notice">管理员默认拥有全部资产；这里只配置运维/审计/只读用户的资产级权限。勾选“可连接”后才能发起 SSH/RDP，勾选“可编辑”后才能修改资产与凭据。</div>${AssetDemo.table(['用户', '显示名', '角色', '可连接', '可编辑'], rows)}`,
        AssetDemo.button('取消', 'close-modal') + AssetDemo.button('保存授权', 'qiyun-save-grants', true, `data-asset="${esc(assetId)}"`));
      const layer = document.getElementById('modal-layer');
      layer.querySelector('[data-action="qiyun-save-grants"]').addEventListener('click', async event => {
        const button = event.currentTarget;
        const byUser = new Map();
        layer.querySelectorAll('[data-qiyun-grant]').forEach(input => {
          const entry = byUser.get(input.dataset.qiyunGrant) || { user_id: input.dataset.qiyunGrant, can_connect: false, can_edit: false };
          entry[input.dataset.kind === 'edit' ? 'can_edit' : 'can_connect'] = input.checked;
          byUser.set(input.dataset.qiyunGrant, entry);
        });
        button.disabled = true;
        try {
          await api('/api/v1/assets/' + encodeURIComponent(assetId) + '/grants', {
            method: 'PUT', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ grants: [...byUser.values()] }),
          });
          AssetDemo.toast('资产授权已保存。');
          AssetDemo.closeModal();
        } catch (err) {
          AssetDemo.toast('保存失败：' + err.message);
          button.disabled = false;
        }
      });
    } catch (err) {
      AssetDemo.toast('读取授权失败：' + err.message);
    }
  }

  async function openAuditLog() {
    if (!hasPermission('audit.read')) return AssetDemo.toast('当前账号没有审计查看权限。');
    try {
      const data = await api('/api/v1/audit?limit=200');
      const rows = (data.items || []).map(event => [
        String(event.occurred_at || '').slice(0, 19).replace('T', ' '),
        esc(event.actor_user_id || 'system/local'),
        esc(event.action || ''),
        esc((event.object_type || '') + ':' + (event.object_id || '')),
        esc(event.result || ''),
        `<span class="small muted">${esc(JSON.stringify(event.metadata || {}).slice(0, 160))}</span>`
      ]);
      AssetDemo.modal('审计日志',
        `<div class="notice">最近 200 条服务端事件；时间按服务端 UTC 存储，页面直接显示原始时间。</div>${AssetDemo.table(['时间', '操作人', '动作', '对象', '结果', '详情'], rows)}`,
        AssetDemo.button('关闭', 'close-modal'));
    } catch (error) {
      AssetDemo.toast('读取审计失败：' + error.message);
    }
  }

  AssetDemo.actions['qiyun-user-admin'] = () => openUserAdmin();
  AssetDemo.actions['qiyun-audit'] = () => openAuditLog();
  AssetDemo.actions['qiyun-logout'] = () => logout();
  AssetDemo.actions['asset-grants'] = button => {
    if (window.QiyunAuth?.openAssetGrants) window.QiyunAuth.openAssetGrants(button.dataset.id);
  };

  window.QiyunAuth = { state, ensureSession, showLogin, openUserAdmin, openAssetGrants, hasPermission };

  const originalInit = AssetDemo.init;
  AssetDemo.init = function () {
    ensureSession().then(me => {
      if (!me) { showLogin(); return; }
      hideLogin();
      originalInit.call(AssetDemo);
      installUserMenu();
    }).catch(err => showLogin('无法连接认证服务：' + err.message));
  };
})();