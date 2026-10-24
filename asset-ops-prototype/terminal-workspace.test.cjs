const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');

function setup() {
  let toast = '';
  const writes = new Map();
  const assets = [
    { id: 'asset-prod', name: '生产主机', type: 'Linux', os: 'Ubuntu', ip: '10.0.0.10', owner: 'ops', department: '研发部', environment: 'production', status: '在线', hostKeyStatus: 'verified', hostKeyFingerprint: 'SHA256:prod' },
    { id: 'asset-test', name: '测试主机', type: 'Linux', os: 'Debian', ip: '10.0.0.11', owner: 'qa', department: '测试部', environment: 'testing', status: '在线', hostKeyStatus: 'verified', hostKeyFingerprint: 'SHA256:test' },
    { id: 'asset-pending', name: '待核验主机', type: 'Linux', os: 'Linux', ip: '10.0.0.12', owner: 'ops', department: '研发部', environment: 'production', status: '待核验', hostKeyStatus: 'pending' }
  ];
  const root = { addEventListener() {}, querySelector() { return null; } };
  const A = {
    root, pages: {}, actions: {}, state: { page: 'terminal' }, meta: {},
    nav: [['基础设施', 'hosts', '主机与远程', 'monitor', '']], assets: [],
    escape: value => String(value ?? '').replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char])),
    button: (label, action, primary, attrs = '') => `<button data-action="${action}" ${attrs}>${label}</button>`,
    badge: value => String(value), table: () => '', panel: (title, body) => title + body, metric: () => '',
    modal() {}, closeModal() {}, render() {}, navigate(page) { A.state.page = page; }, toast(message) { toast = message; }
  };
  const QiyunAPI = { getAssets: () => assets.map(item => ({ ...item })), onAssets() {}, createAsset() {}, scanHostKey() {}, approveHostKey() {}, testAsset() {}, refreshAssets() {} };
  const LinuxTerminalGateway = {
    connect(options) {
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
  const context = vm.createContext({ window: { AssetDemo: A, QiyunAPI, LinuxTerminalGateway, TrzszFilter }, console, URL, Number, Date, Map, Set, Promise, TextDecoder, TextEncoder, setTimeout, clearTimeout, setInterval, clearInterval });
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'terminal-workspace.js'), 'utf8'), context, { filename: 'terminal-workspace.js' });
  return { A, writes, get toast() { return toast; } };
}

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

test('unverified asset is blocked from terminal actions', () => {
  const s = setup();
  s.A.actions['terminal-open']({ dataset: { host: 'asset-pending' } });
  assert.match(s.toast, /尚未通过 SSH 指纹核验/);
  assert.equal(s.A.terminalWorkspace.snapshot().tabs.length, 0);
});
