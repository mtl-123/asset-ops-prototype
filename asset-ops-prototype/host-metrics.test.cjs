const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

// host-metrics.js 是纯客户端取数层：只用 QiyunAPI.request，渲染交给 infra.js。
async function boot(handler) {
  const document = { hidden: false };
  const window = {
    QiyunAPI: { request: async (url, options) => handler(url, options) },
    document,
    addEventListener() {}
  };
  const timers = [];
  const context = {
    window, document, console: { warn() {}, log() {} },
    setInterval: (fn, ms) => { timers.push({ fn, ms }); return timers.length; },
    clearInterval: id => { const t = timers.find((_, i) => i === id - 1); if (t) t.cleared = true; },
    Promise, Map, Number, Date, Math, JSON
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'host-metrics.js'), 'utf8'), context);
  // 模块装载时会立即拉一次；等它落地，避免首次 refresh 还在飞行中被后续调用跳过。
  await new Promise(resolve => setImmediate(resolve));
  return { window, timers };
}

const okItem = {
  asset_id: 'a1', status: 'ok', interval_seconds: 60, duration_ms: 420,
  collected_at: '2026-10-25T03:00:00Z',
  snapshot: { hostname: 'node-01', cpu_percent: 12.5, mem_percent: 40, disk_percent: 55, cores: 8, load1: 0.5 }
};

test('refresh 建立 asset_id 索引并暴露汇总', async () => {
  const s = await boot(async url => {
    assert.equal(url, '/api/v1/metrics');
    return { interval_seconds: 60, items: [okItem, { asset_id: 'a2', status: 'error', error: 'i/o timeout' }] };
  });
  const { HostMetrics } = s.window;
  await HostMetrics.refresh();
  const summary = HostMetrics.summary();
  assert.equal(summary.total, 2);
  assert.equal(summary.ok, 1);
  assert.equal(summary.failed, 1);
  assert.equal(summary.interval, 60);
  assert.equal(HostMetrics.get('a1').snapshot.cpu_percent, 12.5);
  assert.equal(HostMetrics.get('ghost'), null);
});

test('refresh 失败时保留上次结果并记录错误，不清空列表', async () => {
  let fail = false;
  const s = await boot(async () => {
    if (fail) throw new Error('Failed to fetch');
    return { interval_seconds: 60, items: [okItem] };
  });
  const { HostMetrics } = s.window;
  await HostMetrics.refresh();
  fail = true;
  await HostMetrics.refresh();
  assert.equal(HostMetrics.summary().total, 1, '旧数据必须保留');
  assert.match(HostMetrics.summary().error.message, /Failed to fetch/);
  assert.equal(HostMetrics.get('a1').status, 'ok');
});

test('collect 单台主机后立即更新该项并通知订阅者', async () => {
  const calls = [];
  const s = await boot(async (url, options) => {
    calls.push({ url, method: options?.method });
    if (url === '/api/v1/metrics/a1/collect') return { ...okItem, snapshot: { ...okItem.snapshot, cpu_percent: 88 } };
    return { interval_seconds: 60, items: [okItem] };
  });
  const { HostMetrics } = s.window;
  await HostMetrics.refresh();
  let notified = 0;
  HostMetrics.on(() => { notified++; });
  const item = await HostMetrics.collect('a1');
  assert.equal(item.snapshot.cpu_percent, 88);
  assert.equal(HostMetrics.get('a1').snapshot.cpu_percent, 88);
  assert.equal(notified, 1);
  assert.deepEqual(calls.at(-1), { url: '/api/v1/metrics/a1/collect', method: 'POST' });
});

test('轮询间隔由服务端下发，后台标签页跳过刷新', async () => {
  let requests = 0;
  const s = await boot(async () => { requests++; return { interval_seconds: 45, items: [okItem] }; });
  const { HostMetrics } = s.window;
  await HostMetrics.refresh();
  HostMetrics.start();
  const timer = s.timers.at(-1);
  assert.equal(timer.ms, 45000, '间隔取服务端值，但不低于 30 秒');
  const base = requests;
  s.window.document.hidden = true;
  timer.fn();
  await new Promise(r => setImmediate(r));
  assert.equal(requests, base, '隐藏页签不应触发采样');
  s.window.document.hidden = false;
  timer.fn();
  await new Promise(r => setImmediate(r));
  assert.equal(requests, base + 1);
});

test('服务未就绪时 collect 明确报错而不是静默失败', async () => {
  const window = { QiyunAPI: undefined };
  const context = { window, document: { hidden: false }, console: { warn() {} }, setInterval: () => 1, clearInterval() {}, Promise, Map, Number, Date, Math, JSON };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'host-metrics.js'), 'utf8'), context);
  await assert.rejects(() => window.HostMetrics.collect('a1'), /服务未就绪/);
});
