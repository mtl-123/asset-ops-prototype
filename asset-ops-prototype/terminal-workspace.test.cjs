const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');

function setup(realToast = false, realDOM = false) {
  let toast = '';
  const writes = new Map();
  const assets = [
    { id: 'asset-prod', name: '生产主机', type: 'Linux', os: 'Ubuntu', ip: '10.0.0.10', owner: 'ops', department: '研发部', environment: 'production', status: '在线', hostKeyStatus: 'verified', hostKeyFingerprint: 'SHA256:prod' },
    { id: 'asset-test', name: '测试主机', type: 'Linux', os: 'Debian', ip: '10.0.0.11', owner: 'qa', department: '测试部', environment: 'testing', status: '在线', hostKeyStatus: 'verified', hostKeyFingerprint: 'SHA256:test' },
    { id: 'asset-pending', name: '待核验主机', type: 'Linux', os: 'Linux', ip: '10.0.0.12', owner: 'ops', department: '研发部', environment: 'production', status: '待核验', hostKeyStatus: 'pending' }
  ];
  const elements = new Map(), terminals = new Map(), connections = new Map(), timers = [], events = {};
  let renders = 0, fits = 0;
  const button = { setAttribute(key, value) { this[key] = value; } };
  const document = { fullscreenElement: null, fullscreenEnabled: true, addEventListener(name, fn) { events[name] = fn; }, async exitFullscreen() { this.fullscreenElement = null; events.fullscreenchange(); } };
  const toastElement = { hidden: true }, dialogs = [];
  document.createElement = tag => ({ tag, events: {}, children: [], setAttribute() {},
    append(...children) { this.children.push(...children); },
    addEventListener(name, fn) { this.events[name] = fn; },
    showModal() { dialogs.push(this); }, focus() {}, remove() { this.removed = true; }
  });
  const stage = { appendChild(el) { el.parentNode = this; }, async requestFullscreen() { document.fullscreenElement = stage; events.fullscreenchange(); } };
  const root = { contains(el) { return el === stage; }, appendChild(el) { el.parentNode = this; }, addEventListener() {}, querySelectorAll() { return []; }, querySelector(selector) {
    if (selector === '#app-toast') return toastElement;
    if (selector === '.terminal-stage') return stage;
    if (selector === '[data-action="terminal-fullscreen"]') return button;
    const id = selector.match(/data-terminal-xterm="([^"]+)"/)?.[1];
    if (!id) return null;
    if (!elements.has(id)) elements.set(id, { id, isConnected: true, clientWidth: 800, clientHeight: 400, closest() { return null; }, dataset: {}, events: {}, addEventListener(name, fn) { this.events[name] = fn; } });
    return elements.get(id);
  } };
  class Terminal {
    constructor(options) { this.options = options; this.selection = ''; this.pastes = []; this.output = ''; }
    open(element) { terminals.set(element.dataset.terminalXterm || element.id, this); }
    loadAddon() {} write(data) { this.output += data; } focus() {} dispose() { this.disposed = true; }
    onData(fn) { this.data = fn; } onBinary(fn) { this.binary = fn; } onResize() {}
    attachCustomKeyEventHandler(fn) { this.key = fn; }
    getSelection() { return this.selection; }
    paste(text) { this.pastes.push(text); this.data('\x1b[200~' + text + '\x1b[201~'); }
  }
  const navigator = { platform: 'Win32', clipboard: { async readText() { return '  pwd  '; }, async writeText(text) { navigator.copied = text; } } };
  const confirmations = [];
  const window = { document, navigator, Terminal, FitAddon: { FitAddon: class { fit() { fits++; } } }, confirm(message) { confirmations.push(message); return true; } };
  const A = {
    root, pages: {}, actions: {}, state: { page: 'terminal' }, meta: {},
    nav: [['基础设施', 'hosts', '主机与远程', 'monitor', '']], assets: [],
    escape: value => String(value ?? '').replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char])),
    button: (label, action, primary, attrs = '') => `<button data-action="${action}" ${attrs}>${label}</button>`,
    badge: value => String(value), table: () => '', panel: (title, body) => title + body, metric: () => '',
    modal() {}, closeModal() {}, render() { renders++; elements.clear(); }, navigate(page) { A.state.page = page; }, toast(message) { toast = message; }
  };
  const QiyunAPI = { getAssets: () => assets.map(item => ({ ...item })), onAssets() {}, createAsset() {}, scanHostKey() {}, approveHostKey() {}, testAsset() {}, refreshAssets() {} };
  const LinuxTerminalGateway = {
    connect(options) {
      connections.set(options.sessionId, options);
      writes.set(options.sessionId, []);
      options.onStatus('connected');
      return { write(data) { writes.get(options.sessionId).push(String(data)); }, resize() {}, close() {} };
    }
  };
  class TrzszFilter {
    constructor(options) { this.options = options; }
    processTerminalInput(data) { this.options.sendToServer(data); }
    processBinaryInput(data) { this.options.sendToServer(data); }
    processServerOutput(data) { this.options.writeToTerminal(data); }
    setTerminalColumns() {}
  }
  Object.assign(window, { AssetDemo: A, QiyunAPI, LinuxTerminalGateway, TrzszFilter });
  const context = vm.createContext({ window, navigator, requestAnimationFrame: fn => fn(), console, URL, Number, Date, Map, Set, Promise, TextDecoder, TextEncoder, setTimeout: fn => timers.push(fn), clearTimeout, setInterval, clearInterval });
  if (realToast) {
    document.getElementById = () => root;
    const coreContext = vm.createContext({ window: { ...window }, document, setTimeout: fn => timers.push(fn), clearTimeout });
    vm.runInContext(fs.readFileSync(path.join(__dirname, 'core.js'), 'utf8'), coreContext);
    A.toast = coreContext.window.AssetDemo.toast;
  }
  let dom, content;
  const observers = [], closed = [];
  if (realDOM) {
    const { JSDOM } = require('jsdom');
    dom = new JSDOM('<div id="asset-app"><div id="page-content"></div><div id="page-title"></div><div id="breadcrumb-current"></div><div id="page-eyebrow"></div><div id="page-description"></div><div class="heading-date"><strong></strong></div></div>');
    window.document = dom.window.document;
    A.root = window.document.querySelector('#asset-app'); content = A.root.firstElementChild;
    Object.defineProperty(dom.window.HTMLElement.prototype, 'clientWidth', { get() { return this.closest('[hidden]') ? 0 : 800; } });
    Object.defineProperty(dom.window.HTMLElement.prototype, 'clientHeight', { get() { return this.closest('[hidden]') ? 0 : 400; } });
    window.ResizeObserver = class { constructor(fn) { this.fn = fn; observers.push(this); } observe(el) { this.element = el; } disconnect() { this.disconnected = true; } };
    window.document.fullscreenEnabled = true;
    dom.window.HTMLElement.prototype.requestFullscreen = async function () { window.document.fullscreenElement = this; window.document.dispatchEvent(new dom.window.Event('fullscreenchange')); };
    window.document.exitFullscreen = async () => { window.document.fullscreenElement = null; };
    // 任一全屏祖先被删除都模拟浏览器退出全屏，防止“删掉再插回”蒙混过关。
    const descriptor = Object.getOwnPropertyDescriptor(dom.window.Element.prototype, 'innerHTML');
    Object.defineProperty(dom.window.Element.prototype, 'innerHTML', { ...descriptor, set(value) {
      if (window.document.fullscreenElement && this.contains(window.document.fullscreenElement)) window.document.fullscreenElement = null;
      descriptor.set.call(this, value);
    } });
    // 执行 core.js 中实际的 render 函数，覆盖其词法调用路径而非仅模拟 A.render。
    const coreSource = fs.readFileSync(path.join(__dirname, 'core.js'), 'utf8');
    const renderSource = coreSource.split('\n').find(line => line.includes('function render(){'));
    const coreRender = vm.runInNewContext(`(${renderSource.trim()})`, { root: A.root, state: A.state, meta: A.meta, pages: A.pages, window, refreshIcons() {} });
    A.meta.hosts = ['主机', '', '']; A.pages.hosts = () => '<p>其他页面</p>';
    A.render = () => { renders++; coreRender(); };
    const connect = LinuxTerminalGateway.connect;
    LinuxTerminalGateway.connect = options => ({ ...connect(options), close() { closed.push(options.sessionId); } });
  }
  let source = fs.readFileSync(path.join(__dirname, 'terminal-workspace.js'), 'utf8');
  if (realDOM) source = source.replace('  const MAX_PANES = 4;', `
    window.seedLegacyPanes = () => { const tab = activeTab(); while (tab.paneIds.length < 8) tab.paneIds.push(makePane(tab.hostId).id); render(); };
    const MAX_PANES = 4;`);
  vm.runInContext(source, context, { filename: 'terminal-workspace.js' });
  if (realDOM) A.render();
  return { A, writes, window, navigator, document: window.document, stage: realDOM ? A.root.querySelector('.terminal-stage') : stage, button, events, elements, terminals, connections, confirmations, toastElement, dialogs, observers, closed, content,
    mount() { A.pages.terminal(); while (timers.length) timers.shift()(); },
    get renders() { return renders; }, get fits() { return fits; }, get toast() { return toast; } };
}

test('初始灰色不自动检测，显式诊断串行更新且保留终端全屏节点', async () => {
  const s = setup(false, true), action = () => s.A.actions['terminal-diagnose']({ dataset: { host: 'asset-prod' } });
  const row = () => s.A.root.querySelector('[data-host="asset-prod"]').closest('.terminal-asset-row');
  assert.equal(row().dataset.reachability, 'unknown');
  assert.match(row().textContent, /未检测/);
  let calls = 0, resolve;
  s.window.QiyunAPI.diagnoseAsset = () => { calls++; return new Promise(done => { resolve = done; }); };
  s.A.actions['terminal-open']({ dataset: { host: 'asset-prod' } });
  const id = s.A.terminalWorkspace.snapshot().tabs[0].activePaneId;
  const node = s.A.root.querySelector(`[data-terminal-xterm="${id}"]`), term = s.terminals.get(id);
  await s.A.actions['terminal-fullscreen']();
  const pending = action(); assert.equal(row().dataset.reachability, 'checking');
  await action(); assert.equal(calls, 1);
  resolve({ ok: false, stages: [{ stage: 'authenticate', ok: false, detail: '认证失败' }] }); await pending;
  assert.equal(row().dataset.reachability, 'failed'); assert.match(row().textContent, /SSH 连接失败/);
  assert.doesNotMatch(row().textContent, /网络断开/);
  assert.equal(s.document.fullscreenElement, s.stage); assert.equal(node.isConnected, true); assert.equal(s.terminals.get(id), term);
  s.window.QiyunAPI.diagnoseAsset = async () => ({ ok: true }); await action();
  assert.equal(row().dataset.reachability, 'reachable');
  s.window.QiyunAPI.diagnoseAsset = async () => { throw Error('HTTP 503'); }; await action();
  assert.equal(row().dataset.reachability, 'unknown');
  for (const stage of ['host_key', 'resolve', 'credential_parse']) {
    s.window.QiyunAPI.diagnoseAsset = async () => ({ ok: false, stages: [{ stage, ok: false }] }); await action();
    assert.equal(row().dataset.reachability, 'unknown');
  }
  s.window.QiyunAPI.diagnoseAsset = async () => ({ ok: false, stages: [{ stage: 'tcp', ok: false }] }); await action();
  assert.equal(row().dataset.reachability, 'failed'); assert.match(row().textContent, /TCP 失败/);
});

test('会话事件更新同一资产，主动关闭和网关异常不判设备不通，迟到诊断不覆盖就绪', async () => {
  const s = setup(false, true), health = () => s.A.root.querySelector('[data-host="asset-prod"]').closest('.terminal-asset-row').dataset.reachability;
  s.A.terminalWorkspace.openHost('asset-prod');
  const tab = s.A.terminalWorkspace.snapshot().tabs[0], event = s.connections.get(tab.activePaneId).onStatus;
  assert.equal(health(), 'reachable');
  event('error', { code: 'SSH_CONNECT_FAILED', message: '认证失败' }); assert.equal(health(), 'failed');
  event('offline'); assert.equal(health(), 'unknown');
  event('error'); assert.equal(health(), 'unknown');
  event('unexpected'); assert.equal(health(), 'unknown');
  let resolve; s.window.QiyunAPI.diagnoseAsset = () => new Promise(done => { resolve = done; });
  const pending = s.A.actions['terminal-diagnose']({ dataset: { host: 'asset-prod' } });
  event('connected'); resolve({ ok: false, stages: [{ stage: 'tcp', ok: false }] }); await pending;
  assert.equal(health(), 'reachable');
  s.A.actions['terminal-close-tab']({ dataset: { id: tab.id } }); assert.equal(health(), 'unknown');
  event('error', { code: 'SSH_CONNECT_FAILED' }); assert.equal(health(), 'unknown');
  s.A.actions['terminal-open']({ dataset: { host: 'asset-pending' } });
  assert.equal(s.A.terminalWorkspace.snapshot().tabs.length, 0);
});

test('真实网关透传 SSH 错误类型，主动关闭不产生离线事件', async () => {
  const sockets = [], statuses = [], window = { sessionStorage: { getItem() { return ''; } } };
  class WebSocket {
    static CLOSING = 2;
    constructor() { this.readyState = 1; sockets.push(this); }
    close() { this.onclose(); }
  }
  const context = vm.createContext({ window, WebSocket, TextEncoder, TextDecoder, URL, console,
    location: { href: 'http://localhost/', protocol: 'http:' },
    fetch: async path => ({ ok: true, status: 200, json: async () => path === '/api/v1/assets' ? { items: [] } : { ws_url: '/ws', ws_ticket: 'mock' } }) });
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'terminal-gateway.js'), 'utf8'), context);
  const handle = window.LinuxTerminalGateway.connect({ hostId: 'mock', onStatus: (state, detail) => statuses.push({ state, detail }) });
  await new Promise(resolve => setImmediate(resolve));
  sockets[0].onmessage({ data: JSON.stringify({ type: 'ready' }) });
  assert.equal(statuses.at(-1).state, 'connected');
  sockets[0].onmessage({ data: JSON.stringify({ type: 'error', code: 'SSH_CONNECT_FAILED', message: '认证失败' }) });
  assert.equal(statuses.at(-1).detail.code, 'SSH_CONNECT_FAILED');
  const count = statuses.length; handle.close(); assert.equal(statuses.length, count);
});

test('verified asset opens one reusable real terminal tab', () => {
  const { A } = setup();
  A.terminalWorkspace.openHost('asset-prod');
  A.terminalWorkspace.openHost('asset-prod');
  const state = A.terminalWorkspace.snapshot();
  assert.equal(state.tabs.length, 1);
  assert.equal(state.tabs[0].hostId, 'asset-prod');
  assert.equal(Object.keys(state.panes).length, 1);
  assert.match(state.panes[state.tabs[0].paneIds[0]].output, /正在通过资产身份连接/);
  assert.equal(state.panes[state.tabs[0].paneIds[0]].transport, 'gateway');
});

test('multiple assets create tabs and split panes keep independent sessions', () => {
  const { A } = setup();
  A.terminalWorkspace.openHost('asset-prod');
  A.terminalWorkspace.openHost('asset-test');
  assert.equal(A.terminalWorkspace.snapshot().tabs.length, 2);
  A.actions['terminal-split']({ dataset: { layout: 'columns' } });
  const state = A.terminalWorkspace.snapshot(), tab = state.tabs.find(item => item.id === state.activeTabId);
  assert.equal(tab.layout, 'columns');
  assert.equal(tab.paneIds.length, 2);
  assert.notEqual(tab.paneIds[0], tab.paneIds[1]);
});

test('synchronized input writes to each real pane handle', () => {
  const { A, writes } = setup();
  A.terminalWorkspace.openHost('asset-prod');
  A.actions['terminal-split']({ dataset: { layout: 'rows' } });
  A.actions['terminal-sync']({ checked: true, dataset: {} });
  const state = A.terminalWorkspace.snapshot(), tab = state.tabs[0], [first, second] = tab.paneIds;
  A.terminalWorkspace.runCommand(first, 'whoami');
  assert.deepEqual(writes.get(first), ['whoami\r']);
  assert.deepEqual(writes.get(second), ['whoami\r']);
});

function connected(realToast = false) {
  const s = setup(realToast); s.A.terminalWorkspace.openHost('asset-prod'); s.mount();
  s.id = s.A.terminalWorkspace.snapshot().tabs[0].activePaneId;
  s.term = s.terminals.get(s.id); return s;
}

test('复制只读取活动选区，空选区和权限失败有提示，快捷键保留 Ctrl+C', async () => {
  const s = connected(), action = s.A.actions;
  await action['terminal-copy'](); assert.match(s.toast, /没有选区/);
  s.term.selection = '  中文\n'; await action['terminal-copy'](); assert.equal(s.navigator.copied, '  中文\n');
  s.navigator.clipboard.writeText = async () => { throw Error('denied'); };
  await action['terminal-copy'](); assert.match(s.toast, /原生快捷键/);
  assert.equal(s.term.key({ key: 'c', ctrlKey: true, type: 'keydown' }), true);
  assert.equal(s.term.key({ key: 'v', metaKey: true, type: 'keydown' }), true);
  let prevented = false;
  assert.equal(s.term.key({ key: 'C', ctrlKey: true, shiftKey: true, type: 'keydown', preventDefault() { prevented = true; } }), false);
  assert.equal(prevented, true);
});

test('粘贴保留空白并经过 xterm.paste，不渲染；读权限失败不写入', async () => {
  const s = connected(), before = s.renders;
  await s.A.actions['terminal-paste']();
  assert.deepEqual(s.term.pastes, ['  pwd  ']);
  assert.deepEqual(s.writes.get(s.id), ['\x1b[200~  pwd  \x1b[201~']);
  assert.equal(s.renders, before);
  s.navigator.clipboard.readText = async () => { throw Error('denied'); };
  await s.A.actions['terminal-paste'](); assert.match(s.toast, /Shift\+Insert/);
  assert.equal(s.term.pastes.length, 1);
});

test('同步粘贴逐终端包装且不重复广播，多行取消与超大输入不发送', async () => {
  const s = connected(); s.A.actions['terminal-split']({ dataset: { layout: 'rows' } }); s.mount();
  s.A.actions['terminal-sync']({ checked: true });
  await s.A.actions['terminal-paste']();
  for (const values of s.writes.values()) assert.equal(values.length, 1);
  assert.equal(s.confirmations.length, 1);
  s.navigator.clipboard.readText = async () => ' a\n b\r\n';
  s.window.confirm = () => false; await s.A.actions['terminal-paste']();
  for (const values of s.writes.values()) assert.equal(values.length, 1);
  s.window.confirm = () => true; await s.A.actions['terminal-paste']();
  for (const values of s.writes.values()) assert.equal(values[1], '\x1b[200~ a\n b\r\n\x1b[201~');
  for (const text of ['中'.repeat(17000), '\x00'.repeat(9000)]) {
    s.navigator.clipboard.readText = async () => text;
    await s.A.actions['terminal-paste'](); assert.match(s.toast, /内容过大/);
  }
  for (const values of s.writes.values()) assert.equal(values.length, 2);
});

test('异步读取期间切换、关闭、断开、改变同步或键入均取消粘贴', async () => {
  const mutations = [
    s => s.A.terminalWorkspace.openHost('asset-test'),
    s => s.A.actions['terminal-close-tab']({ dataset: { id: s.A.terminalWorkspace.snapshot().activeTabId } }),
    s => s.connections.get(s.id).onStatus('offline'),
    s => { s.A.actions['terminal-sync']({ checked: true }); s.A.actions['terminal-sync']({ checked: false }); },
    s => s.term.data('x'),
    s => { s.A.state.page = 'hosts'; },
    s => { s.A.actions['terminal-split']({ dataset: { layout: 'rows' } }); s.mount(); }
  ];
  for (const mutate of mutations) {
    const s = connected(); let resolve;
    s.navigator.clipboard.readText = () => new Promise(done => { resolve = done; });
    const pending = s.A.actions['terminal-paste'](); mutate(s); resolve('danger'); await pending;
    assert.equal(s.term.pastes.length, 0); assert.match(s.toast, /取消粘贴/);
  }
});

test('活动分屏切换后复制对应选区，往返切换取消未完成粘贴', async () => {
  const s = connected(); s.A.actions['terminal-split']({ dataset: { layout: 'columns' } }); s.mount();
  const ids = s.A.terminalWorkspace.snapshot().tabs[0].paneIds;
  s.terminals.get(ids[0]).selection = 'first'; s.terminals.get(ids[1]).selection = 'second';
  await s.A.actions['terminal-copy'](); assert.equal(s.navigator.copied, 'second');
  let resolve; s.navigator.clipboard.readText = () => new Promise(done => { resolve = done; });
  const pending = s.A.actions['terminal-paste']();
  for (const id of ids) s.A.actions['terminal-focus-pane']({ dataset: { id } });
  resolve('cancel'); await pending;
  for (const values of s.writes.values()) assert.equal(values.length, 0);
  assert.match(s.toast, /取消粘贴/);
  s.A.actions['terminal-focus-pane']({ dataset: { id: ids[0] } });
  await s.A.actions['terminal-copy'](); assert.equal(s.navigator.copied, 'first');
});

test('同步粘贴跳过断开分屏；并发读取只发送首个有效结果', async () => {
  const s = connected(); s.A.actions['terminal-split']({ dataset: { layout: 'columns' } });
  s.connections.get(s.id).onStatus('offline'); s.mount();
  s.A.actions['terminal-sync']({ checked: true });
  const resolvers = []; s.navigator.clipboard.readText = () => new Promise(done => resolvers.push(done));
  const a = s.A.actions['terminal-paste'](), b = s.A.actions['terminal-paste']();
  resolvers[1]('second'); await b; resolvers[0]('first'); await a;
  assert.equal(s.writes.get(s.id).length, 0);
  const active = s.A.terminalWorkspace.snapshot().tabs[0].activePaneId;
  assert.deepEqual(s.terminals.get(active).pastes, ['second']); assert.match(s.toast, /取消粘贴/);
});

test('原生粘贴执行相同确认和大小校验，断开会话拒绝输入', async () => {
  const s = connected(); let stopped = 0;
  const paste = text => s.elements.get(s.id).events.paste({ preventDefault() { stopped++; }, stopImmediatePropagation() {}, clipboardData: { getData() { return text; } } });
  paste('  a\n'); assert.equal(s.confirmations.length, 1); assert.deepEqual(s.term.pastes, ['  a\n']);
  paste('x'.repeat(65536)); assert.match(s.toast, /内容过大/); assert.equal(stopped, 2);
  s.connections.get(s.id).onStatus('offline');
  s.term.data('not sent'); await s.A.actions['terminal-paste']();
  assert.match(s.toast, /未连接/); assert.equal(s.writes.get(s.id).length, 1);
});

test('全屏进入、Esc 和退出更新按钮及 fit，不重建终端，错误有提示', async () => {
  const s = connected(), before = s.renders, fit = s.fits;
  await s.A.actions['terminal-fullscreen']();
  assert.equal(s.document.fullscreenElement, s.stage); assert.equal(s.button['aria-pressed'], 'true');
  s.document.fullscreenElement = null; s.events.fullscreenchange();
  assert.equal(s.button.textContent, '全屏');
  await s.A.actions['terminal-fullscreen'](); await s.A.actions['terminal-fullscreen']();
  assert.equal(s.document.fullscreenElement, null); assert.ok(s.fits > fit);
  assert.equal(s.renders, before); assert.equal(s.term.disposed, undefined);
  s.stage.requestFullscreen = async () => { throw Error('denied'); };
  await s.A.actions['terminal-fullscreen'](); assert.match(s.toast, /失败/);
  s.events.fullscreenerror(); assert.match(s.toast, /拒绝/);
  s.stage.requestFullscreen = undefined;
  await s.A.actions['terminal-fullscreen'](); assert.match(s.toast, /不支持/);
  s.A.actions['terminal-close-tab']({ dataset: { id: s.A.terminalWorkspace.snapshot().activeTabId } });
  assert.doesNotThrow(() => s.events.fullscreenchange());
});

test('真实 core toast：全屏复制成功、失败及空选区不重绘并保留终端', async () => {
  const s = connected(true), before = s.renders, element = s.elements.get(s.id);
  await s.A.actions['terminal-fullscreen']();
  for (const text of ['单行', ' 多行\r\n中文\n', '']) {
    s.term.selection = text;
    await s.A.actions['terminal-copy']();
    assert.match(s.toastElement.textContent, text ? /已复制/ : /没有选区/);
    assert.equal(s.term.selection, text);
    assert.equal(s.toastElement.parentNode, s.stage);
  }
  s.term.selection = '保留\n选区';
  s.navigator.clipboard.writeText = async () => { throw Error('denied'); };
  await s.A.actions['terminal-copy']();
  assert.match(s.toastElement.textContent, /无法访问/);
  assert.equal(s.term.selection, '保留\n选区');
  assert.equal(s.renders, before); assert.equal(s.elements.get(s.id), element);
  assert.equal(s.document.fullscreenElement, s.stage); assert.equal(s.term.disposed, undefined);
  s.mount(); assert.equal(s.toastElement.hidden, true);
  await s.A.actions['terminal-fullscreen'](); s.A.toast('普通提示');
  assert.equal(s.toastElement.parentNode, s.A.root);
});

test('会话状态通知不重建全屏节点、终端及选区', async () => {
  const s = connected(), before = s.renders;
  await s.A.actions['terminal-fullscreen'](); s.term.selection = 'a\nb';
  for (const status of ['connected', 'offline', 'connected', 'error']) s.connections.get(s.id).onStatus(status);
  s.mount();
  assert.equal(s.renders, before); assert.equal(s.terminals.get(s.id), s.term);
  assert.equal(s.term.disposed, undefined); assert.equal(s.term.selection, 'a\nb');
  assert.equal(s.document.fullscreenElement, s.stage);
});

test('全屏多行粘贴在页面内确认，取消和确认期间状态变更不发送', async () => {
  for (const decision of ['cancel', 'accept', 'offline', 'escape']) {
    const s = connected(), before = s.renders;
    await s.A.actions['terminal-fullscreen']();
    s.navigator.clipboard.readText = async () => 'a\nb';
    const pending = s.A.actions['terminal-paste'](); await new Promise(setImmediate);
    const dialog = s.dialogs[0]; assert.ok(dialog); assert.equal(dialog.parentNode, s.stage);
    assert.equal(s.confirmations.length, 0);
    if (decision === 'offline') s.connections.get(s.id).onStatus('offline');
    if (decision === 'escape') dialog.events.cancel({ preventDefault() {} });
    else dialog.children[decision === 'cancel' ? 1 : 2].events.click();
    await pending;
    assert.equal(s.term.pastes.length, decision === 'accept' ? 1 : 0);
    assert.equal(dialog.removed, true); assert.equal(s.renders, before);
    assert.equal(s.document.fullscreenElement, s.stage); assert.equal(s.term.disposed, undefined);
  }
});

test('真实 DOM：分屏、关闭、切换及同主机新标签保留全屏节点和存活实例', async () => {
  const s = setup(false, true), { A } = s;
  A.terminalWorkspace.openHost('asset-prod');
  const first = A.terminalWorkspace.snapshot().tabs[0], id = first.activePaneId;
  const term = s.terminals.get(id), node = A.root.querySelector(`[data-terminal-xterm="${id}"]`), shell = A.root.querySelector('.terminal-shell');
  term.selection = '保留选择'; s.connections.get(id).onData('保留输出\r\n'); const output = term.output;
  await A.actions['terminal-fullscreen']();
  s.stage.requestFullscreen = () => { throw Error('禁止自动重新全屏'); };
  const check = () => {
    assert.equal(s.document.fullscreenElement, s.stage);
    assert.equal(A.root.querySelector('.terminal-shell'), shell);
    assert.equal(A.root.querySelector('.terminal-stage'), s.stage);
    assert.equal(A.root.querySelector(`[data-terminal-xterm="${id}"]`), node);
    assert.equal(s.terminals.get(id), term); assert.equal(term.disposed, undefined);
    assert.equal(term.selection, '保留选择'); assert.equal(term.output, output);
  };
  for (const layout of ['rows', 'columns', 'grid']) { A.actions['terminal-split']({ dataset: { layout } }); check(); }
  const split = A.terminalWorkspace.snapshot().tabs[0].activePaneId;
  A.actions['terminal-close-pane']({ dataset: { id: split } }); check();
  assert.equal(s.terminals.get(split).disposed, true); assert.ok(s.closed.includes(split));
  assert.ok(s.observers.find(o => o.element.dataset.terminalXterm === split).disconnected);
  A.actions['terminal-open-new']({ dataset: { host: 'asset-prod' } }); check();
  const second = A.terminalWorkspace.snapshot().tabs[1];
  assert.equal(second.sync, false); assert.notEqual(second.activePaneId, id);
  const fit = s.fits; s.observers.find(o => o.element === node).fn(); assert.equal(s.fits, fit);
  A.actions['terminal-tab']({ dataset: { id: first.id } }); check();
  A.render(); check();
  A.actions['terminal-close-tab']({ dataset: { id: second.id } }); check();
  A.state.page = 'hosts'; A.render(); assert.equal(s.document.fullscreenElement, null);
  A.state.page = 'terminal'; A.render();
  assert.equal(A.root.querySelector('.terminal-shell'), shell); assert.equal(s.terminals.get(id), term); assert.equal(term.disposed, undefined);
  A.actions['terminal-close-tab']({ dataset: { id: first.id } });
  assert.equal(term.disposed, true); assert.equal(s.closed.length, s.connections.size);
});

test('独立 SSH 默认不广播，隐藏来源按所属标签同步且不影响其他标签', () => {
  const s = setup(false, true), { A } = s;
  A.terminalWorkspace.openHost('asset-prod'); const first = A.terminalWorkspace.snapshot().tabs[0];
  A.actions['terminal-open-new']({ dataset: { host: 'asset-prod' } }); const second = A.terminalWorkspace.snapshot().tabs[1];
  A.terminalWorkspace.openHost('asset-prod'); assert.equal(A.terminalWorkspace.snapshot().tabs.length, 2);
  s.terminals.get(first.activePaneId).data('first'); s.terminals.get(second.activePaneId).binary('second');
  assert.deepEqual(s.writes.get(first.activePaneId), ['first']); assert.deepEqual(s.writes.get(second.activePaneId), ['second']);
  A.actions['terminal-split']({ dataset: { layout: 'rows' } }); A.actions['terminal-sync']({ checked: true });
  const split = A.terminalWorkspace.snapshot().tabs[0].activePaneId;
  A.actions['terminal-tab']({ dataset: { id: second.id } });
  s.terminals.get(first.activePaneId).data('hidden');
  A.terminalWorkspace.runCommand(first.activePaneId, 'pwd');
  assert.deepEqual(s.writes.get(split), ['hidden', 'pwd\r']);
  assert.deepEqual(s.writes.get(second.activePaneId), ['second']);
  assert.equal(A.terminalWorkspace.snapshot().activeTabId, second.id);
});

test('所有分屏入口共用四窗上限，不创建额外连接', () => {
  const s = setup(false, true), { A } = s; A.terminalWorkspace.openHost('asset-prod');
  const entries = [
    () => A.terminalWorkspace.openHost('asset-test', 'split'),
    () => A.actions['terminal-open-split']({ dataset: { host: 'asset-prod' } }),
    ...['rows', 'columns', 'grid'].map(layout => () => A.actions['terminal-split']({ dataset: { layout } }))
  ];
  for (const entry of entries) entry();
  for (const entry of entries) { entry(); assert.equal(s.connections.size, 4); assert.match(s.toast, /最多显示 4/); }
  assert.equal(A.terminalWorkspace.snapshot().tabs[0].paneIds.length, 4);
  assert.equal(A.root.querySelectorAll('[data-action="terminal-open-new"]').length, 3);
});

test('已有八分屏原样保留，所有新增入口拒绝，关闭后正常释放', () => {
  const s = setup(false, true), { A } = s; A.terminalWorkspace.openHost('asset-prod'); s.window.seedLegacyPanes();
  const panes = A.terminalWorkspace.snapshot().tabs[0].paneIds;
  assert.equal(panes.length, 8);
  A.actions['terminal-open-split']({ dataset: { host: 'asset-test' } });
  A.terminalWorkspace.openHost('asset-prod', 'split');
  for (const layout of ['columns', 'rows', 'grid']) A.actions['terminal-split']({ dataset: { layout } });
  assert.equal(s.connections.size, 8); assert.equal(A.root.querySelectorAll('.terminal-pane').length, 8);
  for (const id of panes) assert.equal(s.terminals.get(id).disposed, undefined);
  A.actions['terminal-close-pane']({ dataset: { id: panes[7] } });
  assert.equal(A.terminalWorkspace.snapshot().tabs[0].paneIds.length, 7); assert.equal(s.terminals.get(panes[7]).disposed, true);
});

test('黑底浅灰字主题明确配置全部 16 ANSI 色', () => {
  const s = setup(false, true); s.A.terminalWorkspace.openHost('asset-prod');
  const term = [...s.terminals.values()][0], theme = term.options.theme;
  assert.equal(theme.background, '#000000'); assert.equal(theme.foreground, '#d0d0d0');
  const keys = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white'];
  const colors = keys.flatMap(key => [theme[key], theme['bright' + key[0].toUpperCase() + key.slice(1)]]);
  assert.equal(new Set(colors).size, 16); colors.forEach(color => assert.match(color, /^#[0-9a-f]{6}$/));
  const id = s.A.terminalWorkspace.snapshot().tabs[0].activePaneId;
  const ansi = '\x1b[31mred\x1b[0m\x1b[92mgreen\x1b[0m'; s.connections.get(id).onData(ansi); assert.ok(term.output.endsWith(ansi));
});

test('unverified asset is blocked from terminal actions', () => {
  const s = setup();
  s.A.actions['terminal-open']({ dataset: { host: 'asset-pending' } });
  assert.match(s.toast, /尚未通过 SSH 指纹核验/);
  assert.equal(s.A.terminalWorkspace.snapshot().tabs.length, 0);
});
