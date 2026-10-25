const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM } = require('jsdom');

function response(status, body) {
  return {
    status, ok: status >= 200 && status < 300,
    json: async () => body,
    text: async () => (body == null ? '' : JSON.stringify(body)),
  };
}

function boot({ meStatus = 200, authEnabled = true, user = null, permissions = [] } = {}) {
  const dom = new JSDOM('<!doctype html><html><body><div id="asset-app"><div class="top-actions"></div></div></body></html>', {
    url: 'http://127.0.0.1:8765/#/hosts',
    runScripts: 'outside-only',
  });
  const window = dom.window;
  const document = window.document;
  let initCalled = 0;
  let lastRequest = null;
  const D = {
    escape: value => String(value ?? '').replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char])),
    button: (label, action, primary, attrs = '') => `<button data-action="${action}" ${attrs}>${label}</button>`,
    table: () => '', badge: value => String(value), modal() {}, toast() {}, closeModal() {},
    actions: {}, root: document.querySelector('#asset-app'),
    init() { initCalled++; },
  };
  window.AssetDemo = D;
  window.Headers = globalThis.Headers;
  window.fetch = async (input, init = {}) => {
    const url = typeof input === 'string' ? input : input.url;
    lastRequest = { url, init };
    if (url.includes('/api/v1/auth/me')) {
      if (meStatus === 401) return response(401, { detail: '请先登录' });
      return response(200, { auth_enabled: authEnabled, user, permissions, csrf_token: 'csrf-test-token' });
    }
    return response(200, { items: [] });
  };
  window.eval(fs.readFileSync(path.join(__dirname, 'auth-ui.js'), 'utf8'));
  return {
    window, document, D, get initCalled() { return initCalled; },
    get lastRequest() { return lastRequest; },
  };
}

const tick = () => new Promise(resolve => setTimeout(resolve, 10));

test('未登录时显示登录页且不初始化应用', async () => {
  const s = boot({ meStatus: 401 });
  s.D.init();
  await tick();
  assert.equal(s.initCalled, 0);
  const overlay = s.document.querySelector('#qiyun-login');
  assert.ok(overlay, 'login overlay must be created');
  assert.equal(overlay.hidden, false);
  assert.ok(s.document.querySelector('#qiyun-login-form'));
});

test('已登录会话初始化应用并安装用户菜单', async () => {
  const s = boot({
    user: { id: 'u1', username: 'admin', display_name: '系统管理员', role: 'admin' },
    permissions: ['user.manage'],
  });
  s.D.init();
  await tick();
  assert.equal(s.initCalled, 1);
  const chip = s.document.querySelector('#qiyun-user-chip');
  assert.ok(chip, 'user chip must be installed');
  assert.match(chip.textContent, /系统管理员/);
  assert.match(chip.textContent, /管理员/);
});

test('写入请求自动附加 CSRF 头', async () => {
  const s = boot({
    user: { id: 'u1', username: 'admin', display_name: '系统管理员', role: 'admin' },
    permissions: ['user.manage'],
  });
  s.D.init();
  await tick();
  await s.window.fetch('/api/v1/users', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
  assert.ok(s.lastRequest.init.headers instanceof globalThis.Headers);
  assert.equal(s.lastRequest.init.headers.get('X-CSRF-Token'), 'csrf-test-token');
});

test('本机模式不显示退出按钮但保留用户菜单', async () => {
  const s = boot({
    authEnabled: false,
    user: { username: 'local', display_name: '本机用户', role: 'admin' },
    permissions: ['user.manage'],
  });
  s.D.init();
  await tick();
  assert.equal(s.initCalled, 1);
  const chip = s.document.querySelector('#qiyun-user-chip');
  assert.ok(chip);
  assert.equal(chip.querySelector('[data-action="qiyun-logout"]'), null);
  assert.equal(chip.querySelector('[data-action="qiyun-user-admin"]'), null);
});