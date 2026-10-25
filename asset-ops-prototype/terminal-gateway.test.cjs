const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

async function setup() {
  let socket; const output = [];
  class WS {
    static OPEN = 1; static CLOSING = 2;
    constructor() { socket = this; this.readyState = 1; this.bufferedAmount = 0; this.sent = []; }
    send(data) { this.sent.push(data); }
    close() { this.readyState = 3; }
  }
  const window = { sessionStorage: { getItem: () => '' } };
  const context = { window, TextEncoder, TextDecoder, Uint8Array, ArrayBuffer, URL, WebSocket: WS, console,
    location: { href: 'http://localhost/', protocol: 'http:' },
    fetch: async url => ({ ok: true, status: 200, json: async () => url.includes('terminal-sessions') ? { ws_url: '/ws', ws_ticket: 'test' } : { items: [] } }) };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'terminal-gateway.js'), 'utf8'), context);
  const handle = window.LinuxTerminalGateway.connect({ hostId: 'test', onData: text => output.push(text) });
  await new Promise(resolve => setImmediate(resolve));
  return { socket, handle, output };
}

test('传输帧拆分至64KiB并保留全部字节', async () => {
  const { socket, handle } = await setup();
  const data = new Uint8Array(10 * 1024 * 1024).fill(123);
  handle.write(data);
  assert.equal(socket.sent.length, 160);
  assert.ok(socket.sent.every(frame => frame.byteLength <= 65536));
  assert.deepEqual(Buffer.concat(socket.sent.map(frame => Buffer.from(frame))), Buffer.from(data));
});

test('发送积压时关闭而不是无限缓存', async () => {
  const { socket, handle, output } = await setup();
  socket.bufferedAmount = 32 * 1024 * 1024;
  handle.write('a');
  assert.equal(socket.readyState, 3);
  assert.equal(socket.sent.length, 0);
  assert.match(output.join(''), /32 MiB/);
});

test('安装结果不会作为终端控制序列输出且失败不关闭终端', async () => {
  const { socket, output } = await setup();
  socket.onmessage({ data: JSON.stringify({ type: 'transfer_tools', ok: false, message: '失败\x1b[31m，重连重试' }) });
  assert.ok(!output.join('').includes('\x1b'));
  assert.match(output.join(''), /重连重试/);
  assert.equal(socket.readyState, 1);
});

test('浏览器库保持分块落盘并等待关闭后确认完成', () => {
  const source = fs.readFileSync(path.join(__dirname, 'vendor/trzsz/trzsz.js'), 'utf8');
  assert.match(source, /yield this\.writer\.write\(t\)/);
  assert.match(source, /yield l\.closeFile\(\),yield this\.recvFileMD5/);
  assert.match(source, /typeof window\.showDirectoryPicker/);
  assert.ok(!source.includes('createObjectURL'));
});

// 资产清单拉取失败时曾只写 console.warn，界面把「没读到」当成「台账为空」，
// 用户看到的就是资产消失、没有远程主机入口。以下用例锁住可观测的载入状态。
function assetContext(items = [{ id: 'a1', asset_code: 'SVR-1', name: '生产主机', address: '10.0.0.5', host_key_status: 'verified' }], fail = false) {
  const window = { sessionStorage: { getItem: () => '', setItem() {}, removeItem() {} } };
  const errors = [];
  const context = { window, TextEncoder, TextDecoder, Uint8Array, ArrayBuffer, URL, console: { warn(){}, log(){} },
    setTimeout: (fn, ms) => { errors.push({ fn, ms }); return errors.length; },
    location: { href: 'http://localhost/', protocol: 'http:' },
    fetch: async url => {
      if (url.includes('/assets') && fail) throw new Error('Failed to fetch');
      return { ok: true, status: 200, json: async () => url.includes('terminal-sessions') ? { ws_url: '/ws', ws_ticket: 't' } : { items } };
    } };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'terminal-gateway.js'), 'utf8'), context);
  return { api: window.QiyunAPI, retries: errors };
}

test('资产清单载入成功后才标记为已载入', async () => {
  const { api } = assetContext();
  assert.equal(api.assetStatus().loaded, false);
  await api.refreshAssets();
  assert.equal(api.assetStatus().loaded, true);
  assert.equal(api.getAssets().length, 1);
});

const tick = () => new Promise(resolve => setImmediate(resolve));

test('资产清单载入失败保留错误与自动重试，不伪装成空台账', async () => {
  const { api, retries } = assetContext([], true);
  await tick();
  const status = api.assetStatus();
  assert.equal(status.loaded, false);
  assert.match(status.error.message, /Failed to fetch/);
  assert.equal(status.retrying, true);
  assert.ok(retries.length >= 1);
  assert.ok(retries[0].ms >= 2000);
});

test('载入失败会通知界面，界面据此重绘而不是停在空列表', async () => {
  const { api } = assetContext([], true);
  await tick();
  const seen = [];
  api.onAssetsError(error => seen.push(error.message));
  await api.refreshAssets().catch(() => {});
  assert.deepEqual(seen, ['Failed to fetch']);
});
