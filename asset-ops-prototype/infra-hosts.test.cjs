const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

// infra.js 在打包顺序里位于 terminal-gateway.js 之前，装载时 window.QiyunAPI 尚不存在。
// 这里保留该顺序，验证它靠 qiyun:api-ready 事件补订阅，而不是静默丢掉订阅。
function boot() {
  let renders = 0;
  const listeners = new Map();
  const D = {
    state: { page: 'hosts' }, pages: {}, actions: {}, meta: {}, assets: [],
    escape: value => String(value == null ? '' : value),
    button: (label, action) => `<button data-action="${action}">${label}</button>`,
    badge: value => String(value), table: () => '', metric: () => '', panel: (title, body) => body,
    icon: () => '', root: { querySelector: () => null },
    render() { renders++; }, toast() {}, modal() {}, closeModal() {}, navigate() {}
  };
  const window = { AssetDemo: D, addEventListener(name, fn) { listeners.set(name, fn); } };
  const context = { window, console, URL, Number, Date, Map, Set, Promise, TextEncoder, TextDecoder, setTimeout, clearTimeout, document: undefined };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'infra.js'), 'utf8'), context);
  return {
    window, D, get renders() { return renders; },
    fireReady() { listeners.get('qiyun:api-ready')?.(); }
  };
}

const twoAssets = [
  { id: 'a1', name: '生产主机', ip: '10.0.0.5', status: '在线', sshPort: 22, owner: 'ops', department: 'IT', hostKeyFingerprint: 'SHA256:x', assetCode: 'SVR-1' },
  { id: 'a2', name: '测试主机', ip: '10.0.0.6', status: '离线', sshPort: 2200, owner: 'qa', department: 'QA', hostKeyFingerprint: 'SHA256:y', assetCode: 'SVR-2' }
];

function installApi(target, assets) {
  const listeners = [];
  target.QiyunAPI = {
    getAssets: () => assets.map(item => ({ ...item })),
    assetStatus: () => ({ loaded: true, error: null }),
    onAssets(fn) { listeners.push(fn); return () => {}; },
    onAssetsError() { return () => {}; }
  };
  return { emit: () => listeners.forEach(fn => fn()) };
}

test('数据层晚于 infra.js 装载时，靠就绪事件补订阅并同步主机清单', () => {
  const s = boot();
  assert.equal(s.D.infrastructure.getHosts().length, 0, '装载时还没有数据层，应为空');
  const api = installApi(s.window, twoAssets);
  s.fireReady();
  const hosts = s.D.infrastructure.getHosts();
  assert.equal(hosts.length, 2);
  assert.equal(hosts[0].ip, '10.0.0.5');
  assert.equal(hosts[0].source, 'api');
  assert.equal(hosts[1].ports, '2200');
  api.emit();
  assert.equal(s.D.infrastructure.getHosts().length, 2, '重复通知不应产生重复行');
});

test('就绪事件只绑定一次，重复触发不重复追加', () => {
  const s = boot();
  installApi(s.window, twoAssets);
  s.fireReady(); s.fireReady(); s.fireReady();
  assert.equal(s.D.infrastructure.getHosts().length, 2);
});

test('资产清单未载入时保留已有主机，不清零', () => {
  const s = boot();
  installApi(s.window, twoAssets);
  s.fireReady();
  assert.equal(s.D.infrastructure.getHosts().length, 2);
  s.window.QiyunAPI.getAssets = () => [];
  s.window.QiyunAPI.assetStatus = () => ({ loaded: false, error: new Error('Failed to fetch') });
  s.window.QiyunAPI.onAssets?.(() => {});
  s.D.actions['infra-refresh']();
  return new Promise(resolve => setImmediate(() => {
    assert.equal(s.D.infrastructure.getHosts().length, 2, '载入失败不能把主机清零');
    resolve();
  }));
});

// Windows hosts come from the same Go asset store but are managed over RDP.
// They must not be displayed as "待核验" (they never get an SSH host key) and
// they must show the RDP port rather than the unused SSH port.
test('Windows 资产按 RDP 同步，并保留托管账号与端口', () => {
  const s = boot();
  installApi(s.window, [{
    id: 'win-1', name: 'win-rds-01', ip: '10.20.30.33', type: 'Windows',
    os: 'Windows Server 2022', status: '待连接', sshPort: 22, rdpPort: 3390,
    username: 'CORP\\ops', assetCode: 'WIN-1', assetType: 'windows_host',
    hostKeyStatus: 'pending', hostKeyFingerprint: '', macAddress: 'AA:BB:CC:DD:EE:FF'
  }]);
  s.fireReady();
  const hosts = s.D.infrastructure.getHosts();
  assert.equal(hosts.length, 1);
  assert.equal(hosts[0].type, 'Windows');
  assert.equal(hosts[0].status, '待连接');
  assert.equal(hosts[0].ports, '3390');
  assert.equal(hosts[0].rdpPort, 3390);
  assert.equal(hosts[0].idle, 'RDP · 本机 mstsc');
  assert.equal(hosts[0].username, 'CORP\\ops');
});

// 主机页原来只有“详情/远程”，用户添加资产后无法在同一个页面修改或删除。
test('主机列表提供搜索输入以及编辑、删除动作', () => {
  const s = boot();
  installApi(s.window, twoAssets);
  s.fireReady();
  const html = s.D.pages.hosts();
  assert.ok(html.includes('id="infra-host-search"'), '主机页应有搜索框');
  assert.ok(html.includes('搜索名称、IP、资产编号、责任人'), '搜索框应有明确占位提示');
  assert.ok(html.includes('infra-host-edit'), '主机行应有编辑按钮');
  assert.ok(html.includes('infra-host-delete'), '主机行应有删除按钮');
  const calls = [];
  s.D.assetEditor = id => calls.push('edit:' + id);
  s.D.deleteAssetConfirm = (id, name) => calls.push('delete:' + id + ':' + name);
  s.D.actions['infra-host-edit']({ dataset: { host: 'a1' } });
  s.D.actions['infra-host-delete']({ dataset: { host: 'a1' } });
  assert.deepEqual(calls, ['edit:a1', 'delete:a1:生产主机']);
});
