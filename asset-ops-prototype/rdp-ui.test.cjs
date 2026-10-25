const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');

// terminal-gateway.js is the single data layer: it maps Go assets (snake_case,
// os_family/asset_type) into the objects every page renders. Load it with a
// tiny fake browser so the classification and RDP API shapes are testable
// without a running server.
function bootGateway() {
  const stored = new Map();
  const calls = [];
  const window = {
    sessionStorage: {
      getItem: key => stored.get(key) ?? null,
      setItem: (key, value) => stored.set(key, value),
      removeItem: key => stored.delete(key)
    }
  };
  const context = vm.createContext({
    window, console, URL, Number, Date, Map, Set, Promise, TextEncoder, TextDecoder,
    setTimeout, clearTimeout, setInterval, clearInterval,
    location: { href: 'http://127.0.0.1:8765/' },
    WebSocket: class {},
    fetch: async (url, options = {}) => {
      calls.push({ url, options });
      return { ok: true, status: 200, json: async () => ({ ok: true }) };
    }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'terminal-gateway.js'), 'utf8'), context);
  return { api: window.QiyunAPI, calls };
}

test('mapAsset classifies Windows assets and keeps the RDP port separate from SSH', () => {
  const { api } = bootGateway();
  const windows = api.mapAsset({
    id: 'w1', asset_code: 'WIN-1', name: 'win-rds-01', address: '10.20.30.33',
    ssh_port: 22, rdp_port: 3390, os_family: 'windows', os_name: 'Windows Server 2022',
    asset_type: 'windows_host', host_key_status: 'pending', username: 'CORP\\ops'
  });
  assert.equal(windows.type, 'Windows');
  assert.equal(windows.assetType, 'windows_host');
  assert.equal(windows.rdpPort, 3390);
  assert.equal(windows.sshPort, 22);
  assert.equal(windows.status, '待连接', 'RDP 资产不应显示为等待 SSH 指纹');
  assert.equal(windows.username, 'CORP\\ops');

  const linux = api.mapAsset({
    id: 'l1', asset_code: 'LNX-1', name: 'srv', address: '10.20.30.21',
    ssh_port: 2200, rdp_port: 3389, os_family: 'linux', os_name: 'Ubuntu 24.04',
    asset_type: 'linux_host', host_key_status: 'verified'
  });
  assert.equal(linux.type, 'Linux');
  assert.equal(linux.sshPort, 2200);
  assert.equal(linux.status, '在线');
});

test('legacy Windows assets without os_family are still recognised by OS name', () => {
  const { api } = bootGateway();
  const legacy = api.mapAsset({
    id: 'old', asset_code: 'OLD-1', name: 'old-win', address: '10.0.0.9',
    ssh_port: 22, os_family: 'linux', os_name: 'Windows Server 2019', asset_type: 'linux_host'
  });
  assert.equal(legacy.type, 'Windows');
  assert.equal(api.isWindowsAsset({ os_name: 'Microsoft Windows 11' }), true);
  assert.equal(api.isWindowsAsset({ os_name: 'Ubuntu 24.04' }), false);
});

test('RDP API methods hit the documented endpoints', async () => {
  const { api, calls } = bootGateway();
  await api.rdpCapabilities();
  await api.rdpSetupSigning();
  await api.rdpLaunch('asset/id', { port: 3390, redirect_clipboard: true });
  // Loading the data layer also refreshes the asset list; only look at RDP calls.
  const rdpCalls = calls.filter(call => call.url.includes('/rdp'));
  assert.equal(rdpCalls.length, 3);
  assert.equal(rdpCalls[0].url, '/api/v1/rdp/capabilities');
  assert.equal(rdpCalls[1].url, '/api/v1/rdp/signing');
  assert.equal(rdpCalls[1].options.method, 'POST');
  assert.equal(rdpCalls[2].url, '/api/v1/assets/asset%2Fid/rdp/launch');
  assert.equal(rdpCalls[2].options.method, 'POST');
  assert.deepEqual(JSON.parse(rdpCalls[2].options.body), { port: 3390, redirect_clipboard: true });
});

// Render the real remote-management dialog with a DOM so the async capability
// branch and the request payload are tested the way the browser runs them.
function bootInfra({ signingConfigured = true, available = true, api = {}, account = 'CORP\\ops', central = false, connector = null } = {}) {
  const { JSDOM } = require('jsdom');
  const dom = new JSDOM('<div id="asset-app" class="vab-shell"><div id="page-content"></div><div id="modal-layer" hidden></div></div>');
  const document = dom.window.document;
  for (const filename of ['styles.css', 'admin-theme.css']) {
    const style = document.createElement('style');
    style.textContent = fs.readFileSync(path.join(__dirname, filename), 'utf8');
    document.head.appendChild(style);
  }
  const calls = { launches: [], toasts: [], capabilities: 0, setups: 0, connectorLaunches: [], connectorSigning: 0, connectorTickets: [], webRdp: [] };
  let modalTitle = '';
  const D = {
    state: { page: 'hosts' }, pages: {}, actions: {}, meta: {}, root: document.querySelector('#asset-app'),
    escape: value => String(value ?? '').replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char])),
    button: (label, action, primary, attrs = '') => `<button class="op-btn${primary ? ' op-btn-primary' : ''}" data-action="${action}" ${attrs}>${label}</button>`,
    badge: value => String(value), table: () => '', panel: (title, body) => body, metric: () => '',
    modal(title, html) {
      modalTitle = title;
      const layer = this.root.querySelector('#modal-layer');
      layer.hidden = false;
      layer.innerHTML = `<section class="op-modal" role="dialog"><div class="modal-body">${html}</div></section>`;
    },
    closeModal() { const layer = this.root.querySelector('#modal-layer'); layer.hidden = true; layer.innerHTML = ''; }, render() {}, navigate() {},
    toast(message) { calls.toasts.push(message); }
  };
  const assets = [{
    id: 'win-1', assetCode: 'WIN-1', name: 'win-rds-01', type: 'Windows',
    os: 'Windows Server 2022', ip: '127.0.0.1', status: '待连接', sshPort: 22,
    rdpPort: 3399, username: account, assetType: 'windows_host',
    hostKeyStatus: 'pending', hostKeyFingerprint: '', department: 'IT'
  }];
  const QiyunAPI = {
    getAssets: () => assets.map(item => ({ ...item })), assetStatus: () => ({ loaded: true }),
    onAssets() {}, onAssetsError() {},
    rdpCapabilities: async () => {
      calls.capabilities++;
      if (api.rdpCapabilities) return api.rdpCapabilities();
      return {
        platform: 'windows', available, mstsc: true, rdpsign: true, credential_api: true,
        signing: { configured: signingConfigured, signingHash: 'sha256' },
        error: available ? '' : '本服务不在 Windows 上运行'
      };
    },
    rdpSetupSigning: async () => {
      calls.setups++;
      if (api.rdpSetupSigning) return api.rdpSetupSigning();
      signingConfigured = true;
      return { configured: true, signingHash: 'sha256' };
    },
    rdpLaunch: async (id, body) => {
      calls.launches.push({ id, body });
      if (api.rdpLaunch) return api.rdpLaunch(id, body);
      return { ok: true, pid: 4321, target: '127.0.0.1:3399' };
    },
    rdpConnectorInfo: async () => ({ connector_url: 'http://127.0.0.1:8766', download_url: '/downloads/QiyunConnector.zip', web_rdp: !!api.webRdp }),
    connectorHealth: async () => (api.connectorHealth ? api.connectorHealth() : connector),
    connectorSigning: async () => {
      calls.connectorSigning++;
      if (api.connectorSigning) return api.connectorSigning();
      return { configured: true, signingHash: 'sha256' };
    },
    rdpTicket: async (id, body) => {
      calls.connectorTickets.push({ id, body });
      return { ticket: 'ticket-1' };
    },
    connectorLaunch: async ticket => {
      calls.connectorLaunches.push(ticket);
      return { ok: true, pid: 9876, target: '127.0.0.1:3399' };
    },
    webRdp: async (id, body) => {
      calls.webRdp.push({ id, body });
      return { url: 'https://guac.example.com/guacamole/#/client/abc', mode: 'guacamole' };
    },
    setConnectorBase() {}
  };
  const window = { AssetDemo: D, QiyunAPI, addEventListener() {}, open: () => ({}) };
  if (central) window.QiyunAuth = { state: { authEnabled: true } };
  const context = vm.createContext({
    window, document, console, URL, Number, Date, Map, Set, Promise,
    setTimeout, clearTimeout, Blob: dom.window.Blob, navigator: { clipboard: {} }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'infra.js'), 'utf8'), context);
  // Keep the actual host page's demo styles mounted while the modal is open.
  document.querySelector('#page-content').innerHTML = D.pages.hosts();
  return { D, document, calls, api: QiyunAPI, style: element => dom.window.getComputedStyle(element), get title() { return modalTitle; } };
}

const tick = () => new Promise(resolve => setTimeout(resolve, 0));

test('Windows 远程管理弹窗在签名就绪时启用连接按钮', async () => {
  const s = bootInfra();
  s.D.infrastructure.openRemote('win-1');
  assert.ok(s.title.includes('win-rds-01'), 'remote dialog must open for a Windows asset');
  await tick();
  assert.equal(s.document.querySelector('#rdp-launch').disabled, false);
  assert.equal(s.document.querySelector('#rdp-cap-title').textContent, '本机 mstsc 已就绪');
  assert.equal(s.document.querySelector('#rdp-signing').hidden, true);
  assert.equal(s.document.querySelector('#rdp-port').value, '3399');
  assert.equal(s.document.querySelector('#rdp-account').textContent, 'CORP\\ops');
});

test('签名缺失时禁用连接并显示首次启用入口', async () => {
  const s = bootInfra({ signingConfigured: false });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  assert.equal(s.document.querySelector('#rdp-launch').disabled, true);
  assert.equal(s.document.querySelector('#rdp-signing').hidden, false);
  assert.match(s.document.querySelector('#rdp-cap-title').textContent, /首次启用/);
});

test('非 Windows 服务显示不可用原因而不是可点击的启动按钮', async () => {
  const s = bootInfra({ available: false });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  assert.equal(s.document.querySelector('#rdp-launch').disabled, true);
  assert.match(s.document.querySelector('#rdp-cap-detail').textContent, /Windows/);
});

test('连接按钮把目标、端口与本地资源选项交给 RDP 接口', async () => {
  const s = bootInfra();
  s.D.infrastructure.openRemote('win-1');
  await tick();
  s.document.querySelector('#rdp-drives').checked = true;
  s.document.querySelector('#rdp-drive-mode').value = 'custom';
  s.document.querySelector('#rdp-drive-list').value = 'D:,E:\\';
  s.document.querySelector('#rdp-fullscreen').checked = true;
  const button = s.document.querySelector('#rdp-launch');
  await s.D.actions['infra-rdp-launch'](button);
  assert.equal(s.calls.launches.length, 1);
  assert.equal(s.calls.launches[0].id, 'win-1');
  // The payload is built inside the vm context, so compare its JSON shape
  // rather than its prototype.
  assert.equal(JSON.stringify(s.calls.launches[0].body), JSON.stringify({
    port: 3399, redirect_clipboard: true, redirect_drives: true,
    drive_mode: 'custom', drive_list: 'D:,E:\\', fullscreen: true,
    multi_monitor: false, prompt_credentials: false
  }));
  assert.match(s.calls.toasts[0], /远程桌面已启动/);
});

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const cap = configured => ({ available: true, signing: { configured, signingHash: 'sha256' } });
const byId = (s, id) => s.document.getElementById(id);
const act = (s, id) => { const button = byId(s, id); return s.D.actions[button.dataset.action](button); };

test('中心模式检测到连接器后通过票据启动本机 mstsc', async () => {
  const s = bootInfra({ central: true, connector: { ok: true, capabilities: cap(true) } });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  assert.equal(byId(s, 'rdp-launch').disabled, false);
  assert.equal(byId(s, 'rdp-connector-box').hidden, true);
  await act(s, 'rdp-launch');
  assert.equal(s.calls.connectorTickets.length, 1);
  assert.equal(s.calls.connectorTickets[0].id, 'win-1');
  assert.deepEqual(s.calls.connectorLaunches, ['ticket-1']);
  assert.match(s.calls.toasts.join(' '), /远程桌面已启动/);
});

test('中心模式没有连接器时提示下载且不能启动', async () => {
  const s = bootInfra({ central: true, connector: null });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  assert.equal(byId(s, 'rdp-launch').disabled, true);
  assert.equal(byId(s, 'rdp-connector-box').hidden, false);
  assert.match(byId(s, 'rdp-cap-title').textContent, /未检测到本机 Windows 连接器/);
  assert.match(byId(s, 'rdp-connector-download').getAttribute('href'), /QiyunConnector\.zip/);
});

test('中心模式可通过本机连接器完成签名', async () => {
  const health = { ok: true, capabilities: cap(false) };
  const s = bootInfra({
    central: true, connector: health,
    api: { connectorSigning: async () => { health.capabilities = cap(true); return { signingHash: 'sha256' }; } }
  });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  assert.equal(byId(s, 'rdp-launch').disabled, true);
  assert.equal(byId(s, 'rdp-signing').hidden, false);
  await act(s, 'rdp-signing');
  assert.equal(s.calls.connectorSigning, 1);
  assert.equal(byId(s, 'rdp-launch').disabled, false);
  assert.equal(byId(s, 'rdp-signing').hidden, true);
});

test('中心模式配置 Guacamole 时可直接在浏览器打开', async () => {
  const s = bootInfra({ central: true, connector: null, api: { webRdp: true } });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  assert.equal(byId(s, 'rdp-web').hidden, false);
  assert.equal(byId(s, 'rdp-web').disabled, false);
  assert.equal(byId(s, 'rdp-launch').disabled, true);
  await act(s, 'rdp-web');
  assert.equal(s.calls.webRdp.length, 1);
  assert.equal(s.calls.webRdp[0].id, 'win-1');
  assert.match(s.calls.toasts.join(' '), /浏览器中打开远程桌面/);
});

test('签名成功后重新检测，设置与其他操作在请求期间均禁用', async () => {
  const setup = deferred();
  let configured = false;
  const s = bootInfra({ api: {
    rdpCapabilities: async () => cap(configured),
    rdpSetupSigning: () => setup.promise
  } });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  const request = act(s, 'rdp-signing');
  assert.equal(byId(s, 'rdp-panel').getAttribute('aria-busy'), 'true');
  for (const control of byId(s, 'rdp-panel').querySelectorAll('button,input,select')) assert.equal(control.disabled, true);
  await act(s, 'rdp-signing');
  await act(s, 'rdp-refresh');
  await act(s, 'rdp-launch');
  assert.equal(s.calls.setups, 1);
  assert.equal(s.calls.capabilities, 1);
  assert.equal(s.calls.launches.length, 0);
  configured = true;
  setup.resolve({ signingHash: 'sha256' });
  await request;
  assert.equal(s.calls.capabilities, 2);
  assert.equal(byId(s, 'rdp-panel').getAttribute('aria-busy'), 'false');
  assert.equal(byId(s, 'rdp-signing').hidden, true);
  assert.equal(byId(s, 'rdp-launch').disabled, false);
  assert.equal(byId(s, 'rdp-port').disabled, false);
  assert.equal(byId(s, 'rdp-refresh').disabled, false);
});

test('长 PowerShell 错误仅在折叠技术信息出现一次，不影响专用网格', async () => {
  const longError = 'New-SelfSignedCertificate : NTE_PERM\nAt line: 24 char: 9\n' + 'CertificatePrivateKeyPath'.repeat(250) + '\n<script>bad()</script>';
  const account = 'MicrosoftAccount\\' + 'long.account.'.repeat(40) + '@example.test';
  const s = bootInfra({ signingConfigured: false, account, api: { rdpSetupSigning: async () => { throw new Error(longError); } } });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  await act(s, 'rdp-signing');
  const panel = byId(s, 'rdp-panel'), status = byId(s, 'rdp-cap-title').closest('[role=status]');
  const error = byId(s, 'rdp-error-box'), settings = byId(s, 'rdp-port').parentElement.parentElement;
  assert.equal(status.parentElement, panel);
  assert.equal(error.parentElement, panel);
  assert.equal(settings.parentElement, panel);
  assert.equal(s.style(panel).display, 'grid');
  assert.equal(s.style(panel).gridTemplateColumns, 'minmax(0,1fr)');
  assert.equal(s.style(settings).gridTemplateColumns, 'repeat(2,minmax(0,1fr))');
  assert.equal(s.style(status).gridTemplateColumns, '9px minmax(0,1fr)');
  assert.equal(s.style(settings.children[0]).minWidth, '0');
  assert.equal(s.style(settings.children[1]).minWidth, '0');
  assert.equal(s.style(byId(s, 'rdp-port')).width, '100%');
  assert.equal(byId(s, 'rdp-account').textContent, account);
  assert.equal(s.style(byId(s, 'rdp-account')).overflowWrap, 'anywhere');
  assert.equal(byId(s, 'rdp-error-details').open, false);
  assert.equal(byId(s, 'rdp-error-details').hidden, false);
  assert.equal(byId(s, 'rdp-error-technical').textContent, longError);
  assert.equal(panel.textContent.split(longError).length - 1, 1);
  assert.equal(byId(s, 'rdp-error').textContent.includes('NTE_PERM'), false);
  assert.equal(byId(s, 'rdp-cap-detail').textContent.includes('NTE_PERM'), false);
  assert.equal(panel.querySelector('script'), null);
  assert.equal(s.style(byId(s, 'rdp-error-technical')).whiteSpace, 'pre-wrap');
  assert.equal(s.style(byId(s, 'rdp-error-technical')).overflowWrap, 'anywhere');
  byId(s, 'rdp-error-details').querySelector('summary').click();
  assert.equal(byId(s, 'rdp-error-details').open, true);
  assert.equal(byId(s, 'rdp-launch').disabled, true);
  assert.equal(byId(s, 'rdp-signing').disabled, false);
  await act(s, 'rdp-refresh');
  assert.equal(byId(s, 'rdp-error-box').hidden, true);
  assert.equal(byId(s, 'rdp-error-details').open, false);
  assert.equal(byId(s, 'rdp-error-technical').textContent, '');
});

test('403 签名存储访问错误指导普通终端启动且不重复服务端提示', async () => {
  const detail = '当前服务无法写入 Windows 证书/私钥存储。请从本机普通 Windows 终端运行 start-qiyun.cmd 后重试';
  const failure = Object.assign(new Error(detail), { status: 403, code: 'RDP_SIGNING_ACCESS_DENIED' });
  const s = bootInfra({ signingConfigured: false, api: { rdpSetupSigning: async () => { throw failure; } } });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  await act(s, 'rdp-signing');
  assert.match(byId(s, 'rdp-error').textContent, /普通 Windows 终端运行 start-qiyun\.cmd/);
  assert.equal(byId(s, 'rdp-panel').textContent.split('start-qiyun.cmd').length - 1, 1);
  assert.doesNotMatch(byId(s, 'rdp-panel').textContent, /管理员|UAC/);
  assert.equal(byId(s, 'rdp-error-technical').textContent, 'RDP_SIGNING_ACCESS_DENIED');
  assert.equal(byId(s, 'rdp-signing').disabled, false);
  assert.equal(byId(s, 'rdp-refresh').disabled, false);
  assert.equal(byId(s, 'rdp-launch').disabled, true);
});

test('重新检测失败后不沿用之前可启动状态', async () => {
  const s = bootInfra();
  s.D.infrastructure.openRemote('win-1');
  await tick();
  s.api.rdpCapabilities = async () => { throw new Error('network unavailable'); };
  await act(s, 'rdp-refresh');
  assert.equal(byId(s, 'rdp-launch').disabled, true);
  assert.equal(byId(s, 'rdp-refresh').disabled, false);
  assert.equal(byId(s, 'rdp-error-box').hidden, false);
  assert.match(byId(s, 'rdp-error').textContent, /重新检测/);
});

test('端口和自定义磁盘校验失败不会发起远程启动', async () => {
  const s = bootInfra();
  s.D.infrastructure.openRemote('win-1');
  await tick();
  byId(s, 'rdp-port').value = '65536';
  await act(s, 'rdp-launch');
  assert.match(byId(s, 'rdp-error').textContent, /端口/);
  assert.equal(byId(s, 'rdp-error-details').hidden, true);
  byId(s, 'rdp-port').value = '3389';
  byId(s, 'rdp-drives').checked = true;
  await act(s, 'rdp-drives');
  assert.equal(byId(s, 'rdp-drive-options').hidden, false);
  byId(s, 'rdp-drive-mode').value = 'custom';
  await act(s, 'rdp-launch');
  assert.match(byId(s, 'rdp-error').textContent, /盘符/);
  assert.equal(s.calls.launches.length, 0);
});

test('启动返回签名失效时重新提供初始化入口并禁止重复启动', async () => {
  const s = bootInfra({ api: { rdpLaunch: async () => { throw Object.assign(new Error('signing no longer configured'), { code: 'RDP_SIGNING_REQUIRED' }); } } });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  await act(s, 'rdp-launch');
  assert.equal(byId(s, 'rdp-launch').disabled, true);
  assert.equal(byId(s, 'rdp-signing').hidden, false);
  assert.equal(byId(s, 'rdp-signing').disabled, false);
  await act(s, 'rdp-launch');
  assert.equal(s.calls.launches.length, 1);
});

test('已关闭弹窗的能力检测结果不能覆盖同一主机的新弹窗', async () => {
  const old = deferred();
  let call = 0;
  const s = bootInfra({ api: { rdpCapabilities: () => ++call === 1 ? old.promise : Promise.resolve(cap(false)) } });
  s.D.infrastructure.openRemote('win-1');
  const oldLaunch = byId(s, 'rdp-launch');
  s.D.closeModal();
  s.D.infrastructure.openRemote('win-1');
  await tick();
  old.resolve(cap(true));
  await tick();
  assert.equal(byId(s, 'rdp-launch').disabled, true);
  assert.equal(byId(s, 'rdp-signing').hidden, false);
  assert.match(byId(s, 'rdp-cap-title').textContent, /首次启用/);
  await s.D.actions['infra-rdp-launch'](oldLaunch);
  assert.equal(s.calls.launches.length, 0);
});

for (const succeeds of [true, false]) test(`旧签名请求${succeeds ? '成功' : '失败'}不更新新弹窗或触发新检测`, async () => {
  const old = deferred();
  const s = bootInfra({ signingConfigured: false, api: { rdpSetupSigning: () => old.promise } });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  const request = act(s, 'rdp-signing');
  s.D.closeModal();
  s.D.infrastructure.openRemote('win-1');
  await tick();
  if (succeeds) old.resolve({ signingHash: 'old' });
  else old.reject(new Error('old setup failure'));
  await request;
  assert.equal(s.calls.capabilities, 2);
  assert.equal(s.calls.toasts.length, 0);
  assert.equal(byId(s, 'rdp-error-box').hidden, true);
  assert.match(byId(s, 'rdp-cap-title').textContent, /首次启用/);
  assert.equal(byId(s, 'rdp-signing').disabled, false);
  assert.equal(byId(s, 'rdp-launch').disabled, true);
});

for (const succeeds of [true, false]) test(`旧启动请求${succeeds ? '成功' : '失败'}不改变新弹窗连接状态`, async () => {
  const old = deferred();
  const s = bootInfra({ api: { rdpLaunch: () => old.promise } });
  s.D.infrastructure.openRemote('win-1');
  await tick();
  const oldButton = byId(s, 'rdp-launch');
  const request = act(s, 'rdp-launch');
  await act(s, 'rdp-launch');
  assert.equal(s.calls.launches.length, 1);
  s.D.closeModal();
  s.api.rdpCapabilities = async () => cap(false);
  s.D.infrastructure.openRemote('win-1');
  await tick();
  if (succeeds) old.resolve({ ok: true, target: 'old-target', pid: 1234 });
  else old.reject(Object.assign(new Error('old launch failure'), { code: 'RDP_PLATFORM_UNSUPPORTED' }));
  await request;
  assert.equal(s.calls.toasts.length, 0);
  assert.equal(byId(s, 'rdp-error-box').hidden, true);
  assert.equal(byId(s, 'rdp-launch').disabled, true);
  assert.equal(byId(s, 'rdp-launch').textContent, '连接远程桌面');
  assert.equal(byId(s, 'rdp-signing').hidden, false);
  await s.D.actions['infra-rdp-launch'](oldButton);
  assert.equal(s.calls.launches.length, 1);
});